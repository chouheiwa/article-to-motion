package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/hyperframes"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/project"
)

// maxFileBytes 是 /api/files 返回正文的上限。
const maxFileBytes = 2 << 20

// sseHeartbeat 是 SSE 连接上没有事件时发注释行的间隔，防止代理与浏览器断开空闲连接。
const sseHeartbeat = 15 * time.Second

func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sessions", s.sessionsAPI)
	mux.HandleFunc("/api/sessions/", s.sessionAPI)
	mux.HandleFunc("/api/production/start", s.productionStartAPI)
	mux.HandleFunc("/api/turns/", s.turnAPI)
	mux.HandleFunc("/api/commands/", s.commandAPI)
	mux.HandleFunc("/api/files", s.filesAPI)
	mux.HandleFunc("/api/skills", s.skillsAPI)
	mux.HandleFunc("/preview/scenes/", s.previewAPI)
}

func (s *Server) projectOr404(w http.ResponseWriter) (Project, bool) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, http.StatusNotFound, "no_project", err.Error())
		return Project{}, false
	}
	return p, true
}

// writeStartError 把发起对话时的错误映射成状态码。
func (s *Server) writeStartError(w http.ResponseWriter, err error) {
	var busy *BusyError
	switch {
	case errors.As(err, &busy):
		s.error(w, http.StatusConflict, "project_busy", err.Error())
	default:
		s.error(w, http.StatusBadRequest, "turn_rejected", err.Error())
	}
}

func decodeJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

func (s *Server) sessionsAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.write(w, http.StatusOK, map[string]any{"sessions": s.listSessions(p.Path), "busy": s.busyHolder(p.Path)})
	case http.MethodPost:
		var req struct {
			Title string `json:"title"`
		}
		if err := decodeJSON(r, &req); err != nil {
			s.error(w, http.StatusBadRequest, "invalid_json", "请求不是合法 JSON")
			return
		}
		cfg, err := config.Load(p.Path, nil)
		if err != nil {
			s.error(w, http.StatusBadRequest, "invalid_config", err.Error())
			return
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = "对话 " + time.Now().Format("01-02 15:04")
		}
		sess, err := s.createSession(p.Path, "chat", title, cfg.Orchestrator)
		if err != nil {
			s.error(w, http.StatusInternalServerError, "session_failed", err.Error())
			return
		}
		s.write(w, http.StatusCreated, sess.meta)
	default:
		s.method(w, "GET, POST")
	}
}

// sessionAPI 处理 /api/sessions/{id}[/turns|/events|/cancel]。
func (s *Server) sessionAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/"), "/")
	if !validID(parts[0]) {
		s.error(w, http.StatusNotFound, "session_not_found", "会话不存在")
		return
	}
	sess := s.getSession(p.Path, parts[0])
	if sess == nil {
		s.error(w, http.StatusNotFound, "session_not_found", "会话不存在")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		s.sessions.mu.Lock()
		meta := sess.meta
		s.sessions.mu.Unlock()
		turns := []*Turn{}
		for _, id := range meta.Turns {
			if turn, err := loadTurn(p.Path, id); err == nil {
				turn.Before, turn.After = nil, nil
				turns = append(turns, turn)
			}
		}
		s.write(w, http.StatusOK, map[string]any{"session": meta, "turns": turns})
	case "turns":
		if r.Method != http.MethodPost {
			s.method(w, http.MethodPost)
			return
		}
		var req struct {
			Message     string       `json:"message"`
			Attachments []Attachment `json:"attachments"`
		}
		if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Message) == "" {
			s.error(w, http.StatusBadRequest, "invalid_request", "消息不能为空")
			return
		}
		turn, err := s.startTurn(p, sess, turnRequest{Message: strings.TrimSpace(req.Message), Attachments: req.Attachments})
		if err != nil {
			s.writeStartError(w, err)
			return
		}
		s.write(w, http.StatusAccepted, map[string]any{"turn_id": turn.ID, "session_id": sess.meta.ID})
	case "events":
		s.sessionEvents(w, r, sess)
	case "cancel":
		if r.Method != http.MethodPost {
			s.method(w, http.MethodPost)
			return
		}
		if err := s.cancelSession(sess); err != nil {
			s.error(w, http.StatusConflict, "not_running", err.Error())
			return
		}
		s.write(w, http.StatusOK, map[string]any{"ok": true})
	default:
		s.error(w, http.StatusNotFound, "not_found", "会话接口不存在")
	}
}

// sessionEvents 用 SSE 推送会话事件。先补发 Last-Event-ID（或 ?after=）之后的全部事件，
// 然后等新事件；连接一直保持到客户端断开，事件 id 就是 seq，断线重连不丢不重。
func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request, sess *session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, http.StatusInternalServerError, "no_stream", "连接不支持流式响应")
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if value := r.URL.Query().Get("after"); value != "" {
		after, _ = strconv.Atoi(value)
	}
	follow := r.URL.Query().Get("follow") != "0"
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		events, wake, _ := s.eventsAfter(sess, after)
		for _, event := range events {
			body, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Kind, body)
			after = event.Seq
		}
		flusher.Flush()
		if !follow {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
		}
	}
}

