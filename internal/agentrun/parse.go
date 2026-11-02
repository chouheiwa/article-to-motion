// Package agentrun 运行编排工具，并把各家 CLI 的结构化输出统一成同一套事件。
//
// am run 与 am studio 共用这一条路径：前者把事件打印成可读文字，后者推给网页。
// 各家的流格式在 2026-11 实测录下，见 testdata/；codebuddy 本机未登录，只录到
// 401 的错误流，结构按与 claude 相同处理，未经完整实测。
package agentrun

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// Kind 是统一事件的种类。
type Kind string

const (
	KindSession    Kind = "session"
	KindText       Kind = "text"
	KindThinking   Kind = "thinking"
	KindToolCall   Kind = "tool-call"
	KindToolResult Kind = "tool-result"
	KindUsage      Kind = "usage"
	KindError      Kind = "error"
	KindDone       Kind = "done"
)

// maxToolOutput 限制工具结果正文的长度：渲染日志、ffprobe 输出动辄几万字，
// 全文推给网页没有意义，原始流另有日志可查。
const maxToolOutput = 4000

// Event 是一条统一事件。只做消息级粒度，不解析 token 增量。
type Event struct {
	Kind      Kind            `json:"kind"`
	Text      string          `json:"text,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Usage     *tools.Usage    `json:"usage,omitempty"`
}

// Parser 把一行原始输出译成零到多条事件。Parser 有状态（会话 id 去重、用量累计），
// 一次运行用一个。
type Parser struct {
	tool    string
	session string
	usage   tools.Usage
}

// NewParser 为工具创建解析器。
func NewParser(tool string) (*Parser, error) {
	if !tools.ValidTools[tool] {
		return nil, fmt.Errorf("未知工具：%s", tool)
	}
	return &Parser{tool: tool}, nil
}

// SessionID 返回目前从流里读到的会话 id。
func (p *Parser) SessionID() string { return p.session }

// Usage 返回累计用量；一条用量事件都没读到时 Empty() 为 true。
func (p *Parser) Usage() tools.Usage { return p.usage }

// Parse 译一行。认不出的行返回 nil，不报错：各家会在流里夹带限流、钩子之类的事件。
func (p *Parser) Parse(line string) []Event {
	tools.AccumulateUsage(p.tool, line, &p.usage)
	var raw map[string]any
	if json.Unmarshal([]byte(line), &raw) != nil {
		return nil
	}
	var events []Event
	switch p.tool {
	case "codex":
		events = parseCodex(raw)
	case "opencode":
		events = parseOpencode(raw)
	default:
		events = parseClaudeLike(raw)
	}
	out := events[:0]
	for _, event := range events {
		if event.Kind == KindSession {
			if event.SessionID == "" || event.SessionID == p.session {
				continue
			}
			p.session = event.SessionID
		}
		out = append(out, event)
	}
	return out
}

func str(value any) string {
	s, _ := value.(string)
	return s
}

func obj(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func rawJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}

func clip(text string) string {
	if len(text) <= maxToolOutput {
		return text
	}
	cut := maxToolOutput
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut] + "\n…（已截断）"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// parseClaudeLike 处理 claude、qoder、codebuddy 共用的 stream-json。
func parseClaudeLike(e map[string]any) []Event {
	var events []Event
	if id := str(e["session_id"]); id != "" {
		events = append(events, Event{Kind: KindSession, SessionID: id})
	}
	switch str(e["type"]) {
	case "assistant":
		content, _ := obj(e["message"])["content"].([]any)
		for _, item := range content {
			block := obj(item)
			switch str(block["type"]) {
			case "text":
				if text := str(block["text"]); text != "" {
					events = append(events, Event{Kind: KindText, Text: text})
				}
			case "thinking":
				if text := str(block["thinking"]); text != "" {
					events = append(events, Event{Kind: KindThinking, Text: text})
				}
			case "tool_use":
				events = append(events, Event{Kind: KindToolCall, Tool: str(block["name"]), CallID: str(block["id"]), Input: rawJSON(block["input"])})
			}
		}
	case "user":
		content, _ := obj(e["message"])["content"].([]any)
		for _, item := range content {
			block := obj(item)
			if str(block["type"]) != "tool_result" {
				continue
			}
			isError, _ := block["is_error"].(bool)
			events = append(events, Event{Kind: KindToolResult, CallID: str(block["tool_use_id"]), Text: clip(toolResultText(block["content"])), IsError: isError})
		}
	case "result":
		if isError, _ := e["is_error"].(bool); isError {
			text := str(e["result"])
			if text == "" {
				text = str(e["subtype"])
			}
			events = append(events, Event{Kind: KindError, Text: text})
		}
	}
	return events
}

func toolResultText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			if text := str(obj(item)["text"]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// parseCodex 处理 codex exec --json 的 JSONL。
func parseCodex(e map[string]any) []Event {
	item := obj(e["item"])
	switch str(e["type"]) {
	case "thread.started":
		return []Event{{Kind: KindSession, SessionID: str(e["thread_id"])}}
	case "item.started":
		if str(item["type"]) == "command_execution" {
			return []Event{{Kind: KindToolCall, Tool: "shell", CallID: str(item["id"]), Input: rawJSON(map[string]any{"command": item["command"]})}}
		}
	case "item.completed":
		switch str(item["type"]) {
		case "agent_message":
			return []Event{{Kind: KindText, Text: str(item["text"])}}
		case "reasoning":
			return []Event{{Kind: KindThinking, Text: str(item["text"])}}
		case "command_execution":
			code, _ := item["exit_code"].(float64)
			return []Event{{Kind: KindToolResult, Tool: "shell", CallID: str(item["id"]), Text: clip(str(item["aggregated_output"])), IsError: code != 0}}
		case "file_change", "mcp_tool_call", "web_search":
			failed := str(item["status"]) == "failed"
			return []Event{{Kind: KindToolResult, Tool: str(item["type"]), CallID: str(item["id"]), Input: rawJSON(item), IsError: failed}}
		case "error":
			return []Event{{Kind: KindError, Text: str(item["message"])}}
		}
	case "turn.failed":
		return []Event{{Kind: KindError, Text: str(obj(e["error"])["message"])}}
	case "error":
		return []Event{{Kind: KindError, Text: str(e["message"])}}
	}
	return nil
}

// parseOpencode 处理 opencode run --format json 的 JSONL（2.x）。
func parseOpencode(e map[string]any) []Event {
	var events []Event
	if id := str(e["sessionID"]); id != "" {
		events = append(events, Event{Kind: KindSession, SessionID: id})
	}
	part := obj(e["part"])
	switch str(e["type"]) {
	case "text":
		if text := str(part["text"]); text != "" {
			events = append(events, Event{Kind: KindText, Text: text})
		}
	case "reasoning":
		if text := str(part["text"]); text != "" {
			events = append(events, Event{Kind: KindThinking, Text: text})
		}
	case "tool_use":
		state := obj(part["state"])
		id, tool := str(part["id"]), str(part["tool"])
		events = append(events, Event{Kind: KindToolCall, Tool: tool, CallID: id, Input: rawJSON(state["input"])})
		switch str(state["status"]) {
		case "completed":
			events = append(events, Event{Kind: KindToolResult, Tool: tool, CallID: id, Text: clip(str(state["output"]))})
		case "error":
			events = append(events, Event{Kind: KindToolResult, Tool: tool, CallID: id, Text: clip(str(state["error"])), IsError: true})
		}
	case "error":
		message := str(obj(e["error"])["message"])
		if message == "" {
			message = str(obj(obj(e["error"])["data"])["message"])
		}
		events = append(events, Event{Kind: KindError, Text: message})
	}
	return events
}
