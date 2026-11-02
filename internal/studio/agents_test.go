package studio

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

func TestAgentOrderCoversValidTools(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range agentOrder {
		if !tools.ValidTools[a.ID] || seen[a.ID] {
			t.Fatalf("agentOrder 里的 %s 无效或重复", a.ID)
		}
		seen[a.ID] = true
	}
	if len(seen) != len(tools.ValidTools) {
		t.Fatalf("agentOrder 漏了工具：%v vs %v", seen, tools.ValidTools)
	}
}

func TestAgentsAPIReportsInstalledToolsAndDefaults(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodGet, f.url+"/api/agents", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	agents := studioList(t, body["agents"])
	if len(agents) != len(agentOrder) {
		t.Fatalf("agents=%#v", agents)
	}
	claude := agents[0].(map[string]any)
	if claude["id"] != "claude" || claude["command"] != "claude" || claude["installed"] != true || !strings.HasSuffix(claude["path"].(string), "/claude") {
		t.Fatalf("claude=%#v", claude)
	}
	if body["orchestrator"] != "claude" || body["renderer"] != "claude" {
		t.Fatalf("defaults=%#v", body)
	}
	for _, a := range agents {
		if a.(map[string]any)["command"] == "" {
			t.Fatalf("命令名为空：%#v", a)
		}
	}
	if status, _ := studioRequest(t, http.MethodPost, f.url+"/api/agents", "{}"); status != http.StatusMethodNotAllowed {
		t.Fatalf("POST agents → %d", status)
	}
}

func TestSessionAgentChoiceReachesChildAndCanSwitch(t *testing.T) {
	f := newSessionFixture(t)
	for _, payload := range []string{`{"tool":"nope"}`, `{"renderer":"nope"}`} {
		if status, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", payload); status != http.StatusBadRequest {
			t.Fatalf("%s → %d %#v", payload, status, body)
		}
	}
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", `{"renderer":"codex"}`)
	requireStudioStatus(t, status, http.StatusCreated, body)
	id := body["session_id"].(string)
	if body["tool"] != "claude" || body["renderer"] != "codex" {
		t.Fatalf("没选的一项应沿用项目配置：%#v", body)
	}

	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"改一下"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	f.waitIdle(t, id)
	renderer, err := os.ReadFile(f.argsLog + ".renderer")
	if err != nil || strings.TrimSpace(string(renderer)) != "codex" {
		t.Fatalf("编排工具的子进程应拿到会话选的渲染工具：%q %v", renderer, err)
	}

	if status, _ := studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/agents", `{"tool":"nope"}`); status != http.StatusBadRequest {
		t.Fatalf("无效工具 → %d", status)
	}
	if status, _ := studioRequest(t, http.MethodGet, f.url+"/api/sessions/"+id+"/agents", ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("GET agents → %d", status)
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/agents", `{"tool":"codex","renderer":"claude"}`)
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["tool"] != "codex" || body["renderer"] != "claude" || body["cli_session_id"] != nil {
		t.Fatalf("换工具后应清掉会话 id：%#v", body)
	}
	sess := f.s.getSession(f.root, id)
	events, _, _ := f.s.eventsAfter(sess, 0)
	if last := events[len(events)-1]; last.Kind != kindNotice || !strings.Contains(last.Text, "codex") {
		t.Fatalf("换工具应在会话里留一条提示：%#v", last)
	}

	// 只换渲染工具不动会话 id，也不发提示。
	f.s.updateMeta(sess, func(meta *SessionMeta) { meta.CLISessionID = "keep" })
	_, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/agents", `{"renderer":"qoder"}`)
	if body["tool"] != "codex" || body["renderer"] != "qoder" || body["cli_session_id"] != "keep" {
		t.Fatalf("只换渲染工具：%#v", body)
	}
}

func TestSessionAgentsCannotChangeMidTurn(t *testing.T) {
	f := newSessionFixture(t)
	t.Setenv("FAKE_SLEEP", "5")
	_, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", `{}`)
	id := body["session_id"].(string)
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"慢一点"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/agents", `{"tool":"codex"}`)
	if status != http.StatusConflict || body["code"] != "turn_running" {
		t.Fatalf("进行中换工具 → %d %#v", status, body)
	}
	requireStudioStatus(t, mustPost(t, f.url+"/api/sessions/"+id+"/cancel"), http.StatusOK, nil)
	f.waitIdle(t, id)
}

func TestProductionStartAndRendersAcceptAgentChoice(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/production/start", `{"tool":"nope"}`)
	if status != http.StatusBadRequest || body["code"] != "invalid_agent" {
		t.Fatalf("无效工具 → %d %#v", status, body)
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/production/start", `{"renderer":"opencode"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	id := body["session_id"].(string)
	detail := f.waitIdle(t, id)
	if meta := detail["session"].(map[string]any); meta["tool"] != "claude" || meta["renderer"] != "opencode" {
		t.Fatalf("production session=%#v", meta)
	}

	for _, path := range []string{"/api/scenes/scene-001/render", "/api/render/all"} {
		status, body := studioRequest(t, http.MethodPost, f.url+path, `{"renderer":"nope"}`)
		if status != http.StatusBadRequest || body["code"] != "invalid_agent" {
			t.Fatalf("%s → %d %#v", path, status, body)
		}
	}
}

func TestOverrideAgentsKeepsConfigForEmptyChoice(t *testing.T) {
	base := config.Config{Orchestrator: "claude", Renderer: "claude"}
	cfg, err := overrideAgents(base, agentChoice{Renderer: "opencode"})
	if err != nil || cfg.Orchestrator != "claude" || cfg.Renderer != "opencode" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	// ChildEnvironment 把它写进子进程，am scene run 读到的就是这个值。
	if env := cfg.ChildEnvironment(map[string]string{"PATH": "/bin"}, false, nil); env["RENDERER"] != "opencode" {
		t.Fatalf("env RENDERER=%q", env["RENDERER"])
	}
	if _, err := overrideAgents(base, agentChoice{Tool: "nope"}); err == nil {
		t.Fatal("无效工具应报错")
	}
}

func mustPost(t *testing.T, url string) int {
	t.Helper()
	status, _ := studioRequest(t, http.MethodPost, url, "")
	return status
}
