package agentrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeCLI 写一个假的编排工具脚本，LookPath 无论要什么名字都返回它。
func fakeCLI(t *testing.T, body string) func(string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func(string) (string, error) { return path, nil }
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(events *[]Event) func(Event) {
	return func(event Event) { *events = append(*events, event) }
}

func TestRunStreamsCodexFixture(t *testing.T) {
	workdir := t.TempDir()
	// 把 stdin 落盘，确认 codex 的 prompt 走 stdin。
	script := `cat > "$PWD/stdin.txt"; cat ` + fixturePath(t, "codex-1")
	var events []Event
	var raw bytes.Buffer
	result, err := Run(context.Background(), Spec{Tool: "codex", Workdir: workdir, Prompt: "做一件事", LookPath: fakeCLI(t, script), Raw: &raw}, collect(&events))
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(workdir, "stdin.txt")); string(body) != "做一件事" {
		t.Fatalf("stdin=%q", body)
	}
	if result.SessionID != "01a19706-15cc-7c23-92b3-d0008785ee57" || !result.Produced || result.Usage.Empty() {
		t.Fatalf("result=%+v", result)
	}
	if events[len(events)-1].Kind != KindDone || events[len(events)-2].Kind != KindUsage {
		t.Fatalf("结尾应是 usage + done：%+v", events[len(events)-2:])
	}
	if strings.Count(raw.String(), "\n") != 7 {
		t.Fatalf("原始流应逐行保留：%q", raw.String())
	}
}

func TestRunEmitsPresetSessionFirst(t *testing.T) {
	var events []Event
	id := NewSessionID("claude")
	result, err := Run(context.Background(), Spec{Tool: "claude", Workdir: t.TempDir(), Prompt: "p", SessionID: id, LookPath: fakeCLI(t, "exit 0")}, collect(&events))
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Kind != KindSession || events[0].SessionID != id || result.SessionID != id {
		t.Fatalf("events=%+v result=%+v", events, result)
	}
	if result.Produced {
		t.Fatal("没有正文的运行不应算作有产出")
	}
}

func TestRunReportsExitCodeAndStderr(t *testing.T) {
	var events []Event
	_, err := Run(context.Background(), Spec{Tool: "claude", Workdir: t.TempDir(), Prompt: "p", SessionID: "gone", Resume: true, LookPath: fakeCLI(t, "echo 'No conversation found' >&2; exit 3")}, collect(&events))
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(exit.Error(), "No conversation found") {
		t.Fatalf("err=%v", err)
	}
	if events[len(events)-1].Kind != KindDone {
		t.Fatal("失败时也要发 done")
	}
}

func TestRunCancelKillsProcessGroup(t *testing.T) {
	workdir := t.TempDir()
	// 子进程再拉起孙进程：取消后孙进程也必须被杀掉，不能继续写文件。
	script := `(sleep 3; touch "$PWD/leaked") & echo '{"type":"thread.started","thread_id":"t"}'; wait`
	ctx, cancel := context.WithCancel(context.Background())
	var events []Event
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Spec{Tool: "codex", Workdir: workdir, Prompt: "p", LookPath: fakeCLI(t, script)}, collect(&events))
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后没有退出")
	}
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(workdir, "leaked")); err == nil {
		t.Fatal("孙进程没有随进程组终止")
	}
}

func TestRunRejectsBadSpecs(t *testing.T) {
	noop := func(Event) {}
	if _, err := Run(context.Background(), Spec{Tool: "bad"}, noop); err == nil {
		t.Fatal("未知工具")
	}
	if _, err := Run(context.Background(), Spec{Tool: "codex", Prompt: "p", SessionID: "x"}, noop); err == nil {
		t.Fatal("codex 不能预先指定会话")
	}
	missing := func(string) (string, error) { return "", errors.New("nope") }
	if _, err := Run(context.Background(), Spec{Tool: "codex", Prompt: "p", LookPath: missing}, noop); err == nil || !strings.Contains(err.Error(), "缺少必需工具") {
		t.Fatalf("err=%v", err)
	}
}

func TestNewSessionID(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for _, tool := range []string{"claude", "qoder", "codebuddy"} {
		if id := NewSessionID(tool); !uuid.MatchString(id) {
			t.Fatalf("%s id=%q", tool, id)
		}
	}
	if NewSessionID("codex") != "" || NewSessionID("opencode") != "" {
		t.Fatal("codex / opencode 的 id 只能从流里读")
	}
	if first, second := NewSessionID("claude"), NewSessionID("claude"); first == second {
		t.Fatal("id 应当随机")
	}
}

func TestTailBufferKeepsEnd(t *testing.T) {
	buffer := &tailBuffer{n: 5}
	_, _ = buffer.Write([]byte("abc"))
	_, _ = buffer.Write([]byte("defgh"))
	if buffer.String() != "defgh" {
		t.Fatalf("got %q", buffer.String())
	}
}
