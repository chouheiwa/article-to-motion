package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestOperationPreviewExecuteAndUndo(t *testing.T) {
	root := t.TempDir()
	sceneDir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sceneDir, "scene.json")
	if err := os.WriteFile(path, []byte(`{"id":"scene-001","duration_seconds":2,"output":"out.mp4","transcript":"voice.srt","text":"before"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post := func(url, body string) map[string]any {
		res, e := http.Post(ts.URL+url, "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var v map[string]any
		if e = json.NewDecoder(res.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode >= 300 {
			t.Fatalf("%s: %d %#v", url, res.StatusCode, v)
		}
		return v
	}
	preview := post("/api/operations/preview", `{"type":"scene.update_caption","scene_id":"scene-001","reason":"修正字幕","changes":{"text":"after"}}`)
	op := preview["operation"].(map[string]any)["operation_id"].(string)
	executed := post("/api/operations/execute", fmt.Sprintf(`{"operation_id":%q}`, op))
	taskID := executed["task_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res, e := http.Get(ts.URL + "/api/tasks/" + taskID)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		_ = json.NewDecoder(res.Body).Decode(&v)
		res.Body.Close()
		if v["status"] == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "after") {
		t.Fatalf("updated scene = %s", body)
	}
	post("/api/operations/"+op+"/undo", "{}")
	body, _ = os.ReadFile(path)
	if !strings.Contains(string(body), "before") {
		t.Fatalf("restored scene = %s", body)
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

func TestChatCanCreateConfirmedCaptionPreview(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"voice.srt","text":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	res, err := http.Post(ts.URL+"/api/chat", "application/json", strings.NewReader(`{"message":"字幕：新的显示文字","scene_id":"scene-001"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	preview, ok := body["operation_preview"].(map[string]any)
	if !ok || preview["operation"] == nil {
		t.Fatalf("preview = %#v", body["operation_preview"])
	}
}

func TestOperationAndSnapshotSurviveServerRestart(t *testing.T) {
	root := t.TempDir()
	sceneDir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sceneDir, "scene.json")
	if err := os.WriteFile(path, []byte(`{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"voice.srt","text":"before"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "projects.json")
	s, err := New(Config{ProjectPath: root, ConfigPath: config})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	post := func(url, body string) map[string]any {
		res, e := http.Post(ts.URL+url, "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var v map[string]any
		_ = json.NewDecoder(res.Body).Decode(&v)
		return v
	}
	p := post("/api/operations/preview", `{"type":"scene.update_caption","scene_id":"scene-001","changes":{"text":"after"}}`)
	opID := p["operation"].(map[string]any)["operation_id"].(string)
	post("/api/operations/execute", fmt.Sprintf(`{"operation_id":%q}`, opID))
	time.Sleep(50 * time.Millisecond)
	s2, err := New(Config{ProjectPath: root, ConfigPath: config})
	if err != nil {
		t.Fatal(err)
	}
	s2.features.mu.RLock()
	_, opLoaded := s2.features.operations[opID]
	_, snapLoaded := s2.features.snapshots[s2.features.operations[opID].SnapshotID]
	s2.features.mu.RUnlock()
	if !opLoaded || !snapLoaded {
		t.Fatalf("operation/snapshot not restored: %v %v", opLoaded, snapLoaded)
	}
}

func TestStudioEndToEndFlow(t *testing.T) {
	root := t.TempDir()
	sceneDir := filepath.Join(root, "scenes", "scene-001")
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scenePath := filepath.Join(sceneDir, "scene.json")
	if err := os.WriteFile(scenePath, []byte(`{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"voice.srt","text":"before"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	get := func(path string) map[string]any {
		res, e := http.Get(ts.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var v map[string]any
		if e = json.NewDecoder(res.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode >= 300 {
			t.Fatalf("GET %s: %d %#v", path, res.StatusCode, v)
		}
		return v
	}
	post := func(path, body string) map[string]any {
		res, e := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var v map[string]any
		if e = json.NewDecoder(res.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode >= 300 {
			t.Fatalf("POST %s: %d %#v", path, res.StatusCode, v)
		}
		return v
	}
	page, pageErr := http.Get(ts.URL + "/")
	if pageErr != nil {
		t.Fatal(pageErr)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(pageBody), "ArticleToMotion") {
		t.Fatal("embedded page was not served")
	}
	if len(get("/api/scenes")["scenes"].([]any)) != 1 {
		t.Fatal("scene list missing")
	}
	chat := post("/api/chat", `{"message":"字幕：after","scene_id":"scene-001"}`)
	preview := chat["operation_preview"].(map[string]any)["operation"].(map[string]any)
	executed := post("/api/operations/execute", fmt.Sprintf(`{"operation_id":%q}`, preview["operation_id"]))
	taskID := executed["task_id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task := get("/api/tasks/" + taskID)
		if task["status"] == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	body, _ := os.ReadFile(scenePath)
	if !strings.Contains(string(body), "after") {
		t.Fatalf("mutation did not persist: %s", body)
	}
	post("/api/operations/"+preview["operation_id"].(string)+"/undo", "{}")
	body, _ = os.ReadFile(scenePath)
	if !strings.Contains(string(body), "before") {
		t.Fatalf("undo did not restore: %s", body)
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
