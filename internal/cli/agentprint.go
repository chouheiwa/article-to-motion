package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/agentrun"
)

// hyperframesCommand 是安全模式下放行给编排工具的 hyperframes 命令前缀，
// 与渲染器的放行写法（scene.RendererAccess）一致，带固定版本。
func hyperframesCommand() string {
	return "npx --yes hyperframes@" + hyperframesVersion
}

// eventPrinter 把统一事件整理成终端里的可读文字。
type eventPrinter struct {
	stdout, stderr io.Writer
}

const summaryLimit = 160

func (p *eventPrinter) print(event agentrun.Event) {
	switch event.Kind {
	case agentrun.KindSession:
		fmt.Fprintf(p.stdout, "会话: %s\n", event.SessionID)
	case agentrun.KindText:
		fmt.Fprintln(p.stdout, strings.TrimRight(event.Text, "\n"))
	case agentrun.KindToolCall:
		fmt.Fprintf(p.stdout, "→ %s %s\n", event.Tool, toolSummary(event.Input))
	case agentrun.KindToolResult:
		if event.IsError {
			fmt.Fprintf(p.stdout, "  ✗ %s\n", firstLine(event.Text))
		}
	case agentrun.KindError:
		fmt.Fprintf(p.stderr, "错误: %s\n", event.Text)
	case agentrun.KindUsage:
		u := event.Usage
		line := fmt.Sprintf("用量: 输入 %d（缓存 %d）/ 输出 %d", u.InputTokens, u.CachedInputTokens, u.OutputTokens)
		if u.CostUSD != nil {
			line += fmt.Sprintf(" / $%.4f", *u.CostUSD)
		}
		fmt.Fprintln(p.stdout, line)
	}
}

// toolSummary 从工具输入里挑最能说明「做了什么」的一个字段。
func toolSummary(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "path", "filePath", "pattern", "url", "description"} {
		if value, ok := fields[key].(string); ok && value != "" {
			return truncate(firstLine(value))
		}
	}
	return ""
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return truncate(text[:index]) + " …"
	}
	return truncate(text)
}

func truncate(text string) string {
	runes := []rune(text)
	if len(runes) <= summaryLimit {
		return text
	}
	return string(runes[:summaryLimit]) + "…"
}
