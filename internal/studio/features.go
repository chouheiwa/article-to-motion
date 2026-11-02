package studio

// The first Studio slice intentionally keeps write operations small and
// explicit. Every mutation goes through an operation record and a snapshot;
// this gives the UI a reviewable boundary before later render integrations are
// added.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

type Task struct {
	ID        string    `json:"task_id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Progress  float64   `json:"progress"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type taskEvent struct {
	Event     string    `json:"event"`
	TaskID    string    `json:"task_id"`
	Progress  float64   `json:"progress"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}
type featureState struct {
	mu         sync.RWMutex
	tasks      map[string]*Task
	taskEvents map[string][]taskEvent
	taskCancel map[string]context.CancelFunc
	seq        uint64
}

func (s *Server) studioTasksPath() string {
	p, err := s.currentProject()
	if err != nil {
		return ""
	}
	return filepath.Join(p.Path, "production", "studio", "tasks.json")
}

func (s *Server) loadPersistedTasks() {
	path := s.studioTasksPath()
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var tasks []Task
	if json.Unmarshal(body, &tasks) != nil {
		return
	}
	s.features.mu.Lock()
	for i := range tasks {
		t := tasks[i]
		s.features.tasks[t.ID] = &t
		s.features.taskEvents[t.ID] = append(s.features.taskEvents[t.ID], taskEvent{Event: "task.restored", TaskID: t.ID, Progress: t.Progress, Message: t.Message, Timestamp: t.UpdatedAt})
	}
	s.features.mu.Unlock()
}

func (s *Server) studioDir() string {
	path := s.studioTasksPath()
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func (s *Server) persistTasksLocked() {
	path := s.studioTasksPath()
	if path == "" {
		return
	}
	list := make([]Task, 0, len(s.features.tasks))
	for _, task := range s.features.tasks {
		list = append(list, *task)
	}
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, append(body, '\n'), 0o644)
}

func (s *Server) ensureFeatures() {
	s.features.mu.Lock()
	defer s.features.mu.Unlock()
	if s.features.tasks == nil {
		s.features.tasks = map[string]*Task{}
		s.features.taskEvents = map[string][]taskEvent{}
		s.features.taskCancel = map[string]context.CancelFunc{}
	}
}
func nextID(prefix string, seq *uint64) string {
	*seq++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), *seq)
}
func (s *Server) addTask(typ, msg string) *Task {
	s.ensureFeatures()
	s.features.mu.Lock()
	defer s.features.mu.Unlock()
	id := nextID("task", &s.features.seq)
	now := time.Now().UTC()
	t := &Task{ID: id, Type: typ, Status: "queued", Message: msg, CreatedAt: now, UpdatedAt: now}
	s.features.tasks[id] = t
	s.features.taskEvents[id] = append(s.features.taskEvents[id], taskEvent{Event: "task.queued", TaskID: id, Message: msg, Timestamp: now})
	s.persistTasksLocked()
	return t
}
func (s *Server) updateTask(id, status, msg string, progress float64, taskErr error) {
	s.features.mu.Lock()
	defer s.features.mu.Unlock()
	t := s.features.tasks[id]
	if t == nil {
		return
	}
	t.Status = status
	t.Message = msg
	t.Progress = progress
	t.UpdatedAt = time.Now().UTC()
	if taskErr != nil {
		t.Error = taskErr.Error()
	}
	ev := "task.progress"
	if status == "succeeded" {
		ev = "task.completed"
	}
	if status == "failed" {
		ev = "task.failed"
	}
	s.features.taskEvents[id] = append(s.features.taskEvents[id], taskEvent{Event: ev, TaskID: id, Progress: progress, Message: msg, Timestamp: t.UpdatedAt})
	s.persistTasksLocked()
}
func (s *Server) runTask(t *Task, work func(context.Context) error) {
	s.updateTask(t.ID, "running", "正在处理", .1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	s.features.mu.Lock()
	s.features.taskCancel[t.ID] = cancel
	s.features.mu.Unlock()
	go func() {
		err := work(ctx)
		s.features.mu.Lock()
		delete(s.features.taskCancel, t.ID)
		s.features.mu.Unlock()
		s.features.mu.RLock()
		cancelled := s.features.tasks[t.ID] != nil && s.features.tasks[t.ID].Status == "cancelled"
		s.features.mu.RUnlock()
		if cancelled {
			return
		}
		if err != nil {
			s.updateTask(t.ID, "failed", err.Error(), 1, err)
			return
		}
		s.updateTask(t.ID, "succeeded", "任务完成", 1, nil)
	}()
}

// runWriteTask 占用项目写权后再跑任务，任务结束时释放。渲染、拼接、复查都会改项目文件，
// 不能和一轮对话同时进行：快照会把任务写的文件算进那一轮的改动，回退时误删。
func (s *Server) runWriteTask(p Project, typ, msg string, work func(context.Context, *Task) error) (*Task, error) {
	if err := s.acquire(p.Path, msg); err != nil {
		return nil, err
	}
	t := s.addTask(typ, msg)
	s.runTask(t, func(ctx context.Context) error {
		defer s.release(p.Path)
		return work(ctx, t)
	})
	return t, nil
}

// writeBusy 把 BusyError 写成 409，其他错误写成 500。
func (s *Server) writeTaskError(w http.ResponseWriter, err error) {
	var busy *BusyError
	if errors.As(err, &busy) {
		s.error(w, http.StatusConflict, "project_busy", err.Error())
		return
	}
	s.error(w, http.StatusInternalServerError, "task_failed", err.Error())
}

func (s *Server) featureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/scenes/", s.sceneFeatureAPI)
	mux.HandleFunc("/api/tasks", s.tasksAPI)
	mux.HandleFunc("/api/tasks/", s.taskFeatureAPI)
	mux.HandleFunc("/api/review", s.reviewAPI)
	mux.HandleFunc("/api/review/run", s.reviewRunAPI)
	mux.HandleFunc("/api/render/all", s.renderAllAPI)
}

func (s *Server) renderAllAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	scenes := discoverScenes(p.Path)
	if len(scenes) == 0 {
		s.error(w, 400, "no_scenes", "项目中没有可渲染的镜头")
		return
	}
	var choice agentChoice
	if err := decodeJSON(r, &choice); err != nil || choice.validate() != nil {
		s.error(w, http.StatusBadRequest, "invalid_agent", "渲染工具无效")
		return
	}
	t, err := s.runWriteTask(p, "scenes.render", fmt.Sprintf("渲染全部 %d 个镜头", len(scenes)), func(ctx context.Context, t *Task) error {
		for index, scene := range scenes {
			if err := ctx.Err(); err != nil {
				return err
			}
			s.updateTask(t.ID, "running", fmt.Sprintf("正在渲染 %s（%d/%d）", scene.ID, index+1, len(scenes)), float64(index)/float64(len(scenes)), nil)
			if _, err := s.runStudioCLIWith(ctx, p, agentChoice{Renderer: choice.Renderer}, "scene", "run", scene.Directory); err != nil {
				return fmt.Errorf("%s：%w", scene.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	s.write(w, http.StatusAccepted, map[string]any{"task_id": t.ID, "scene_count": len(scenes)})
}

func (s *Server) sceneFeatureAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/scenes/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		s.sceneAPI(w, p, parts[0])
		return
	}
	id, action := parts[0], parts[1]
	for _, sc := range discoverScenes(p.Path) {
		if sc.ID != id {
			continue
		}
		switch action {
		case "render":
			if r.Method != http.MethodPost {
				s.method(w, http.MethodPost)
				return
			}
			var choice agentChoice
			if err := decodeJSON(r, &choice); err != nil || choice.validate() != nil {
				s.error(w, http.StatusBadRequest, "invalid_agent", "渲染工具无效")
				return
			}
			sceneDir := sc.Directory
			t, err := s.runWriteTask(p, "scene.render", "渲染 "+id, func(ctx context.Context, t *Task) error {
				_, err := s.runStudioCLIWith(ctx, p, agentChoice{Renderer: choice.Renderer}, "scene", "run", sceneDir)
				return err
			})
			if err != nil {
				s.writeTaskError(w, err)
				return
			}
			s.write(w, http.StatusAccepted, map[string]any{"task_id": t.ID, "scene_id": id})
			return
		case "attachment":
			if r.Method != http.MethodPost {
				s.method(w, http.MethodPost)
				return
			}
			s.sceneAttachment(w, r, p, sc)
			return
		case "asset":
			asset := filepath.Join(sc.Directory, sc.Output)
			rel, relErr := filepath.Rel(sc.Directory, asset)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				s.error(w, http.StatusForbidden, "asset_outside_scene", "镜头产物不得逃出镜头目录")
				return
			}
			http.ServeFile(w, r, asset)
			return
		case "frames":
			if len(parts) > 2 {
				frameName := filepath.Base(parts[2])
				framePath := filepath.Join(sc.Directory, ".studio-frames", frameName)
				if frameName != parts[2] || !strings.HasSuffix(frameName, ".jpg") {
					s.error(w, http.StatusBadRequest, "invalid_frame", "无效的帧路径")
					return
				}
				http.ServeFile(w, r, framePath)
				return
			}
			if r.Method == http.MethodPost {
				t := s.addTask("scene.capture_frames", "抽取 "+id+" 关键帧")
				s.runTask(t, func(ctx context.Context) error { return s.captureSceneFrames(ctx, p, sc) })
				s.write(w, http.StatusAccepted, map[string]any{"task_id": t.ID, "scene_id": id})
				return
			}
			frames := []string{}
			frameDir := filepath.Join(sc.Directory, ".studio-frames")
			if entries, readErr := os.ReadDir(frameDir); readErr == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jpg") {
						frames = append(frames, filepath.ToSlash(filepath.Join(".studio-frames", entry.Name())))
					}
				}
			}
			s.write(w, 200, map[string]any{"scene_id": id, "frames": frames})
			return
		}
	}
	s.error(w, 404, "scene_not_found", "镜头不存在")
}

func (s *Server) captureSceneFrames(ctx context.Context, p Project, sc SceneSummary) error {
	if !sc.Exists {
		return errors.New("镜头产物不存在")
	}
	dir := filepath.Join(sc.Directory, ".studio-frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	toolchain, err := mediaprobe.New(envutil.EnvMap())
	if err != nil {
		return err
	}
	for i, second := range []float64{0, sc.DurationSeconds / 2, maxFloat(sc.DurationSeconds-0.05, 0)} {
		if err := ctx.Err(); err != nil {
			return err
		}
		out := filepath.Join(dir, fmt.Sprintf("frame-%02d.jpg", i+1))
		if err := toolchain.ExtractFrame(filepath.Join(sc.Directory, sc.Output), second, out); err != nil {
			return fmt.Errorf("抽帧失败：%w", err)
		}
	}
	return nil
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func (s *Server) scriptAPI(w http.ResponseWriter, _ *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	for _, name := range []string{"SCRIPT.md", "script.md", "transcription.srt"} {
		path := filepath.Join(p.Path, name)
		body, readErr := os.ReadFile(path)
		if readErr == nil {
			s.write(w, 200, map[string]any{"path": name, "content": string(body)})
			return
		}
	}
	s.write(w, 200, map[string]any{"path": "", "content": ""})
}

func (s *Server) assetsAPI(w http.ResponseWriter, _ *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	type asset struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	assets := []asset{}
	for _, root := range []string{"assets", "cast"} {
		base := filepath.Join(p.Path, root)
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			rel, relErr := filepath.Rel(p.Path, path)
			if relErr == nil {
				assets = append(assets, asset{Path: filepath.ToSlash(rel), Size: info.Size()})
			}
			return nil
		})
	}
	s.write(w, 200, map[string]any{"assets": assets})
}

func (s *Server) publishAPI(w http.ResponseWriter, _ *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	path := filepath.Join(p.Path, "publish.md")
	body, readErr := os.ReadFile(path)
	result := map[string]any{"available": readErr == nil, "path": "publish.md", "content": string(body), "covers": []any{}}
	coverDir := filepath.Join(p.Path, "covers")
	_ = filepath.WalkDir(coverDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(p.Path, path)
		if relErr == nil {
			cover := map[string]any{"path": filepath.ToSlash(rel), "size": info.Size()}
			if imageBody, readErr := os.ReadFile(path); readErr == nil {
				sum := sha256.Sum256(imageBody)
				cover["sha256"] = hex.EncodeToString(sum[:])
				if decoded, _, decodeErr := image.Decode(bytes.NewReader(imageBody)); decodeErr == nil {
					bounds := decoded.Bounds()
					cover["width"] = bounds.Dx()
					cover["height"] = bounds.Dy()
				} else {
					cover["error"] = "无法读取图片尺寸"
				}
			}
			result["covers"] = append(result["covers"].([]any), cover)
		}
		return nil
	})
	s.write(w, 200, result)
}

func findProjectVideo(root string) string {
	for _, candidate := range []string{filepath.Join(root, "production", "audio", "candidate.mp4"), filepath.Join(root, "final.mp4"), filepath.Join(root, "production", "final.mp4")} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func (s *Server) runStudioCLI(ctx context.Context, project Project, args ...string) ([]byte, error) {
	return s.runStudioCLIWith(ctx, project, agentChoice{}, args...)
}

// runStudioCLIWith 与 runStudioCLI 相同，但用网页选的工具覆盖项目配置：子进程环境里的
// RENDERER 优先于 article-to-motion.conf，am scene run 因此用网页选的渲染工具。
func (s *Server) runStudioCLIWith(ctx context.Context, project Project, choice agentChoice, args ...string) ([]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Dir = project.Path
	projectConfig, err := config.Load(project.Path, nil)
	if err != nil {
		return nil, err
	}
	if projectConfig, err = overrideAgents(projectConfig, choice); err != nil {
		return nil, err
	}
	if s.config.Unsafe {
		args = append([]string{"--unsafe"}, args...)
		cmd.Args = append([]string{self}, args...)
	}
	cmd.Env = envutil.EnvList(projectConfig.ChildEnvironment(envutil.EnvMap(), s.config.Unsafe, envutil.ParsePassthrough(os.Getenv("AM_PASSTHROUGH_ENV"))))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("am %s 失败：%w：%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func (s *Server) tasksAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, "GET")
		return
	}
	s.ensureFeatures()
	s.features.mu.RLock()
	out := make([]Task, 0, len(s.features.tasks))
	for _, t := range s.features.tasks {
		out = append(out, *t)
	}
	s.features.mu.RUnlock()
	s.write(w, 200, map[string]any{"tasks": out})
}
func (s *Server) taskFeatureAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		s.error(w, 404, "task_not_found", "任务不存在")
		return
	}
	s.ensureFeatures()
	s.features.mu.RLock()
	t := s.features.tasks[parts[0]]
	events := append([]taskEvent(nil), s.features.taskEvents[parts[0]]...)
	var taskCopy Task
	if t != nil {
		taskCopy = *t
	}
	s.features.mu.RUnlock()
	if t == nil {
		s.error(w, 404, "task_not_found", "任务不存在")
		return
	}
	if len(parts) > 1 && parts[1] == "events" {
		s.sse(w, r, parts[0], events)
		return
	}
	if len(parts) > 1 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if taskCopy.Status != "queued" && taskCopy.Status != "running" {
			s.error(w, http.StatusConflict, "task_not_cancellable", "任务已经结束")
			return
		}
		s.features.mu.RLock()
		cancel := s.features.taskCancel[taskCopy.ID]
		s.features.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		s.updateTask(taskCopy.ID, "cancelled", "用户已取消", taskCopy.Progress, nil)
		s.write(w, 200, map[string]any{"task_id": taskCopy.ID, "status": "cancelled"})
		return
	}
	s.write(w, 200, taskCopy)
}
func (s *Server) sse(w http.ResponseWriter, r *http.Request, taskID string, events []taskEvent) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for _, ev := range events {
		body, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, body)
	}
	fl.Flush()
	if r.URL.Query().Get("follow") != "1" {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	seen := len(events)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.features.mu.RLock()
			current := append([]taskEvent(nil), s.features.taskEvents[taskID]...)
			status := ""
			if task := s.features.tasks[taskID]; task != nil {
				status = task.Status
			}
			s.features.mu.RUnlock()
			for _, ev := range current[seen:] {
				body, _ := json.Marshal(ev)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, body)
				fl.Flush()
			}
			seen = len(current)
			if status == "succeeded" || status == "failed" || status == "cancelled" {
				return
			}
		}
	}
}

func (s *Server) reviewAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	path := filepath.Join(p.Path, "production", "review", "review.json")
	body, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		s.write(w, 200, map[string]any{"available": false, "issues": []any{}})
		return
	}
	if e != nil {
		s.error(w, 500, "review_read_failed", e.Error())
		return
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		v = map[string]any{"raw": string(body)}
	}
	s.write(w, 200, map[string]any{"available": true, "report": v})
}
func (s *Server) reviewRunAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, "POST")
		return
	}
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	t, err := s.runWriteTask(p, "project.review", "运行成片审查", func(ctx context.Context, t *Task) error {
		dir := filepath.Join(p.Path, "production", "review")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		video := findProjectVideo(p.Path)
		if video == "" {
			return errors.New("找不到可审查的成片，请先生成 candidate.mp4 或 final.mp4")
		}
		_, err := s.runStudioCLI(ctx, p, "review", video, "--out", dir)
		return err
	})
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	s.write(w, 202, map[string]any{"task_id": t.ID})
}
