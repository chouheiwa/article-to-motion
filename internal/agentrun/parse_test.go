package agentrun

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, tool, name string) ([]Event, *Parser) {
	t.Helper()
	parser, err := NewParser(tool)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		events = append(events, parser.Parse(scanner.Text())...)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events, parser
}

func kinds(events []Event, kind Kind) []Event {
	var out []Event
	for _, event := range events {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func lastText(events []Event) string {
	texts := kinds(events, KindText)
	if len(texts) == 0 {
		return ""
	}
	return strings.TrimSpace(texts[len(texts)-1].Text)
}

// 每份 fixture 都是同一个两轮脚本：第一轮记住 PELICAN、创建 note.txt、回复 OK；
// 第二轮续接后问单词。第二轮答对，说明续接拿到了第一轮的上下文。
func TestRecordedStreams(t *testing.T) {
	tests := []struct {
		tool, first, second, session, writeTool string
	}{
		{"claude", "claude-1", "claude-2", "a3dc268c-4696-4ac3-9e12-b3b8c18ba703", "Write"},
		{"codex", "codex-1", "codex-2", "01a19706-15cc-7c23-92b3-d0008785ee57", "shell"},
		{"qoder", "qoder-1", "qoder-2", "c43bb3cd-ea41-495e-8fac-2517596a2fd5", "Write"},
		{"opencode", "opencode-1", "opencode-2", "ses_e68f70edcffeGdFO8OOw9sCE5o", "patch"},
	}
	for _, test := range tests {
		t.Run(test.tool, func(t *testing.T) {
			first, parser := parseFixture(t, test.tool, test.first)
			sessions := kinds(first, KindSession)
			if len(sessions) != 1 || sessions[0].SessionID != test.session || parser.SessionID() != test.session {
				t.Fatalf("会话事件应只出现一次且 id 正确：%+v", sessions)
			}
			if first[0].Kind != KindSession {
				t.Fatalf("会话 id 要最先发出，调用方要立刻落盘：%+v", first[0])
			}
			calls := kinds(first, KindToolCall)
			found := false
			for _, call := range calls {
				if call.Tool == test.writeTool && call.CallID != "" && len(call.Input) > 0 {
					found = true
				}
			}
			if !found {
				t.Fatalf("没有译出 %s 工具调用：%+v", test.writeTool, calls)
			}
			if len(kinds(first, KindToolResult)) == 0 {
				t.Fatal("没有译出工具结果")
			}
			if !strings.Contains(lastText(first), "OK") {
				t.Fatalf("第一轮最后一段正文应含 OK：%q", lastText(first))
			}
			second, parser := parseFixture(t, test.tool, test.second)
			if parser.SessionID() != test.session {
				t.Fatalf("续接后会话 id 变了：%s", parser.SessionID())
			}
			if lastText(second) != "PELICAN" {
				t.Fatalf("第二轮答案 %q", lastText(second))
			}
			if len(kinds(second, KindError)) != 0 {
				t.Fatalf("成功的流不应译出错误：%+v", kinds(second, KindError))
			}
		})
	}
}

func TestRecordedStreamsCarryUsage(t *testing.T) {
	for tool, name := range map[string]string{"claude": "claude-1", "codex": "codex-1", "qoder": "qoder-1", "opencode": "opencode-1"} {
		_, parser := parseFixture(t, tool, name)
		usage := parser.Usage()
		// qoder 按积分计费，token 字段实测全为 0，只能确认读到了用量事件。
		if usage.Empty() || (tool != "qoder" && usage.InputTokens+usage.CachedInputTokens == 0) {
			t.Fatalf("%s 没读到用量：%+v", tool, usage)
		}
	}
}

// qoder 在 dont_ask 下拒绝写文件，工具结果带 is_error。
func TestToolResultErrorsSurvive(t *testing.T) {
	events, _ := parseFixture(t, "qoder", "qoder-1")
	failed := 0
	for _, event := range kinds(events, KindToolResult) {
		if event.IsError && strings.Contains(event.Text, "approval") {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("被拒绝的工具调用应当标成错误")
	}
}

// codebuddy 本机未登录，只录到 401：结果事件 is_error 要译成错误。
func TestCodebuddyErrorStream(t *testing.T) {
	events, parser := parseFixture(t, "codebuddy", "codebuddy-1")
	if parser.SessionID() != "65dd779c-9206-4cc8-8c23-1187b780efc4" {
		t.Fatalf("session=%s", parser.SessionID())
	}
	if len(kinds(events, KindError)) != 1 {
		t.Fatalf("events=%+v", events)
	}
	if !strings.Contains(lastText(events), "401") {
		t.Fatalf("正文应保留 401 原文：%q", lastText(events))
	}
}

func TestParserIgnoresNoise(t *testing.T) {
	parser, err := NewParser("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"", "not json", `{"type":"rate_limit_event"}`, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}`} {
		if events := parser.Parse(line); len(events) != 0 {
			t.Fatalf("%q -> %+v", line, events)
		}
	}
	if _, err := NewParser("bad"); err == nil {
		t.Fatal("未知工具应当报错")
	}
}

func TestCodexFailureEvents(t *testing.T) {
	parser, _ := NewParser("codex")
	events := parser.Parse(`{"type":"turn.failed","error":{"message":"boom"}}`)
	events = append(events, parser.Parse(`{"type":"error","message":"stream closed"}`)...)
	events = append(events, parser.Parse(`{"type":"item.completed","item":{"id":"i","type":"command_execution","aggregated_output":"x","exit_code":2}}`)...)
	events = append(events, parser.Parse(`{"type":"item.completed","item":{"id":"j","type":"file_change","status":"failed"}}`)...)
	events = append(events, parser.Parse(`{"type":"item.completed","item":{"type":"reasoning","text":"hmm"}}`)...)
	if len(kinds(events, KindError)) != 2 || len(kinds(events, KindThinking)) != 1 {
		t.Fatalf("%+v", events)
	}
	for _, result := range kinds(events, KindToolResult) {
		if !result.IsError {
			t.Fatalf("非零退出码与失败的改动都应标错：%+v", result)
		}
	}
}

func TestOpencodeErrorAndReasoning(t *testing.T) {
	parser, _ := NewParser("opencode")
	events := parser.Parse(`{"type":"error","sessionID":"ses_1","error":{"data":{"message":"quota"}}}`)
	events = append(events, parser.Parse(`{"type":"reasoning","sessionID":"ses_1","part":{"text":"think"}}`)...)
	events = append(events, parser.Parse(`{"type":"tool_use","sessionID":"ses_1","part":{"id":"c","tool":"bash","state":{"status":"error","error":"denied"}}}`)...)
	if len(kinds(events, KindSession)) != 1 || kinds(events, KindError)[0].Text != "quota" || len(kinds(events, KindThinking)) != 1 {
		t.Fatalf("%+v", events)
	}
	if results := kinds(events, KindToolResult); len(results) != 1 || !results[0].IsError {
		t.Fatalf("%+v", results)
	}
}

func TestClipKeepsUTF8Boundaries(t *testing.T) {
	text := strings.Repeat("镜", maxToolOutput)
	clipped := clip(text)
	if !strings.HasSuffix(clipped, "（已截断）") || !strings.HasPrefix(clipped, "镜") {
		t.Fatalf("clipped=%q", clipped[len(clipped)-40:])
	}
	if strings.ContainsRune(clipped, '�') {
		t.Fatal("截断切坏了 UTF-8")
	}
	if clip("short") != "short" {
		t.Fatal("短文本不应改动")
	}
}