func (s *Server) productionStartAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
		Prompt  string `json:"prompt"`
	}
	if err := decodeJSON(r, &req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_json", "请求不是合法 JSON")
		return
	}
	override := ""
	if req.Prompt != "" {
		path, err := containedPath(p.Path, req.Prompt)
		if err != nil {
			s.error(w, http.StatusBadRequest, "invalid_prompt", err.Error())
			return
		}
		override = path
	}
	prompt, promptPath, err := project.RunPrompt(p.Path, override)
	if err != nil {
		s.error(w, http.StatusBadRequest, "prompt_missing", err.Error())
		return
	}
	cfg, err := config.Load(p.Path, nil)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}
	if holder := s.busyHolder(p.Path); holder != "" {
		s.error(w, http.StatusConflict, "project_busy", (&BusyError{Holder: holder}).Error())
		return
	}
	rel, _ := filepath.Rel(p.Path, promptPath)
	sess, err := s.createSession(p.Path, "production", "整片制作（"+filepath.ToSlash(rel)+"）", cfg.Orchestrator)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	turn, err := s.startTurn(p, sess, turnRequest{Message: strings.TrimSpace(req.Message), Prompt: string(prompt)})
	if err != nil {
		s.writeStartError(w, err)
		return
	}
	s.write(w, http.StatusAccepted, map[string]any{"session_id": sess.meta.ID, "turn_id": turn.ID, "prompt": filepath.ToSlash(rel)})
}

// turnAPI 处理 /api/turns/{id}、/api/turns/{id}/file?path=、/api/turns/{id}/revert。
func (s *Server) turnAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/turns/"), "/"), "/")
	turn, err := loadTurn(p.Path, parts[0])
	if err != nil {
		s.error(w, http.StatusNotFound, "turn_not_found", "这一轮不存在")
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		turn.Before, turn.After = nil, nil
		s.write(w, http.StatusOK, turn)
	case "file":
		rel := r.URL.Query().Get("path")
		var change *FileChange
		for i := range turn.Changes {
			if turn.Changes[i].Path == rel {
				change = &turn.Changes[i]
			}
		}
		if change == nil {
			s.error(w, http.StatusNotFound, "file_not_changed", "这一轮没有改动这个文件")
			return
		}
		before, err := readBlob(p.Path, turn.Before[rel])
		if err != nil {
			s.error(w, http.StatusInternalServerError, "blob_missing", err.Error())
			return
		}
		after, err := readBlob(p.Path, turn.After[rel])
		if err != nil {
			s.error(w, http.StatusInternalServerError, "blob_missing", err.Error())
			return
		}
		s.write(w, http.StatusOK, map[string]any{"path": rel, "change": change.Change, "text": change.Revertable || isContentState(turn.Before[rel]) || isContentState(turn.After[rel]), "before": string(before), "after": string(after)})
	case "revert":
		if r.Method != http.MethodPost {
			s.method(w, http.MethodPost)
			return
		}
		reverted, skipped, err := s.revert(p, turn.ID)
		var conflict *ConflictError
		var busy *BusyError
		switch {
		case errors.As(err, &conflict):
			s.write(w, http.StatusConflict, map[string]any{"code": "revert_conflict", "message": err.Error(), "paths": conflict.Paths})
		case errors.As(err, &busy):
			s.error(w, http.StatusConflict, "project_busy", err.Error())
		case err != nil:
			s.error(w, http.StatusConflict, "revert_rejected", err.Error())
		default:
			if skipped == nil {
				skipped = []string{}
			}
			s.write(w, http.StatusOK, map[string]any{"turn_id": reverted.ID, "status": reverted.Status, "skipped": skipped})
		}
	default:
		s.error(w, http.StatusNotFound, "not_found", "接口不存在")
	}
}

// studioCommands 是可以从网页直接触发的确定性步骤。它们不经过编排工具，
// 参数也只有镜头 id，不接受任意命令行。
var studioCommands = map[string]string{
	"scene.validate": "校验镜头",
	"project.concat": "拼接无声母版",
	"publish.check":  "检查发布信息",
	"beat.confirm":   "确认卡点网格",
}

