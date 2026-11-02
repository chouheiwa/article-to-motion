package studio

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/agentrun"
)

// fakeClaude 是一个假的 claude：记下参数，按 --session-id / --resume 回报会话，
// 改写 scenes/scene-001/prompt.md，输出一组 stream-json。
//
// 子进程环境按安全模式白名单构造，测试用的开关要经 AM_PASSTHROUGH_ENV 透传。
const fakeClaude = `#!/bin/sh
printf '%s' "$*" | tr '\n' ' ' >> "$FAKE_ARGS_LOG"; echo >> "$FAKE_ARGS_LOG"
sid=""; prev=""; resume=""
for a in "$@"; do
  if [ "$prev" = "--session-id" ]; then sid="$a"; fi
  if [ "$prev" = "--resume" ]; then sid="$a"; resume=1; fi
  prev="$a"
done
if [ -n "$resume" ] && [ -n "$FAKE_FAIL_RESUME" ]; then echo "No conversation found with session ID: $sid" >&2; exit 1; fi
printf '{"type":"system","subtype":"init","session_id":"%s"}\n' "$sid"
if [ -n "$FAKE_SLEEP" ]; then sleep "$FAKE_SLEEP"; fi
printf '%s\n' "${FAKE_CONTENT:-edited}" > scenes/scene-001/prompt.md
printf '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Write","input":{"file_path":"scenes/scene-001/prompt.md"}}]}}\n'
printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"改好了"}]}}\n'
printf '{"type":"result","subtype":"success","is_error":false,"result":"改好了","usage":{"input_tokens":3,"output_tokens":4},"total_cost_usd":0.01,"num_turns":1}\n'
`

type sessionFixture struct {
	root, argsLog string
	s             *Server
	url           string
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"article-to-motion.conf":                   "ORCHESTRATOR=claude\nRENDERER=claude\n",
		"PROMPT.md":                                "整片制作提示词",
		"scenes/scene-001/scene.json":              `{"id":"scene-001","duration_seconds":1,"output":"out.mp4"}`,
		"scenes/scene-001/prompt.md":               "original",
		"scenes/scene-001/index.html":              "<html>composition</html>",
		"scenes/scene-001/.env":                    "NOTE=1",
		".env":                                     "NOTE=1",
		".agents/skills/scene-kit/SKILL.md":        "skill",
		".agents/skills/hyperframes-upstream.json": `{"commit":"abc"}`,
	})
	bin := t.TempDir()
	writeStudioFile(t, filepath.Join(bin, "claude"), fakeClaude)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCHESTRATOR", "claude")
	t.Setenv("RENDERER", "claude")
	t.Setenv("FAKE_ARGS_LOG", argsLog)
	t.Setenv("FAKE_SLEEP", "")
	t.Setenv("FAKE_FAIL_RESUME", "")
	t.Setenv("FAKE_CONTENT", "")
	t.Setenv("AM_PASSTHROUGH_ENV", "FAKE_ARGS_LOG,FAKE_SLEEP,FAKE_FAIL_RESUME,FAKE_CONTENT")
	s, ts := startStudio(t, Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	// 服务端按 EvalSymlinks 后的路径区分项目（macOS 的 /var → /private/var），测试要用同一个。
	p, err := s.currentProject()
	if err != nil {
		t.Fatal(err)
	}
	return &sessionFixture{root: p.Path, argsLog: argsLog, s: s, url: ts.URL}
}

