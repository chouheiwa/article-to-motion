package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPromptCarriesConfirmedStudioChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(filepath.Join(root, "production", "studio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"voice.srt","text":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "production", "studio", "op.json"), []byte(`{"scene_id":"scene-001","type":"camera","changes":{"zoom":1.2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "voice.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Studio 已确认的修改") || !strings.Contains(prompt, `"zoom":1.2`) {
		t.Fatalf("prompt missing studio change: %s", prompt)
	}
}
