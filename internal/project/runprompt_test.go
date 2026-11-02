package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPromptReadsDefaultAndOverride(t *testing.T) {
	root := t.TempDir()
	if _, _, err := RunPrompt(root, ""); err == nil || !strings.Contains(err.Error(), "PROMPT.md") {
		t.Fatalf("缺 PROMPT.md 应报出路径：%v", err)
	}
	os.WriteFile(filepath.Join(root, "PROMPT.md"), []byte("default"), 0o644)
	prompt, path, err := RunPrompt(root, "")
	if err != nil || string(prompt) != "default" || path != filepath.Join(root, "PROMPT.md") {
		t.Fatalf("prompt=%q path=%s err=%v", prompt, path, err)
	}
	custom := filepath.Join(root, "custom.md")
	os.WriteFile(custom, []byte("custom"), 0o644)
	prompt, path, err = RunPrompt(root, custom)
	if err != nil || string(prompt) != "custom" || path != custom {
		t.Fatalf("prompt=%q path=%s err=%v", prompt, path, err)
	}
}
