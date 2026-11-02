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
	if state["stage"] != "setup" {
		t.Fatalf("stage = %#v", state["stage"])
	}
	meta := state["metadata"].(map[string]any)
	if meta["canvas"] != "vertical-9x16" {
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
