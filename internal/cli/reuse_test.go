package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
)

func reuseScene(t *testing.T) scene.Scene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\n一句\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"一句"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "out.mp4"), []byte("video"), 0o644)
	s, err := scene.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReuseDecisionUsesFingerprint(t *testing.T) {
	s := reuseScene(t)
	cfg := config.Config{Renderer: "codex"}
	env := map[string]string{"HOME": t.TempDir()}
	resolved, err := scene.ResolveAllSkills("codex", s.Directory, env)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := scene.Fingerprint(s, "codex", resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := scene.WriteRecord(s, inputs); err != nil {
		t.Fatal(err)
	}
	// 只动修改时间不动内容：旧判定会误报 Stale，指纹判定不会。
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(s.Directory, "scene.json"), future, future)
	if status, reason := reuseDecision(s, cfg, env); status != schedule.Skipped || !strings.Contains(reason, "指纹一致") {
		t.Fatalf("内容未变应跳过，实际 %s：%s", status, reason)
	}
	// prompt.md 之外的输入变了也要能发现，并说出是哪一项。
	os.WriteFile(filepath.Join(s.Directory, "transcript.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\n改过\n"), 0o644)
	status, reason := reuseDecision(s, cfg, env)
	if status != schedule.Stale || !strings.Contains(reason, "file:transcript.srt") {
		t.Fatalf("字幕变了应标 Stale 并点名，实际 %s：%s", status, reason)
	}
}

func TestReuseDecisionFallsBackToModTimeWithoutRecord(t *testing.T) {
	s := reuseScene(t)
	cfg := config.Config{Renderer: "codex"}
	env := map[string]string{"HOME": t.TempDir()}
	past := time.Now().Add(-time.Hour)
	for _, name := range []string{"scene.json", "transcript.srt"} {
		os.Chtimes(filepath.Join(s.Directory, name), past, past)
	}
	if status, _ := reuseDecision(s, cfg, env); status != schedule.Skipped {
		t.Fatalf("没有记录、输入不比产物新时应跳过，实际 %s", status)
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(s.Directory, "scene.json"), future, future)
	if status, _ := reuseDecision(s, cfg, env); status != schedule.Stale {
		t.Fatalf("没有记录时退回修改时间判定，实际 %s", status)
	}
}
