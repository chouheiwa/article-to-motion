package agentrun

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealCLIResume 对真实 CLI 跑两轮：第一轮记住一个词，第二轮续接后问它。
//
// 每次都是真实的模型调用，所以只在设置 AM_TEST_AGENT_TOOLS 时运行，例如
//
//	AM_TEST_AGENT_TOOLS=claude,codex,qoder,codebuddy,opencode go test ./internal/agentrun -run RealCLI -v
//
// 改了某个工具的调用参数或解析器之后，要在本机对它跑一遍。
func TestRealCLIResume(t *testing.T) {
	list := os.Getenv("AM_TEST_AGENT_TOOLS")
	if list == "" {
		t.Skip("设置 AM_TEST_AGENT_TOOLS 才运行真实 CLI 集成测试")
	}
	for _, tool := range strings.Split(list, ",") {
		tool = strings.TrimSpace(tool)
		t.Run(tool, func(t *testing.T) {
			workdir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			run := func(prompt, session string, resume bool) (Result, string) {
				var text strings.Builder
				result, err := Run(ctx, Spec{Tool: tool, Workdir: workdir, Prompt: prompt, SessionID: session, Resume: resume, Env: os.Environ()}, func(event Event) {
					if event.Kind == KindText {
						text.WriteString(event.Text)
					}
					if event.Kind == KindError {
						t.Logf("%s 错误事件：%s", tool, event.Text)
					}
				})
				if err != nil {
					t.Fatalf("%s：%v（stderr：%s）", tool, err, result.Stderr)
				}
				return result, text.String()
			}
			first, _ := run("记住单词 PELICAN，只回复 OK。", NewSessionID(tool), false)
			if first.SessionID == "" {
				t.Fatal("没有拿到会话 id")
			}
			second, answer := run("我让你记住的单词是什么？只回复单词。", first.SessionID, true)
			if second.SessionID != first.SessionID {
				t.Fatalf("续接后会话 id 变了：%s → %s", first.SessionID, second.SessionID)
			}
			if !strings.Contains(strings.ToUpper(answer), "PELICAN") {
				t.Fatalf("续接没有带上上下文，回答：%q", answer)
			}
		})
	}
}