func (s *Server) commandAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/commands/"), "/")
	label, known := studioCommands[name]
	if !known {
		s.error(w, http.StatusNotFound, "unknown_command", "不支持的命令："+name)
		return
	}
	var req struct {
		SceneID string `json:"scene_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid_json", "请求不是合法 JSON")
		return
	}
	var args []string
	switch name {
	case "scene.validate":
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == req.SceneID {
				args = []string{"scene", "lint", sc.Directory}
			}
		}
		if args == nil {
			s.error(w, http.StatusNotFound, "scene_not_found", "镜头不存在")
			return
		}
		label += " " + req.SceneID
	case "project.concat":
		args = []string{"concat", filepath.Join(p.Path, "scenes"), "--out", filepath.Join(p.Path, "production", "silent-master.mp4")}
	case "publish.check":
		publish := filepath.Join(p.Path, "publish.md")
		if !fileExists(publish) {
			s.error(w, http.StatusBadRequest, "publish_missing", "找不到 publish.md")
			return
		}
		args = []string{"validate", "publish", publish, "--project-root", p.Path}
	case "beat.confirm":
		args = []string{"beat", "confirm", "--project-root", p.Path}
	}
	t, err := s.runWriteTask(p, name, label, func(ctx context.Context, t *Task) error {
		if name == "project.concat" {
			if err := os.MkdirAll(filepath.Join(p.Path, "production"), 0o755); err != nil {
				return err
			}
		}
		_, err := s.runStudioCLI(ctx, p, args...)
		return err
	})
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	s.write(w, http.StatusAccepted, map[string]any{"task_id": t.ID, "command": name})
}

// filesAPI 只读返回项目里的一个文件。超过上限或含 NUL 的按二进制处理，不返回正文。
func (s *Server) filesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	path, err := containedPath(p.Path, rel)
	if err != nil {
		if os.IsNotExist(err) {
			s.error(w, http.StatusNotFound, "file_not_found", "文件不存在")
			return
		}
		s.error(w, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	if isEnvFile(path) {
		s.error(w, http.StatusForbidden, "secret_file", "Studio 不读取 .env")
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		s.error(w, http.StatusNotFound, "file_not_found", "文件不存在")
		return
	}
	result := map[string]any{"path": filepath.ToSlash(filepath.Clean(rel)), "size": info.Size(), "binary": true, "content": ""}
	if info.Size() <= maxFileBytes {
		body, err := os.ReadFile(path)
		if err == nil && !strings.ContainsRune(string(body[:min(len(body), 8192)]), 0) {
			result["binary"], result["content"] = false, string(body)
		}
	}
	s.write(w, http.StatusOK, result)
}

func isEnvFile(path string) bool {
	base := filepath.Base(path)
	return base == ".env" || strings.HasPrefix(base, ".env.") && base != ".env.example"
}

// previewAPI 静态服务镜头目录，供网页用 iframe 打开合成实时预览。
func (s *Server) previewAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/preview/scenes/")
	id, file, _ := strings.Cut(rest, "/")
	for _, sc := range discoverScenes(p.Path) {
		if sc.ID != id {
			continue
		}
		if file == "" {
			file = "index.html"
		}
		sceneRel, err := filepath.Rel(p.Path, sc.Directory)
		if err != nil {
			break
		}
		path, err := containedPath(p.Path, filepath.Join(sceneRel, filepath.FromSlash(file)))
		if err != nil || !strings.HasPrefix(path, sc.Directory+string(filepath.Separator)) || isEnvFile(path) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
		return
	}
	http.NotFound(w, r)
}

// skillsAPI 列出项目里安装的技能与上游技能的来源记录。
func (s *Server) skillsAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	dir := filepath.Join(p.Path, ".agents", "skills")
	type skill struct {
		Name     string `json:"name"`
		HasSkill bool   `json:"has_skill_md"`
	}
	skills := []skill{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				skills = append(skills, skill{Name: entry.Name(), HasSkill: fileExists(filepath.Join(dir, entry.Name(), "SKILL.md"))})
			}
		}
	}
	var upstream any
	if body, err := os.ReadFile(filepath.Join(dir, hyperframes.ManifestFile)); err == nil {
		_ = json.Unmarshal(body, &upstream)
	}
	s.write(w, http.StatusOK, map[string]any{"skills": skills, "upstream": upstream, "pinned_hyperframes": hyperframes.PinnedVersion})
}

// sceneAttachment 在镜头产物的某一秒抽一帧存成附件，供下一条消息引用。
func (s *Server) sceneAttachment(w http.ResponseWriter, r *http.Request, p Project, sc SceneSummary) {
	var req struct {
		Seconds float64 `json:"seconds"`
		Region  *Box    `json:"region"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Seconds < 0 {
		s.error(w, http.StatusBadRequest, "invalid_request", "需要非负的 seconds")
		return
	}
	if !sc.Exists {
		s.error(w, http.StatusBadRequest, "scene_not_rendered", "镜头还没有产物")
		return
	}
	toolchain, err := mediaprobe.New(envutil.EnvMap())
	if err != nil {
		s.error(w, http.StatusInternalServerError, "ffprobe_missing", err.Error())
		return
	}
	name := fmt.Sprintf("%s-%s.png", sc.ID, newID("a"))
	rel := filepath.ToSlash(filepath.Join("production", "studio", "attachments", name))
	if err := toolchain.ExtractFrame(filepath.Join(sc.Directory, sc.Output), req.Seconds, filepath.Join(p.Path, filepath.FromSlash(rel))); err != nil {
		s.error(w, http.StatusInternalServerError, "extract_failed", err.Error())
		return
	}
	s.write(w, http.StatusCreated, Attachment{SceneID: sc.ID, Path: rel, Seconds: req.Seconds, Region: req.Region})
}