func (f *sessionFixture) waitIdle(t *testing.T, sessionID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body := studioRequest(t, http.MethodGet, f.url+"/api/sessions/"+sessionID, "")
		requireStudioStatus(t, status, http.StatusOK, body)
		if body["session"].(map[string]any)["running_turn"] == nil && f.s.busyHolder(f.root) == "" {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("对话没有结束")
	return nil
}

func (f *sessionFixture) args(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(f.argsLog)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func (f *sessionFixture) readScenePrompt(t *testing.T) string {
	t.Helper()
	body, _ := os.ReadFile(filepath.Join(f.root, "scenes", "scene-001", "prompt.md"))
	return strings.TrimSpace(string(body))
}

func lastTurn(t *testing.T, detail map[string]any) map[string]any {
	t.Helper()
	turns := studioList(t, detail["turns"])
	return turns[len(turns)-1].(map[string]any)
}

func TestSessionTurnResumeAndRevert(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", `{"title":"改镜头"}`)
	requireStudioStatus(t, status, http.StatusCreated, body)
	id := body["session_id"].(string)
	if body["tool"] != "claude" || body["kind"] != "chat" {
		t.Fatalf("session=%#v", body)
	}

	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"把开头改快一点"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	detail := f.waitIdle(t, id)
	first := lastTurn(t, detail)
	if first["status"] != "succeeded" || f.readScenePrompt(t) != "edited" {
		t.Fatalf("turn=%#v prompt=%q", first, f.readScenePrompt(t))
	}
	changes := studioList(t, first["changes"])
	if len(changes) != 1 || changes[0].(map[string]any)["path"] != "scenes/scene-001/prompt.md" {
		t.Fatalf("changes=%#v", changes)
	}
	cliSession := detail["session"].(map[string]any)["cli_session_id"].(string)
	args := f.args(t)
	if !strings.Contains(args[0], "--session-id "+cliSession) || !strings.Contains(args[0], "把开头改快一点") || !strings.Contains(args[0], "动手前先读项目规则") {
		t.Fatalf("第一轮参数=%s", args[0])
	}

	// 第二轮续接同一个 CLI 会话，不再带开场说明。
	t.Setenv("FAKE_CONTENT", "edited twice")
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"再慢一点"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	detail = f.waitIdle(t, id)
	second := lastTurn(t, detail)
	args = f.args(t)
	if !strings.Contains(args[1], "--resume "+cliSession) || strings.Contains(args[1], "动手前先读项目规则") {
		t.Fatalf("第二轮参数=%s", args[1])
	}

	// 事件流：断线重放从 Last-Event-ID 之后开始。
	status, raw := rawStudioGet(t, f.url+"/api/sessions/"+id+"/events?follow=0")
	for _, want := range []string{"event: user", "event: turn-start", "event: session", "event: tool-call", "event: text", "event: files-changed", "event: usage", "event: turn-end"} {
		if status != http.StatusOK || !strings.Contains(raw, want) {
			t.Fatalf("缺少 %s：%s", want, raw)
		}
	}
	if strings.Contains(raw, "event: done") {
		t.Fatal("done 是 agentrun 内部事件，不进会话流")
	}
	status, tail := rawStudioGet(t, f.url+"/api/sessions/"+id+"/events?follow=0&after=1000")
	if status != http.StatusOK || strings.Contains(tail, "event:") {
		t.Fatalf("after 之后不应再有事件：%q", tail)
	}

	// 改动详情：前后内容都能取到。
	status, body = studioRequest(t, http.MethodGet, f.url+"/api/turns/"+second["turn_id"].(string)+"/file?path=scenes/scene-001/prompt.md", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["before"] != "edited\n" || body["after"] != "edited twice\n" {
		t.Fatalf("file diff=%#v", body)
	}

	// 先回退第一轮：第二轮之后文件又变了，必须冲突。
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/turns/"+first["turn_id"].(string)+"/revert", "")
	requireStudioStatus(t, status, http.StatusConflict, body)
	if body["code"] != "revert_conflict" {
		t.Fatalf("revert=%#v", body)
	}
	// 依次回退第二轮、第一轮，回到最初。
	for _, turn := range []map[string]any{second, first} {
		status, body = studioRequest(t, http.MethodPost, f.url+"/api/turns/"+turn["turn_id"].(string)+"/revert", "")
		requireStudioStatus(t, status, http.StatusOK, body)
	}
	if f.readScenePrompt(t) != "original" {
		t.Fatalf("回退后=%q", f.readScenePrompt(t))
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/turns/"+first["turn_id"].(string)+"/revert", "")
	requireStudioStatus(t, status, http.StatusConflict, body)

	// 重启 Studio 后会话与事件都还在。
	s2, err := New(Config{ProjectPath: f.root, ConfigPath: f.s.config.ConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	sessions := s2.listSessions(f.root)
	if len(sessions) != 1 || sessions[0].CLISessionID != cliSession || len(sessions[0].Turns) != 2 {
		t.Fatalf("restored=%+v", sessions)
	}
	if events := s2.getSession(f.root, id).events; len(events) == 0 || events[len(events)-1].Kind != kindTurnReverted {
		t.Fatalf("事件没有恢复：%d", len(events))
	}
}

func TestResumeFailureReseedsTranscript(t *testing.T) {
	f := newSessionFixture(t)
	_, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", `{}`)
	id := body["session_id"].(string)
	studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"第一句"}`)
	f.waitIdle(t, id)
	t.Setenv("FAKE_FAIL_RESUME", "1")
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"第二句"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	detail := f.waitIdle(t, id)
	if lastTurn(t, detail)["status"] != "succeeded" {
		t.Fatalf("turn=%#v", lastTurn(t, detail))
	}
	args := f.args(t)
	if len(args) != 3 || !strings.Contains(args[1], "--resume") || !strings.Contains(args[2], "--session-id") {
		t.Fatalf("args=%q", args)
	}
	if !strings.Contains(args[2], "之前的对话") || !strings.Contains(args[2], "第一句") || !strings.Contains(args[2], "改好了") || !strings.Contains(args[2], "第二句") {
		t.Fatalf("补发的提示词没带上对话记录：%s", args[2])
	}
	_, raw := rawStudioGet(t, f.url+"/api/sessions/"+id+"/events?follow=0")
	if !strings.Contains(raw, "原会话无法续接") {
		t.Fatalf("应提示续接失败：%s", raw)
	}
}

func TestProductionStartBusyAndCancel(t *testing.T) {
	f := newSessionFixture(t)
	t.Setenv("FAKE_SLEEP", "5")
	status, body := studioRequest(t, http.MethodPost, f.url+"/api/production/start", `{"message":"竖屏，三分钟"}`)
	requireStudioStatus(t, status, http.StatusAccepted, body)
	id := body["session_id"].(string)
	if body["prompt"] != "PROMPT.md" {
		t.Fatalf("start=%#v", body)
	}
	// 进行中：其他写入一律 409。
	for _, path := range []string{"/api/production/start", "/api/render/all", "/api/scenes/scene-001/render", "/api/commands/project.concat"} {
		status, body = studioRequest(t, http.MethodPost, f.url+path, "{}")
		requireStudioStatus(t, status, http.StatusConflict, body)
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/turns", `{"message":"再来"}`)
	requireStudioStatus(t, status, http.StatusConflict, body)
	status, body = studioRequest(t, http.MethodGet, f.url+"/api/sessions", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["busy"] == "" {
		t.Fatalf("sessions=%#v", body)
	}
	// 等编排工具真正启动后再取消。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(f.argsLog); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/cancel", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	detail := f.waitIdle(t, id)
	turn := lastTurn(t, detail)
	if turn["status"] != "cancelled" || detail["session"].(map[string]any)["kind"] != "production" {
		t.Fatalf("turn=%#v", turn)
	}
	if args := f.args(t); !strings.Contains(args[0], "整片制作提示词") || !strings.Contains(args[0], "用户补充：竖屏，三分钟") {
		t.Fatalf("args=%q", args)
	}
	status, body = studioRequest(t, http.MethodPost, f.url+"/api/sessions/"+id+"/cancel", "")
	requireStudioStatus(t, status, http.StatusConflict, body)
}

func TestInterruptedTurnIsMarkedOnRestart(t *testing.T) {
	f := newSessionFixture(t)
	sess, err := f.s.createSession(f.root, "chat", "x", "claude")
	if err != nil {
		t.Fatal(err)
	}
	turn := &Turn{ID: newID("t"), SessionID: sess.meta.ID, Status: "running", StartedAt: time.Now()}
	if err := saveTurn(f.root, turn); err != nil {
		t.Fatal(err)
	}
	f.s.updateMeta(sess, func(meta *SessionMeta) { meta.Turns, meta.RunningTurn = []string{turn.ID}, turn.ID })
	s2, err := New(Config{ProjectPath: f.root, ConfigPath: f.s.config.ConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.listSessions(f.root); got[0].RunningTurn != "" {
		t.Fatalf("meta=%+v", got[0])
	}
	if loaded, _ := loadTurn(f.root, turn.ID); loaded.Status != "interrupted" {
		t.Fatalf("turn=%+v", loaded)
	}
}

func TestSessionRequestValidation(t *testing.T) {
	f := newSessionFixture(t)
	_, body := studioRequest(t, http.MethodPost, f.url+"/api/sessions", `{}`)
	id := body["session_id"].(string)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/sessions", "{", http.StatusBadRequest},
		{http.MethodDelete, "/api/sessions", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/sessions/../x", "", http.StatusNotFound},
		{http.MethodGet, "/api/sessions/s-missing", "", http.StatusNotFound},
		{http.MethodGet, "/api/sessions/" + id + "/bogus", "", http.StatusNotFound},
		{http.MethodGet, "/api/sessions/" + id + "/turns", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/sessions/" + id + "/turns", `{"message":"  "}`, http.StatusBadRequest},
		{http.MethodPost, "/api/sessions/" + id + "/turns", `{"message":"x","attachments":[{"path":"../../etc/passwd"}]}`, http.StatusBadRequest},
		{http.MethodPost, "/api/sessions/" + id + "/turns", `{"message":"x","attachments":[{"path":"production/studio/attachments/missing.png"}]}`, http.StatusBadRequest},
		{http.MethodPost, "/api/sessions/" + id + "/turns", `{"message":"x","attachments":[{"source":"../outside.html"}]}`, http.StatusBadRequest},
		{http.MethodGet, "/api/sessions/" + id + "/cancel", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/production/start", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/production/start", `{"prompt":"../x.md"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/production/start", `{"prompt":"missing.md"}`, http.StatusBadRequest},
		{http.MethodGet, "/api/turns/t-missing", "", http.StatusNotFound},
		{http.MethodPost, "/api/commands/rm-rf", "{}", http.StatusNotFound},
		{http.MethodGet, "/api/commands/project.concat", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/commands/scene.validate", `{"scene_id":"missing"}`, http.StatusNotFound},
		{http.MethodPost, "/api/commands/publish.check", "{}", http.StatusBadRequest},
		{http.MethodGet, "/api/scenes/scene-001/render", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/scenes/scene-001/attachment", `{"seconds":0}`, http.StatusBadRequest},
		{http.MethodPost, "/api/scenes/scene-001/attachment", `{"seconds":-1}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		status, body := studioRequest(t, c.method, f.url+c.path, c.body)
		if status != c.want {
			t.Fatalf("%s %s → %d %#v，期望 %d", c.method, c.path, status, body, c.want)
		}
	}
}

func TestFilesPreviewAndSkills(t *testing.T) {
	f := newSessionFixture(t)
	status, body := studioRequest(t, http.MethodGet, f.url+"/api/files?path=scenes/scene-001/prompt.md", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	if body["content"] != "original" || body["binary"] != false {
		t.Fatalf("file=%#v", body)
	}
	for path, want := range map[string]int{"../etc/passwd": 400, "/etc/passwd": 400, ".env": 403, "missing.md": 404, "scenes": 404} {
		status, body = studioRequest(t, http.MethodGet, f.url+"/api/files?path="+path, "")
		if status != want {
			t.Fatalf("%s → %d %#v", path, status, body)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(f.root, "escape")); err == nil {
		status, _ = studioRequest(t, http.MethodGet, f.url+"/api/files?path=escape/hosts", "")
		if status != http.StatusBadRequest {
			t.Fatalf("符号链接逃逸应被拒：%d", status)
		}
	}
	writeStudioFile(t, filepath.Join(f.root, "blob.bin"), "a\x00b")
	_, body = studioRequest(t, http.MethodGet, f.url+"/api/files?path=blob.bin", "")
	if body["binary"] != true || body["content"] != "" {
		t.Fatalf("binary=%#v", body)
	}

	status, raw := rawStudioGet(t, f.url+"/preview/scenes/scene-001/")
	if status != http.StatusOK || !strings.Contains(raw, "composition") {
		t.Fatalf("preview=%d %q", status, raw)
	}
	for _, path := range []string{"/preview/scenes/scene-001/.env", "/preview/scenes/missing/index.html", "/preview/scenes/scene-001/%2e%2e/%2e%2e/PROMPT.md"} {
		if status, raw := rawStudioGet(t, f.url+path); status == http.StatusOK && (strings.Contains(raw, "NOTE=1") || strings.Contains(raw, "整片制作提示词")) {
			t.Fatalf("%s 不应可读：%q", path, raw)
		}
	}

	status, body = studioRequest(t, http.MethodGet, f.url+"/api/skills", "")
	requireStudioStatus(t, status, http.StatusOK, body)
	skills := studioList(t, body["skills"])
	if len(skills) != 1 || skills[0].(map[string]any)["name"] != "scene-kit" || body["upstream"].(map[string]any)["commit"] != "abc" {
		t.Fatalf("skills=%#v", body)
	}
}

func TestSessionEventsFollowStreamsNewEvents(t *testing.T) {
	f := newSessionFixture(t)
	sess, err := f.s.createSession(f.root, "chat", "x", "claude")
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(f.url + "/api/sessions/" + sess.meta.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.s.emit(sess, "", SessionEvent{Event: eventOf(kindNotice, "hello")})
	}()
	reader := bufio.NewReader(res.Body)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "hello") {
			return
		}
	}
	t.Fatal("没有收到新事件")
}

func TestAttachmentText(t *testing.T) {
	text := attachmentText([]Attachment{{SceneID: "scene-001", Path: "production/studio/attachments/a.png", Seconds: 1.5, Region: &Box{X: 1, Y: 2, W: 3, H: 4}, Selector: "#title", Source: "scenes/scene-001/index.html", Note: "太挤"}})
	for _, want := range []string{"镜头 scene-001", "1.50 秒", "a.png", "x=1 y=2 宽 3 高 4", "`#title`", "index.html", "太挤"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q：%s", want, text)
		}
	}
	if attachmentText(nil) != "" {
		t.Fatal("没有附件时不加内容")
	}
	if got := transcript([]SessionEvent{{Event: eventOf(kindUser, strings.Repeat("长", transcriptLimit+10))}}); len([]rune(got)) > transcriptLimit {
		t.Fatalf("对话记录没有截断：%d", len([]rune(got)))
	}
}

func eventOf(kind agentrun.Kind, text string) agentrun.Event {
	return agentrun.Event{Kind: kind, Text: text}
}
