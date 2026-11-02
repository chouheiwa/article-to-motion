package studio

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestStylesListAndExampleImages(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodGet, f.url+"/api/styles", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if len(studioList(t, body["canvases"])) != len(preset.All()) || len(studioList(t, body["styles"])) != len(preset.AllStyles()) {
		t.Fatalf("styles=%#v", body)
	}
	if body["custom"].(map[string]any)["id"] != preset.CustomStyle.ID || body["default_canvas"] != preset.Default().ID {
		t.Fatalf("custom/default=%#v", body)
	}

	style, canvas := preset.DefaultStyle().ID, preset.Default().ID
	status, raw := rawStudioGet(t, f.url+"/api/styles/"+style+"/"+canvas+"/"+preset.ArchetypeIDs[0]+".png")
	if status != http.StatusOK || !strings.HasPrefix(raw, "\x89PNG") {
		t.Fatalf("example=%d", status)
	}
	for _, path := range []string{
		style + "/" + canvas + "/unknown.png",
		"no-such-style/" + canvas + "/proposition.png",
		style + "/no-such-canvas/proposition.png",
		preset.CustomStyle.ID + "/" + canvas + "/proposition.png",
		style + "/" + canvas + "/proposition.svg",
		style + "/" + canvas,
	} {
		if status, _ := rawStudioGet(t, f.url+"/api/styles/"+path); status != http.StatusNotFound {
			t.Fatalf("%s → %d", path, status)
		}
	}
	if status, _ := studioRequest(t, http.MethodPost, f.url+"/api/styles", "{}"); status != http.StatusMethodNotAllowed {
		t.Fatalf("POST styles → %d", status)
	}
}

func TestTreeSkipsStateDependenciesAndSecrets(t *testing.T) {
	f := newSessionFixture(t)
	writeTree(t, f.root, map[string]string{
		"node_modules/x/index.js":        "skip",
		"production/studio/tasks.json":   "skip",
		"production/review/sheet-01.png": "png",
	})
	status, body := studioRequest(t, http.MethodGet, f.url+"/api/tree", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	paths := map[string]bool{}
	for _, entry := range studioList(t, body["files"]) {
		paths[entry.(map[string]any)["path"].(string)] = true
	}
	for _, want := range []string{"PROMPT.md", "scenes/scene-001/prompt.md", "production/review/sheet-01.png"} {
		if !paths[want] {
			t.Fatalf("缺少 %s：%v", want, paths)
		}
	}
	for _, skipped := range []string{".env", "scenes/scene-001/.env", "node_modules/x/index.js", "production/studio/tasks.json", ".agents/skills/scene-kit/SKILL.md"} {
		if paths[skipped] {
			t.Fatalf("%s 不应列出", skipped)
		}
	}
	if body["truncated"] != false {
		t.Fatalf("truncated=%v", body["truncated"])
	}
}

func TestRawServesProjectFilesButNotSecrets(t *testing.T) {
	f := newSessionFixture(t)
	status, raw := rawStudioGet(t, f.url+"/api/raw?path=scenes/scene-001/prompt.md")
	if status != http.StatusOK || raw != "original" {
		t.Fatalf("raw=%d %q", status, raw)
	}
	for _, path := range []string{".env", "scenes/scene-001/.env", "../etc/passwd", "/etc/passwd", "scenes", "missing.mp4", ""} {
		if status, raw := rawStudioGet(t, f.url+"/api/raw?path="+path); status != http.StatusNotFound {
			t.Fatalf("%q → %d %q", path, status, raw)
		}
	}
	if status, _ := studioRequest(t, http.MethodPost, f.url+"/api/raw?path=PROMPT.md", "{}"); status != http.StatusMethodNotAllowed {
		t.Fatalf("POST raw → %d", status)
	}
}

func TestBeatStateFollowsProjectFiles(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodGet, f.url+"/api/beat", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["enabled"] != false || body["click"] != false {
		t.Fatalf("beat=%#v", body)
	}
	writeTree(t, f.root, map[string]string{"beat.yaml": "x", "production/beat/grid-click.wav": "wav", "production/beat/grid-draft.json": "{}"})
	_, body = studioRequest(t, http.MethodGet, f.url+"/api/beat", "")
	if body["enabled"] != true || body["click"] != true || body["draft"] != true || body["confirmed"] != false || body["click_path"] != "production/beat/grid-click.wav" {
		t.Fatalf("beat=%#v", body)
	}
}

func TestStaticServesBuiltAssetsWithMIME(t *testing.T) {
	f := newSessionFixture(t)
	matches, err := filepath.Glob("web/assets/*.js")
	if err != nil || len(matches) == 0 {
		t.Fatal("web/assets 下没有构建产物，先在 internal/studio/ui 跑 npm run build")
	}
	res, err := http.Get(f.url + "/assets/" + filepath.Base(matches[0]))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("app.js = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}
