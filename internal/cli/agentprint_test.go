package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/agentrun"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

func TestRunPrintsReadableEvents(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "agentrun", "testdata", "codex-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.WriteFile(filepath.Join(root, "PROMPT.md"), []byte("p"), 0o644)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\ncat >/dev/null\ncat '"+fixture+"'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCHESTRATOR", "codex")
	t.Setenv("RENDERER", "claude")
	t.Chdir(root)
	var out, errOut bytes.Buffer
	if code := Execute([]string{"run"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"会话: 01a19706-15cc-7c23-92b3-d0008785ee57", "→ shell /bin/zsh -lc", "\nOK\n", "用量: 输入 41579"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("缺少 %q：\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), `"type"`) {
		t.Fatalf("不应再透传原始 JSON：\n%s", out.String())
	}
}

func TestEventPrinterFormatsEveryKind(t *testing.T) {
	var out, errOut bytes.Buffer
	printer := &eventPrinter{stdout: &out, stderr: &errOut}
	cost := 0.5
	input, _ := json.Marshal(map[string]string{"file_path": "/work/a.txt"})
	printer.print(agentrun.Event{Kind: agentrun.KindToolCall, Tool: "Write", Input: input})
	printer.print(agentrun.Event{Kind: agentrun.KindToolCall, Tool: "Odd", Input: json.RawMessage(`[1]`)})
	printer.print(agentrun.Event{Kind: agentrun.KindToolResult, Text: "ok"})
	printer.print(agentrun.Event{Kind: agentrun.KindToolResult, IsError: true, Text: "denied\nmore"})
	printer.print(agentrun.Event{Kind: agentrun.KindThinking, Text: "secret plan"})
	printer.print(agentrun.Event{Kind: agentrun.KindError, Text: "boom"})
	printer.print(agentrun.Event{Kind: agentrun.KindUsage, Usage: &tools.Usage{InputTokens: 3, OutputTokens: 4, CostUSD: &cost}})
	got := out.String()
	for _, want := range []string{"→ Write /work/a.txt\n", "→ Odd \n", "  ✗ denied …\n", "$0.5000"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：%q", want, got)
		}
	}
	if strings.Contains(got, "ok") || strings.Contains(got, "secret plan") {
		t.Fatalf("成功的工具结果与思考不应打印：%q", got)
	}
	if errOut.String() != "错误: boom\n" {
		t.Fatalf("stderr=%q", errOut.String())
	}
	if long := truncate(strings.Repeat("字", summaryLimit+5)); !strings.HasSuffix(long, "…") || len([]rune(long)) != summaryLimit+1 {
		t.Fatalf("truncate=%q", long)
	}
}
