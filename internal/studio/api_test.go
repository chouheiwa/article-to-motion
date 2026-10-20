package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startStudio(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func studioRequest(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("%s %s: response is not JSON: %q: %v", method, url, raw, err)
		}
	}
	return res.StatusCode, value
}

func requireStudioStatus(t *testing.T, status, want int, body map[string]any) {
	t.Helper()
	if status != want {
		t.Fatalf("status = %d, want %d (%#v)", status, want, body)
	}
}

func studioList(t *testing.T, value any) []any {
	t.Helper()
	switch list := value.(type) {
	case nil:
		return nil
	case []any:
		return list
	default:
		t.Fatalf("expected a list, got %#v", value)
		return nil
	}
}

func waitStudioTask(t *testing.T, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, body := studioRequest(t, http.MethodGet, ts.URL+"/api/tasks/"+id, "")
		if status == http.StatusOK {
			switch body["status"] {
			case "succeeded", "failed", "cancelled":
				return body
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return nil
}

func writeStudioPNG(t *testing.T, path string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStudioFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rawStudioGet(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

func TestStudioDirectoryAndProjectLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "projects.json")
	_, ts := startStudio(t, Config{ConfigPath: configPath})

	status, body := studioRequest(t, http.MethodGet, ts.URL+"/api/directories", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["path"] != home || body["parent"] != "" {
		t.Fatalf("browse root = %#v", body)
	}
	if entries := studioList(t, body["entries"]); len(entries) != 0 {
		t.Fatalf("empty home entries = %#v", entries)
	}

	alpha := filepath.Join(home, "alpha")
	beta := filepath.Join(home, "beta")
	writeStudioFile(t, filepath.Join(alpha, "frame.md"), "---\nstyle_id: editorial\n---\n")
	if err := os.MkdirAll(filepath.Join(beta, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStudioFile(t, filepath.Join(home, "note.txt"), "not a directory")

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/directories", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	entries := studioList(t, body["entries"])
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	first := entries[0].(map[string]any)
	second := entries[1].(map[string]any)
	if first["name"] != "alpha" || first["is_project"] != true {
		t.Fatalf("first entry = %#v", first)
	}
	if second["name"] != "beta" || second["is_project"] != false {
		t.Fatalf("second entry = %#v", second)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/directories?path="+url.QueryEscape(alpha), "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["path"] != alpha || body["parent"] != home || body["root"] != home {
		t.Fatalf("subdirectory browse = %#v", body)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/directories?path="+url.QueryEscape(other), "")
	requireStudioStatus(t, status, http.StatusForbidden, body)
	if body["code"] != "directory_outside_root" {
		t.Fatalf("outside root = %#v", body)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/directories?path="+url.QueryEscape(filepath.Join(home, "missing")), "")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/directories?path="+url.QueryEscape(filepath.Join(home, "note.txt")), "")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/directories", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["current_project_id"] != "" || len(studioList(t, body["projects"])) != 0 {
		t.Fatalf("initial projects = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/workspaces/current", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["project"] != nil {
		t.Fatalf("initial current project = %#v", body)
	}

	for _, path := range []string{"/api/state", "/api/scenes", "/api/script", "/api/assets", "/api/publish", "/api/review", "/api/scenes/scene-001"} {
		status, body = studioRequest(t, http.MethodGet, ts.URL+path, "")
		requireStudioStatus(t, status, http.StatusNotFound, body)
	}
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", "{}")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/render/all", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/review/run", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/tasks", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/operations", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/operations/preview", "{}")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/operations/execute", "{}")
	requireStudioStatus(t, status, http.StatusBadRequest, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/open", "{")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/open", `{"path":""}`)
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects/open", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/recent/remove", "{")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/recent/remove", "{}")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects/recent/remove", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/workspaces/switch", "{")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/workspaces/switch", `{"project_id":"missing"}`)
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/workspaces/switch", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects/create", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)
	for _, payload := range []string{
		`{}`,
		fmt.Sprintf(`{"parent":%q}`, home),
		fmt.Sprintf(`{"parent":%q,"name":"a/b"}`, home),
		fmt.Sprintf(`{"parent":%q,"name":"x"}`, other),
		fmt.Sprintf(`{"parent":%q,"name":"x"}`, filepath.Join(home, "missing")),
		fmt.Sprintf(`{"parent":%q,"name":"bad-canvas","canvas":"missing"}`, home),
		fmt.Sprintf(`{"parent":%q,"name":"bad-style","style":"missing"}`, home),
	} {
		status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/create", payload)
		if status != http.StatusBadRequest && status != http.StatusForbidden {
			t.Fatalf("create %s: status = %d (%#v)", payload, status, body)
		}
	}

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/create", fmt.Sprintf(`{"parent":%q,"name":"my-project"}`, home))
	requireStudioStatus(t, status, http.StatusCreated, body)
	project := body["project"].(map[string]any)
	projectID := project["id"].(string)
	target := project["path"].(string)
	if project["name"] != "my-project" {
		t.Fatalf("created project = %#v", project)
	}
	for _, name := range []string{"frame.md", "docs/style-brief.md"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("created project missing %s: %v", name, err)
		}
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/workspaces/current", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["project"].(map[string]any)["id"] != projectID {
		t.Fatalf("current project = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["current_project_id"] != projectID || len(studioList(t, body["projects"])) != 1 {
		t.Fatalf("projects = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/state", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["stage"] != "setup" {
		t.Fatalf("state = %#v", body)
	}
	metadata := body["metadata"].(map[string]any)
	if metadata["style_id"] != "custom-draft" || metadata["style_name"] == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/script", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["path"] != "" || body["content"] != "" {
		t.Fatalf("empty script = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/assets", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/publish", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["available"] != false {
		t.Fatalf("publish = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/review", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["available"] != false {
		t.Fatalf("review = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if len(studioList(t, body["scenes"])) != 0 {
		t.Fatalf("scenes = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/missing", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/create", fmt.Sprintf(`{"parent":%q,"name":"my-project"}`, home))
	requireStudioStatus(t, status, http.StatusConflict, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/open", fmt.Sprintf(`{"path":%q}`, target))
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/workspaces/switch", fmt.Sprintf(`{"project_id":%q}`, projectID))
	requireStudioStatus(t, status, http.StatusOK, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/projects/recent/remove", fmt.Sprintf(`{"project_id":%q}`, projectID))
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/workspaces/current", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["project"] != nil {
		t.Fatalf("current project after removal = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/projects", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if len(studioList(t, body["projects"])) != 0 {
		t.Fatalf("projects after removal = %#v", body)
	}
}

func TestStudioContentAndTaskEndpoints(t *testing.T) {
	root := t.TempDir()
	writeStudioFile(t, filepath.Join(root, "frame.md"), "---\nstyle_id: editorial\nstyle_name: \"编辑\"\n---\n")
	sceneOne := filepath.Join(root, "scenes", "scene-001")
	writeStudioFile(t, filepath.Join(sceneOne, "scene.json"), `{"id":"scene-001","duration_seconds":2,"output":"out.mp4","text":"before"}`)
	writeStudioFile(t, filepath.Join(sceneOne, "out.mp4"), "video-bytes")
	writeStudioFile(t, filepath.Join(sceneOne, ".studio-frames", "frame-01.jpg"), "jpg-bytes")
	sceneTwo := filepath.Join(root, "scenes", "scene-002")
	writeStudioFile(t, filepath.Join(sceneTwo, "scene.json"), `{"id":"scene-002","duration_seconds":1,"output":"missing.mp4"}`)
	writeStudioFile(t, filepath.Join(root, "SCRIPT.md"), "script body")
	// macOS 的默认文件系统大小写不敏感，不能再写一份小写 script.md：
	// 两者会落到同一个文件并互相覆盖。render 阶段改由 transcription.srt 触发。
	writeStudioFile(t, filepath.Join(root, "transcription.srt"), "1\n00:00:00,000 --> 00:00:01,000\nline\n")
	writeStudioFile(t, filepath.Join(root, "assets", "logo.txt"), "logo")
	writeStudioFile(t, filepath.Join(root, "cast", "char.txt"), "cast")
	writeStudioFile(t, filepath.Join(root, "publish.md"), "# publish")
	writeStudioPNG(t, filepath.Join(root, "covers", "cover.png"))
	writeStudioFile(t, filepath.Join(root, "covers", "broken.png"), "not an image")
	writeStudioFile(t, filepath.Join(root, "production", "review", "review.json"), `{"issues":[{"id":"i1"}]}`)

	s, ts := startStudio(t, Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})

	status, body := studioRequest(t, http.MethodGet, ts.URL+"/api/scenes", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	scenes := studioList(t, body["scenes"])
	if len(scenes) != 2 {
		t.Fatalf("scenes = %#v", scenes)
	}
	first := scenes[0].(map[string]any)
	if first["id"] != "scene-001" || first["exists"] != true || first["stale"] != false {
		t.Fatalf("scene-001 = %#v", first)
	}
	second := scenes[1].(map[string]any)
	if second["id"] != "scene-002" || second["exists"] != false {
		t.Fatalf("scene-002 = %#v", second)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/scene-001", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	data := body["data"].(map[string]any)
	if data["id"] != "scene-001" || body["exists"] != true {
		t.Fatalf("scene detail = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/missing", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)

	status, raw := rawStudioGet(t, ts.URL+"/api/scenes/scene-001/asset")
	if status != http.StatusOK || raw != "video-bytes" {
		t.Fatalf("asset = %d %q", status, raw)
	}
	status, raw = rawStudioGet(t, ts.URL+"/api/scenes/scene-001/frames/frame-01.jpg")
	if status != http.StatusOK || raw != "jpg-bytes" {
		t.Fatalf("frame = %d %q", status, raw)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/scene-001/frames", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if frames := body["frames"].([]any); len(frames) != 1 || frames[0] != ".studio-frames/frame-01.jpg" {
		t.Fatalf("frames = %#v", body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/scenes/scene-001/frames/../evil.jpg", nil)
	rec := httptest.NewRecorder()
	s.sceneFeatureAPI(rec, req)
	requireStudioStatus(t, rec.Code, http.StatusBadRequest, nil)

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/scene-001/bogus", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes/missing/asset", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/scenes/scene-002/frames", "")
	requireStudioStatus(t, status, http.StatusAccepted, body)
	frameTask := waitStudioTask(t, ts, body["task_id"].(string))
	if frameTask["status"] != "failed" || !strings.Contains(frameTask["error"].(string), "镜头产物不存在") {
		t.Fatalf("frame task = %#v", frameTask)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/script", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["path"] != "SCRIPT.md" || body["content"] != "script body" {
		t.Fatalf("script = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/assets", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	assetPaths := map[string]bool{}
	for _, item := range studioList(t, body["assets"]) {
		assetPaths[item.(map[string]any)["path"].(string)] = true
	}
	if !assetPaths["assets/logo.txt"] || !assetPaths["cast/char.txt"] {
		t.Fatalf("assets = %#v", assetPaths)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/publish", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["available"] != true || len(studioList(t, body["covers"])) != 2 {
		t.Fatalf("publish = %#v", body)
	}
	for _, item := range studioList(t, body["covers"]) {
		cover := item.(map[string]any)
		switch cover["path"] {
		case "covers/cover.png":
			if cover["width"] != float64(2) || cover["height"] != float64(3) || cover["sha256"] == "" {
				t.Fatalf("cover = %#v", cover)
			}
		case "covers/broken.png":
			if cover["error"] != "无法读取图片尺寸" {
				t.Fatalf("broken cover = %#v", cover)
			}
		default:
			t.Fatalf("unexpected cover = %#v", cover)
		}
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/review", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["available"] != true {
		t.Fatalf("review = %#v", body)
	}
	report := body["report"].(map[string]any)
	if len(studioList(t, report["issues"])) != 1 {
		t.Fatalf("review report = %#v", report)
	}
	writeStudioFile(t, filepath.Join(root, "production", "review", "review.json"), "not json")
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/review", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["report"].(map[string]any)["raw"] != "not json" {
		t.Fatalf("invalid review = %#v", body)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/state", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["stage"] != "render" {
		t.Fatalf("stage = %#v", body)
	}
	stateMeta := body["metadata"].(map[string]any)
	if stateMeta["style_id"] != "editorial" || stateMeta["style_name"] != "编辑" {
		t.Fatalf("metadata = %#v", stateMeta)
	}
	stateScenes := body["scenes"].(map[string]any)
	if stateScenes["total"] != float64(2) || stateScenes["completed"] != float64(1) {
		t.Fatalf("state scenes = %#v", stateScenes)
	}
	media := body["media"].(map[string]any)
	if media["publish"] != true || media["final"] != false || media["audio"] != false {
		t.Fatalf("media = %#v", media)
	}
	if issues := studioList(t, body["issues"]); len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}

	writeStudioFile(t, filepath.Join(root, "production", "run-report.json"), "{}")
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/state", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["stage"] != "review" {
		t.Fatalf("stage after run report = %#v", body)
	}

	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(sceneOne, "scene.json"), future, future); err != nil {
		t.Fatal(err)
	}
	writeStudioFile(t, filepath.Join(root, "scenes", "scene-004", "scene.json"), `{"id":"scene-004","duration_seconds":1,"output":"/etc/passwd"}`)
	writeStudioFile(t, filepath.Join(root, "scenes", "scene-003", "scene.json"), "not json")
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/scenes", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if got := len(studioList(t, body["scenes"])); got != 3 {
		t.Fatalf("scenes with invalid/absolute outputs = %d %#v", got, body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/state", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["scenes"].(map[string]any)["stale"] != float64(1) {
		t.Fatalf("stale scenes = %#v", body["scenes"])
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/tasks", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if len(studioList(t, body["tasks"])) == 0 {
		t.Fatalf("tasks = %#v", body)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/tasks/"+frameTask["task_id"].(string), "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["status"] != "failed" {
		t.Fatalf("frame task detail = %#v", body)
	}
	status, raw = rawStudioGet(t, ts.URL+"/api/tasks/"+frameTask["task_id"].(string)+"/events")
	if status != http.StatusOK || !strings.Contains(raw, "event: task.queued") || !strings.Contains(raw, "task.failed") {
		t.Fatalf("task events = %d %q", status, raw)
	}
	status, raw = rawStudioGet(t, ts.URL+"/api/tasks/"+frameTask["task_id"].(string)+"/events?follow=1")
	if status != http.StatusOK || !strings.Contains(raw, "task.failed") {
		t.Fatalf("followed task events = %d %q", status, raw)
	}
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/tasks/"+frameTask["task_id"].(string)+"/cancel", "")
	requireStudioStatus(t, status, http.StatusConflict, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/tasks/", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/tasks/missing", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/tasks", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/operations", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/operations", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/review/run", "")
	requireStudioStatus(t, status, http.StatusAccepted, body)
	reviewTask := waitStudioTask(t, ts, body["task_id"].(string))
	if reviewTask["status"] != "failed" || !strings.Contains(reviewTask["error"].(string), "找不到可审查的成片") {
		t.Fatalf("review task = %#v", reviewTask)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/review/run", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/render/all", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)

	emptyRoot := t.TempDir()
	_, emptyTS := startStudio(t, Config{ProjectPath: emptyRoot, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	status, body = studioRequest(t, http.MethodPost, emptyTS.URL+"/api/render/all", "")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	if body["code"] != "no_scenes" {
		t.Fatalf("empty render = %#v", body)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/missing-endpoint", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)
	if body["code"] != "not_found" {
		t.Fatalf("missing API = %#v", body)
	}
	status, raw = rawStudioGet(t, ts.URL+"/missing-asset.js")
	if status != http.StatusOK || !strings.Contains(raw, "ArticleToMotion") {
		t.Fatalf("static fallback = %d %q", status, raw)
	}
}

func TestStudioChatEndpoints(t *testing.T) {
	root := t.TempDir()
	writeStudioFile(t, filepath.Join(root, "scenes", "scene-001", "scene.json"), `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","text":"old"}`)
	s, ts := startStudio(t, Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})

	status, body := studioRequest(t, http.MethodGet, ts.URL+"/api/chat", "")
	requireStudioStatus(t, status, http.StatusMethodNotAllowed, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", "{")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", "{}")
	requireStudioStatus(t, status, http.StatusBadRequest, body)
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", `{"message":"hi","conversation_id":"missing"}`)
	requireStudioStatus(t, status, http.StatusNotFound, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", `{"message":"password: hunter2"}`)
	requireStudioStatus(t, status, http.StatusOK, body)
	conversation := body["conversation"].(map[string]any)
	conversationID := conversation["conversation_id"].(string)
	messages := studioList(t, conversation["messages"])
	if !strings.Contains(messages[0].(map[string]any)["content"].(string), "password=[REDACTED]") {
		t.Fatalf("redacted conversation = %#v", conversation)
	}
	if !strings.Contains(body["message"].(string), "我已收到") {
		t.Fatalf("generic reply = %#v", body)
	}

	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/chat/"+conversationID, "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["conversation_id"] != conversationID {
		t.Fatalf("conversation = %#v", body)
	}
	status, raw := rawStudioGet(t, ts.URL+"/api/chat/"+conversationID+"?stream=1")
	if status != http.StatusOK || !strings.Contains(raw, "event: chat.message") {
		t.Fatalf("chat stream = %d %q", status, raw)
	}
	status, raw = rawStudioGet(t, ts.URL+"/api/chat/"+conversationID+"?stream=1&follow=1")
	if status != http.StatusOK || !strings.Contains(raw, "event: chat.message") {
		t.Fatalf("chat follow = %d %q", status, raw)
	}
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/chat/missing", "")
	requireStudioStatus(t, status, http.StatusNotFound, body)

	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", `{"message":"字幕：新字幕","scene_id":"scene-001"}`)
	requireStudioStatus(t, status, http.StatusOK, body)
	preview, ok := body["operation_preview"].(map[string]any)
	if !ok || preview["operation"] == nil {
		t.Fatalf("caption preview = %#v", body)
	}

	installFakeCodex(t)
	t.Setenv("AM_STUDIO_AI_TOOL", "codex")
	t.Setenv("ORCHESTRATOR", "codex")
	t.Setenv("RENDERER", "claude")
	status, body = studioRequest(t, http.MethodPost, ts.URL+"/api/chat", `{"message":"请分析这个镜头","scene_id":"scene-001"}`)
	requireStudioStatus(t, status, http.StatusOK, body)
	if !strings.Contains(body["message"].(string), "AI 已收到请求") {
		t.Fatalf("ai reply = %#v", body)
	}
	aiConversation := body["conversation"].(map[string]any)["conversation_id"].(string)
	waitForAIChat(t, ts)
	status, body = studioRequest(t, http.MethodGet, ts.URL+"/api/chat/"+aiConversation, "")
	requireStudioStatus(t, status, http.StatusOK, body)
	foundReply := false
	for _, item := range studioList(t, body["messages"]) {
		if item.(map[string]any)["content"] == "fake reply" {
			foundReply = true
		}
	}
	if !foundReply {
		t.Fatalf("ai conversation = %#v", body)
	}
	s.features.mu.RLock()
	proposed := 0
	for _, op := range s.features.operations {
		if op.Type == "scene.capture_frames" {
			proposed++
		}
	}
	s.features.mu.RUnlock()
	if proposed == 0 {
		t.Fatal("AI operation was not registered")
	}

	if err := s.runAIChat(context.Background(), aiConversation, "unknown-tool", "scene-001", "hi", nil, nil); err == nil {
		t.Fatal("unknown tool should fail")
	}
	t.Setenv("ORCHESTRATOR", "")
	t.Setenv("RENDERER", "")
	if err := s.runAIChat(context.Background(), aiConversation, "codex", "scene-001", "hi", nil, nil); err == nil {
		t.Fatal("invalid project config should fail")
	}
}

func installFakeCodex(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"[[USER_MESSAGE]]fake reply"}}'
printf '%s\n' '[[AM_OPERATION]] {"type":"scene.capture_frames","scene_id":"scene-001"}'
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func waitForAIChat(t *testing.T, ts *httptest.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, body := studioRequest(t, http.MethodGet, ts.URL+"/api/tasks", "")
		if status == http.StatusOK {
			for _, item := range studioList(t, body["tasks"]) {
				task := item.(map[string]any)
				if task["type"] == "ai.chat" && task["status"] == "succeeded" {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("AI chat task did not finish")
}

func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		"api_key=abc":     "api_key=[REDACTED]",
		"TOKEN: xyz":      "TOKEN=[REDACTED]",
		"password = pass": "password=[REDACTED]",
		"nothing secret":  "nothing secret",
	}
	for input, want := range cases {
		if got := redactSecrets(input); got != want {
			t.Fatalf("redactSecrets(%q) = %q, want %q", input, got, want)
		}
	}
}
