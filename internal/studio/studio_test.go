package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectAndStateAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "frame.md"), []byte("---\ncanvas: vertical-9x16\nstyle_id: editorial\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sceneDir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sceneDir, "scene.json"), []byte(`{"id":"scene-001","duration_seconds":2,"output":"out.mp4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", res.StatusCode)
	}
	var health map[string]any
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["ok"] != true {
		t.Fatalf("health = %#v", health)
	}
	res, err = http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var state map[string]any
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state["stage"] != "render" {
		t.Fatalf("stage = %#v", state["stage"])
	}
	meta := state["metadata"].(map[string]any)
	if meta["canvas_id"] != "vertical-9x16" || meta["style_id"] != "editorial" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestListenAndServeStopsWithContext(t *testing.T) {
	s, err := New(Config{ListenAddr: "127.0.0.1:0", ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ListenAndServe(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTasksPersistInProject(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	task := s.addTask("test", "persist me")
	s.updateTask(task.ID, "succeeded", "done", 1, nil)
	s2, err := New(Config{ProjectPath: root, ConfigPath: s.config.ConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	s2.features.mu.RLock()
	defer s2.features.mu.RUnlock()
	if got := s2.features.tasks[task.ID]; got == nil || got.Status != "succeeded" {
		t.Fatalf("persisted task = %#v", got)
	}
}

func TestTaskCancellationCancelsContext(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	task := s.addTask("test", "waiting")
	done := make(chan struct{})
	s.runTask(task, func(ctx context.Context) error { <-ctx.Done(); close(done); return ctx.Err() })
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Post(ts.URL+"/api/tasks/"+task.ID+"/cancel", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task context was not cancelled")
	}
	s.features.mu.RLock()
	status := s.features.tasks[task.ID].Status
	s.features.mu.RUnlock()
	if status != "cancelled" {
		t.Fatalf("status = %s", status)
	}
}

func TestStageFollowsProductionArtifacts(t *testing.T) {
	pending := []SceneSummary{{ID: "a", Exists: true}, {ID: "b"}}
	rendered := []SceneSummary{{ID: "a", Exists: true}, {ID: "b", Exists: true}}
	cases := []struct {
		name   string
		files  []string
		scenes []SceneSummary
		want   string
	}{
		{"空项目", nil, nil, "setup"},
		{"口播终稿", []string{"production/script-final.md"}, nil, "plan"},
		{"配音时间轴", []string{"production/audio/voice.srt"}, nil, "plan"},
		{"分镜计划", []string{"production/scene-plan.json"}, nil, "plan"},
		{"卡点网格", []string{"production/beat/grid.json"}, nil, "plan"},
		{"旧版脚本", []string{"script.md"}, nil, "plan"},
		{"镜头未出齐", nil, pending, "render"},
		{"镜头出齐", nil, rendered, "assemble"},
		{"有成片", []string{"final.mp4"}, rendered, "review"},
		{"成片在 production 下", []string{"production/final.mp4"}, rendered, "review"},
		{"成片加发布信息", []string{"final.mp4", "publish.md"}, rendered, "publish"},
		// 卡点项目没有脚本，但做完了就该显示发布阶段（choice-paralysis 实测）。
		{"卡点成片", []string{"beat.yaml", "production/beat/grid.json", "final.mp4", "publish.md"}, rendered[:1], "publish"},
	}
	for _, c := range cases {
		root := t.TempDir()
		for _, f := range c.files {
			writeStudioFile(t, filepath.Join(root, filepath.FromSlash(f)), "x")
		}
		if got := stageFor(root, c.scenes); got != c.want {
			t.Errorf("%s：stage=%s want %s", c.name, got, c.want)
		}
	}
}

func TestProjectMetadataReadsTopLevelFrontMatterOnly(t *testing.T) {
	root := t.TempDir()
	// 节选自真实项目：colors.canvas 是颜色值，按行扫描会把它当成画幅。
	writeStudioFile(t, filepath.Join(root, "frame.md"), "---\nschema_version: 1\nstyle_id: custom-cine-layered\nstyle_name: 分层电影\ncanvas:\n  width_px: 1920\n  height_px: 1080\n  fps: 30\n  orientation: landscape\ncolors:\n  canvas: \"#05060A\"\n---\n正文 canvas: nope\n")
	meta := projectMetadata(root)
	want := map[string]string{"style_id": "custom-cine-layered", "style_name": "分层电影", "canvas": "1920×1080", "fps": "30", "orientation": "landscape", "canvas_id": "landscape-16x9"}
	if len(meta) != len(want) {
		t.Fatalf("meta=%v", meta)
	}
	for k, v := range want {
		if meta[k] != v {
			t.Fatalf("%s=%q want %q（%v）", k, meta[k], v, meta)
		}
	}

	// 不在预设表里的画幅照样显示尺寸，只是没有 canvas_id。
	writeStudioFile(t, filepath.Join(root, "frame.md"), "---\nstyle_id: x\ncanvas: {width_px: 1000, height_px: 1000, fps: 24, orientation: square}\n---\n")
	if meta := projectMetadata(root); meta["canvas"] != "1000×1000" || meta["canvas_id"] != "" {
		t.Fatalf("custom canvas meta=%v", meta)
	}

	for _, broken := range []string{"no front matter", "---\nstyle_id: x\n", "---\n: [bad\n---\n"} {
		writeStudioFile(t, filepath.Join(root, "frame.md"), broken)
		if meta := projectMetadata(root); len(meta) != 0 {
			t.Fatalf("%q → %v", broken, meta)
		}
	}
	writeStudioFile(t, filepath.Join(root, "frame.md"), "---\r\nstyle_id: crlf\r\n---\r\n")
	if meta := projectMetadata(root); meta["style_id"] != "crlf" {
		t.Fatalf("CRLF meta=%v", meta)
	}
}
