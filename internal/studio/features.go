package studio

// The first Studio slice intentionally keeps write operations small and
// explicit. Every mutation goes through an operation record and a snapshot;
// this gives the UI a reviewable boundary before later render integrations are
// added.

import (
	"bufio"
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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

type Operation struct {
	ID                   string         `json:"operation_id"`
	Type                 string         `json:"type"`
	ProjectID            string         `json:"project_id"`
	SceneID              string         `json:"scene_id,omitempty"`
	Reason               string         `json:"reason"`
	Changes              map[string]any `json:"changes"`
	RequiresConfirmation bool           `json:"requires_confirmation"`
	Status               string         `json:"status"`
	CreatedAt            time.Time      `json:"created_at"`
	SnapshotID           string         `json:"snapshot_id,omitempty"`
}

type Snapshot struct {
	ID          string         `json:"snapshot_id"`
	OperationID string         `json:"operation_id"`
	CreatedAt   time.Time      `json:"created_at"`
	Files       []SnapshotFile `json:"files"`
}
type SnapshotFile struct {
	Path   string `json:"path"`
	Before string `json:"sha256_before"`
	After  string `json:"sha256_after"`
	Backup string `json:"backup,omitempty"`
	Body   []byte `json:"-"`
}

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
type conversation struct {
	ID        string              `json:"conversation_id"`
	Messages  []map[string]string `json:"messages"`
	CreatedAt time.Time           `json:"created_at"`
}

type featureState struct {
	mu            sync.RWMutex
	operations    map[string]*Operation
	snapshots     map[string]*Snapshot
	tasks         map[string]*Task
	taskEvents    map[string][]taskEvent
	conversations map[string]*conversation
	taskCancel    map[string]context.CancelFunc
	seq           uint64
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

func (s *Server) loadPersistedOperations() {
	dir := s.studioDir()
	if dir == "" {
		return
	}
	if body, err := os.ReadFile(filepath.Join(dir, "operations.json")); err == nil {
		var list []Operation
		if json.Unmarshal(body, &list) == nil {
			s.features.mu.Lock()
			for i := range list {
				op := list[i]
				s.features.operations[op.ID] = &op
			}
			s.features.mu.Unlock()
		}
	}
	snapshotDir := filepath.Join(dir, "snapshots")
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(snapshotDir, entry.Name(), "snapshot.json"))
		if readErr != nil {
			continue
		}
		var snap Snapshot
		if json.Unmarshal(body, &snap) == nil {
			s.features.mu.Lock()
			s.features.snapshots[snap.ID] = &snap
			s.features.mu.Unlock()
		}
	}
}

func (s *Server) studioDir() string {
	path := s.studioTasksPath()
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func (s *Server) persistSnapshot(snap *Snapshot) {
	dir := filepath.Join(s.studioDir(), "snapshots", snap.ID)
	if dir == "snapshots/"+snap.ID || dir == "" {
		return
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	for i := range snap.Files {
		if len(snap.Files[i].Body) == 0 {
			continue
		}
		backup := filepath.Join(dir, fmt.Sprintf("%d.bak", i))
		if os.WriteFile(backup, snap.Files[i].Body, 0o600) == nil {
			snap.Files[i].Backup = backup
		}
		snap.Files[i].Body = nil
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, "snapshot.json"), append(body, '\n'), 0o600)
	}
}

func (s *Server) persistOperation(op *Operation) {
	dir := s.studioDir()
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "operations.json")
	var list []Operation
	if body, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(body, &list)
	}
	found := false
	for i := range list {
		if list[i].ID == op.ID {
			list[i] = *op
			found = true
			break
		}
	}
	if !found {
		list = append(list, *op)
	}
	body, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, append(body, '\n'), 0o600)
	}
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
	if s.features.operations == nil {
		s.features.operations = map[string]*Operation{}
		s.features.snapshots = map[string]*Snapshot{}
		s.features.tasks = map[string]*Task{}
		s.features.taskEvents = map[string][]taskEvent{}
		s.features.conversations = map[string]*conversation{}
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

func (s *Server) featureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/scenes/", s.sceneFeatureAPI)
	mux.HandleFunc("/api/operations/preview", s.operationPreviewAPI)
	mux.HandleFunc("/api/operations/execute", s.operationExecuteAPI)
	mux.HandleFunc("/api/operations", s.operationsAPI)
	mux.HandleFunc("/api/operations/", s.operationActionAPI)
	mux.HandleFunc("/api/tasks", s.tasksAPI)
	mux.HandleFunc("/api/tasks/", s.taskFeatureAPI)
	mux.HandleFunc("/api/chat", s.chatAPI)
	mux.HandleFunc("/api/chat/", s.chatEventsAPI)
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
	t := s.addTask("scenes.render", fmt.Sprintf("渲染全部 %d 个镜头", len(scenes)))
	s.runTask(t, func(ctx context.Context) error {
		for index, scene := range scenes {
			if err := ctx.Err(); err != nil {
				return err
			}
			s.updateTask(t.ID, "running", fmt.Sprintf("正在渲染 %s（%d/%d）", scene.ID, index+1, len(scenes)), float64(index)/float64(len(scenes)), nil)
			if _, err := runStudioCLI(ctx, p, "scene", "run", scene.Directory); err != nil {
				return fmt.Errorf("%s：%w", scene.ID, err)
			}
		}
		return nil
	})
	s.write(w, http.StatusAccepted, map[string]any{"task_id": t.ID, "scene_count": len(scenes)})
}

func (s *Server) operationsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	s.ensureFeatures()
	s.features.mu.RLock()
	list := make([]Operation, 0, len(s.features.operations))
	for _, op := range s.features.operations {
		list = append(list, *op)
	}
	s.features.mu.RUnlock()
	s.write(w, 200, map[string]any{"operations": list})
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
	for i, second := range []float64{0, sc.DurationSeconds / 2, maxFloat(sc.DurationSeconds-0.05, 0)} {
		out := filepath.Join(dir, fmt.Sprintf("frame-%02d.jpg", i+1))
		cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-ss", fmt.Sprintf("%.3f", second), "-i", filepath.Join(sc.Directory, sc.Output), "-frames:v", "1", "-q:v", "3", out)
		cmd.Dir = p.Path
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("抽帧失败：%w：%s", err, strings.TrimSpace(string(output)))
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

func (s *Server) operationPreviewAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, "POST")
		return
	}
	var op Operation
	if json.NewDecoder(r.Body).Decode(&op) != nil {
		s.error(w, 400, "invalid_json", "请求不是合法 JSON")
		return
	}
	if err := s.validateOperation(&op); err != nil {
		s.error(w, 400, "invalid_operation", err.Error())
		return
	}
	currentProject, _ := s.currentProject()
	if op.ProjectID != "" && op.ProjectID != currentProject.ID {
		s.error(w, http.StatusConflict, "project_mismatch", "操作所属项目与当前工作区不一致")
		return
	}
	op.ProjectID = currentProject.ID
	s.ensureFeatures()
	s.features.mu.Lock()
	op.ID = nextID("op", &s.features.seq)
	op.Status = "preview"
	op.RequiresConfirmation = true
	op.CreatedAt = time.Now().UTC()
	s.features.operations[op.ID] = &op
	s.features.mu.Unlock()
	s.persistOperation(&op)
	s.write(w, 200, map[string]any{"operation": op, "diff": s.operationDiff(op)})
}
func (s *Server) operationExecuteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, "POST")
		return
	}
	var req struct {
		OperationID string `json:"operation_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.OperationID == "" {
		s.error(w, 400, "invalid_request", "缺少 operation_id")
		return
	}
	s.ensureFeatures()
	s.features.mu.Lock()
	op := s.features.operations[req.OperationID]
	var opCopy Operation
	if op != nil {
		opCopy = *op
		opCopy.Changes = cloneMap(op.Changes)
	}
	s.features.mu.Unlock()
	if op == nil {
		s.error(w, 404, "operation_not_found", "操作不存在")
		return
	}
	currentProject, _ := s.currentProject()
	if opCopy.ProjectID != currentProject.ID {
		s.error(w, http.StatusConflict, "project_mismatch", "操作所属项目与当前工作区不一致")
		return
	}
	if !opCopy.RequiresConfirmation {
		s.error(w, 409, "confirmation_required", "写操作必须确认")
		return
	}
	snap, err := s.snapshotFor(&opCopy)
	if err != nil {
		s.error(w, 500, "snapshot_failed", err.Error())
		return
	}
	t := s.addTask("operation.execute", "执行 "+opCopy.Type)
	s.features.mu.Lock()
	op.SnapshotID = snap.ID
	op.Status = "queued"
	queuedCopy := *op
	s.features.mu.Unlock()
	s.persistOperation(&queuedCopy)
	s.runTask(t, func(ctx context.Context) error {
		err := s.applyOperation(ctx, &opCopy)
		if err != nil {
			return err
		}
		s.features.mu.Lock()
		op.Status = "succeeded"
		succeededCopy := *op
		s.features.mu.Unlock()
		s.persistOperation(&succeededCopy)
		return nil
	})
	s.write(w, 202, map[string]any{"operation_id": op.ID, "task_id": t.ID, "snapshot_id": snap.ID})
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (s *Server) operationActionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/operations/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "undo" {
		s.error(w, 404, "not_found", "操作接口不存在")
		return
	}
	s.ensureFeatures()
	s.features.mu.RLock()
	op := s.features.operations[parts[0]]
	snap := (*Snapshot)(nil)
	if op != nil {
		snap = s.features.snapshots[op.SnapshotID]
	}
	s.features.mu.RUnlock()
	if op == nil || snap == nil {
		s.error(w, 404, "operation_not_found", "操作或快照不存在")
		return
	}
	if err := s.restoreSnapshot(snap); err != nil {
		s.error(w, 500, "undo_failed", err.Error())
		return
	}
	s.features.mu.Lock()
	op.Status = "undone"
	undoneCopy := *op
	s.features.mu.Unlock()
	s.persistOperation(&undoneCopy)
	s.write(w, 200, map[string]any{"ok": true, "operation_id": op.ID})
}

func (s *Server) validateOperation(op *Operation) error {
	if op.Type == "" {
		return errors.New("缺少操作类型")
	}
	allowed := map[string]bool{"project.inspect": true, "scene.inspect": true, "scene.capture_frames": true, "scene.update_caption": true, "scene.update_timing_plan": true, "scene.update_character_motion": true, "scene.update_camera": true, "scene.update_style_parameters": true, "scene.render": true, "scene.validate": true, "project.concat": true, "project.review": true, "publish.check": true}
	if !allowed[op.Type] {
		return fmt.Errorf("不支持的操作类型：%s", op.Type)
	}
	if strings.HasPrefix(op.Type, "scene.") && op.SceneID == "" {
		return errors.New("镜头操作需要 scene_id")
	}
	if _, err := s.currentProject(); err != nil {
		return err
	}
	return nil
}
func (s *Server) operationDiff(op Operation) map[string]any {
	return map[string]any{"type": op.Type, "scene_id": op.SceneID, "changes": op.Changes, "impact": "相关镜头将标记为 stale，并在确认后执行任务"}
}
func (s *Server) snapshotFor(op *Operation) (*Snapshot, error) {
	p, err := s.currentProject()
	if err != nil {
		return nil, err
	}
	paths := []string{}
	if op.SceneID != "" {
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == op.SceneID {
				paths = append(paths, filepath.Join(sc.Directory, "scene.json"))
			}
		}
	} else if op.Type == "scene.update_timing_plan" {
		paths = append(paths, filepath.Join(p.Path, "production", "scene-plan.json"))
	} else if op.Type == "script.revise" {
		paths = append(paths, filepath.Join(p.Path, "SCRIPT.md"), filepath.Join(p.Path, "script.md"))
	}
	if op.Type == "scene.update_character_motion" || op.Type == "scene.update_camera" || op.Type == "scene.update_style_parameters" {
		paths = append(paths, filepath.Join(p.Path, "production", "studio", op.ID+".json"))
	}
	snap := &Snapshot{ID: projectID(fmt.Sprintf("%s-%d", op.ID, time.Now().UnixNano())), OperationID: op.ID, CreatedAt: time.Now().UTC()}
	for _, path := range paths {
		body, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			snap.Files = append(snap.Files, SnapshotFile{Path: path})
			continue
		}
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(body)
		snap.Files = append(snap.Files, SnapshotFile{Path: path, Before: hex.EncodeToString(sum[:]), Body: body})
	}
	s.ensureFeatures()
	s.features.mu.Lock()
	s.features.snapshots[snap.ID] = snap
	s.features.mu.Unlock()
	s.persistSnapshot(snap)
	return snap, nil
}
func (s *Server) applyOperation(ctx context.Context, op *Operation) error {
	p, err := s.currentProject()
	if err != nil {
		return err
	}
	if op.Type == "scene.update_caption" {
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID != op.SceneID {
				continue
			}
			path := filepath.Join(sc.Directory, "scene.json")
			body, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			var v map[string]any
			if e = json.Unmarshal(body, &v); e != nil {
				return e
			}
			if text, ok := op.Changes["text"].(string); ok {
				v["text"] = text
			} else {
				return errors.New("scene.update_caption 需要 changes.text")
			}
			next, e := json.MarshalIndent(v, "", "  ")
			if e != nil {
				return e
			}
			return os.WriteFile(path, append(next, '\n'), 0o644)
		}
	}
	if op.Type == "scene.update_timing_plan" {
		path := filepath.Join(p.Path, "production", "scene-plan.json")
		body, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var value any
		if e = json.Unmarshal(body, &value); e != nil {
			return e
		}
		updated, ok := op.Changes["plan"]
		if !ok {
			return errors.New("scene.update_timing_plan 需要 changes.plan")
		}
		next, e := json.MarshalIndent(updated, "", "  ")
		if e != nil {
			return e
		}
		if err := os.WriteFile(path, append(next, '\n'), 0o644); err != nil {
			return err
		}
		for _, sc := range discoverScenes(p.Path) {
			scenePath := filepath.Join(sc.Directory, "scene.json")
			now := time.Now()
			_ = os.Chtimes(scenePath, now, now)
		}
		return nil
	}
	if op.Type == "scene.update_character_motion" || op.Type == "scene.update_camera" || op.Type == "scene.update_style_parameters" {
		found := false
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == op.SceneID {
				found = true
				break
			}
		}
		if !found {
			return errors.New("镜头不存在")
		}
		dir := filepath.Join(p.Path, "production", "studio")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		key := map[string]string{"scene.update_character_motion": "character_motion", "scene.update_camera": "camera", "scene.update_style_parameters": "style_parameters"}[op.Type]
		next, e := json.MarshalIndent(map[string]any{"scene_id": op.SceneID, "type": key, "changes": op.Changes, "operation_id": op.ID}, "", "  ")
		if e != nil {
			return e
		}
		if err := os.WriteFile(filepath.Join(dir, op.ID+".json"), append(next, '\n'), 0o644); err != nil {
			return err
		}
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == op.SceneID {
				scenePath := filepath.Join(sc.Directory, "scene.json")
				now := time.Now()
				_ = os.Chtimes(scenePath, now, now)
				break
			}
		}
		return nil
	}
	if op.Type == "script.revise" {
		target := filepath.Join(p.Path, "SCRIPT.md")
		if _, e := os.Stat(target); os.IsNotExist(e) {
			target = filepath.Join(p.Path, "script.md")
		}
		text, ok := op.Changes["text"].(string)
		if !ok {
			return errors.New("script.revise 需要 changes.text")
		}
		if err := os.WriteFile(target, []byte(text), 0o644); err != nil {
			return err
		}
		for _, sc := range discoverScenes(p.Path) {
			scenePath := filepath.Join(sc.Directory, "scene.json")
			now := time.Now()
			_ = os.Chtimes(scenePath, now, now)
		}
		return nil
	}
	if op.Type == "scene.render" {
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID != op.SceneID {
				continue
			}
			executable, e := os.Executable()
			if e != nil {
				return e
			}
			cmd := exec.CommandContext(ctx, executable, "scene", "run", sc.Directory)
			cmd.Dir = p.Path
			cmd.Env = append(os.Environ(), "AM_STUDIO_TASK=1")
			if output, e := cmd.CombinedOutput(); e != nil {
				return fmt.Errorf("镜头渲染失败：%w：%s", e, strings.TrimSpace(string(output)))
			}
			return nil
		}
		return errors.New("镜头不存在")
	}
	if op.Type == "scene.capture_frames" {
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == op.SceneID {
				return s.captureSceneFrames(ctx, p, sc)
			}
		}
		return errors.New("镜头不存在")
	}
	if op.Type == "scene.validate" {
		for _, sc := range discoverScenes(p.Path) {
			if sc.ID == op.SceneID {
				if !sc.Exists {
					return errors.New("镜头产物不存在")
				}
				_, err := runStudioCLI(ctx, p, "scene", "lint", sc.Directory)
				return err
			}
		}
		return errors.New("镜头不存在")
	}
	if op.Type == "publish.check" {
		publish := filepath.Join(p.Path, "publish.md")
		if !fileExists(publish) {
			return errors.New("找不到 publish.md")
		}
		_, err := runStudioCLI(ctx, p, "validate", "publish", publish, "--project-root", p.Path)
		return err
	}
	if op.Type == "project.review" {
		video := findProjectVideo(p.Path)
		if video == "" {
			return errors.New("找不到可审查的成片")
		}
		_, err := runStudioCLI(ctx, p, "review", video, "--out", filepath.Join(p.Path, "production", "review"))
		return err
	}
	if op.Type == "project.concat" {
		out := filepath.Join(p.Path, "production", "silent-master.mp4")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		_, err := runStudioCLI(ctx, p, "concat", filepath.Join(p.Path, "scenes"), "--out", out)
		return err
	}
	return nil
}

func findProjectVideo(root string) string {
	for _, candidate := range []string{filepath.Join(root, "production", "audio", "candidate.mp4"), filepath.Join(root, "final.mp4"), filepath.Join(root, "production", "final.mp4")} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func runStudioCLI(ctx context.Context, project Project, args ...string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = project.Path
	projectConfig, err := config.Load(project.Path, nil)
	if err != nil {
		return nil, err
	}
	cmd.Env = envutil.EnvList(projectConfig.ChildEnvironment(envutil.EnvMap(), false, nil))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("am %s 失败：%w：%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func (s *Server) restoreSnapshot(snap *Snapshot) error {
	for _, f := range snap.Files {
		if f.Before == "" {
			if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		body := f.Body
		if len(body) == 0 && f.Backup != "" {
			var err error
			body, err = os.ReadFile(f.Backup)
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(f.Path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
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

func (s *Server) chatAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, "POST")
		return
	}
	if _, err := s.currentProject(); err != nil {
		s.error(w, http.StatusNotFound, "no_project", err.Error())
		return
	}
	var req struct {
		ConversationID string         `json:"conversation_id"`
		Message        string         `json:"message"`
		SceneID        string         `json:"scene_id"`
		AtFrame        *int           `json:"at_frame"`
		Region         map[string]any `json:"region"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Message) == "" {
		s.error(w, 400, "invalid_request", "消息不能为空")
		return
	}
	req.Message = redactSecrets(req.Message)
	s.ensureFeatures()
	s.features.mu.Lock()
	id := req.ConversationID
	if id == "" {
		id = nextID("conversation", &s.features.seq)
		s.features.conversations[id] = &conversation{ID: id, CreatedAt: time.Now().UTC()}
	}
	c := s.features.conversations[id]
	if c == nil {
		s.features.mu.Unlock()
		s.error(w, 404, "conversation_not_found", "对话不存在")
		return
	}
	c.Messages = append(c.Messages, map[string]string{"role": "user", "content": req.Message})
	reply := "我已收到这个问题。请先确认镜头、时间点和想要的结果；我会在确认后生成结构化操作预览。"
	if req.SceneID != "" {
		reply = "我会围绕 " + req.SceneID + " 分析你提供的上下文，并先给出可撤销的修改方案。"
	}
	toolName := os.Getenv("AM_STUDIO_AI_TOOL")
	if toolName == "" {
		toolName = os.Getenv("ORCHESTRATOR")
	}
	if toolName == "" {
		if project, projectErr := s.currentProject(); projectErr == nil {
			if projectConfig, configErr := config.Load(project.Path, nil); configErr == nil {
				toolName = projectConfig.Orchestrator
			}
		}
	}
	aiTask := ""
	if toolName != "" {
		if _, lookErr := exec.LookPath(toolName); lookErr == nil {
			aiTask = nextID("chat", &s.features.seq)
			reply = "AI 已收到请求，正在读取当前项目上下文并生成回复。"
		}
	}
	c.Messages = append(c.Messages, map[string]string{"role": "assistant", "content": reply})
	out := *c
	s.features.mu.Unlock()
	if aiTask != "" {
		t := s.addTask("ai.chat", "调用 "+toolName+" 分析当前上下文")
		s.runTask(t, func(ctx context.Context) error {
			return s.runAIChat(ctx, id, toolName, req.SceneID, req.Message, req.AtFrame, req.Region)
		})
	}
	var operationPreview any
	if req.SceneID != "" {
		if match := captionChangePattern.FindStringSubmatch(req.Message); len(match) == 2 {
			s.ensureFeatures()
			s.features.mu.Lock()
			op := Operation{ID: nextID("op", &s.features.seq), Type: "scene.update_caption", ProjectID: stateProjectID(s), SceneID: req.SceneID, Reason: "根据 AI 对话生成字幕修改预览", Changes: map[string]any{"text": strings.TrimSpace(match[1])}, RequiresConfirmation: true, Status: "preview", CreatedAt: time.Now().UTC()}
			s.features.operations[op.ID] = &op
			s.features.mu.Unlock()
			operationPreview = map[string]any{"operation": op, "diff": s.operationDiff(op)}
		}
	}
	s.write(w, 200, map[string]any{"conversation": out, "message": reply, "operation_preview": operationPreview})
}

var captionChangePattern = regexp.MustCompile(`字幕[:：]\s*(.+)`)

func stateProjectID(s *Server) string { s.mu.RLock(); defer s.mu.RUnlock(); return s.current }

func (s *Server) runAIChat(ctx context.Context, conversationID, toolName, sceneID, message string, atFrame *int, region map[string]any) error {
	p, err := s.currentProject()
	if err != nil {
		return err
	}
	contextText := fmt.Sprintf("你正在 ArticleToMotion Studio 中协助用户。项目标识是 %s。当前镜头是 %s。用户请求：%s。选中帧：%v。选中区域：%v。请只解释问题并提出结构化、可确认的修改建议，不要直接修改文件。", p.ID, sceneID, message, atFrame, region)
	argv, stdin, err := tools.OrchestratorInvocation(toolName, p.Path, contextText, false)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = p.Path
	projectConfig, configErr := config.Load(p.Path, nil)
	if configErr != nil {
		return configErr
	}
	cmd.Env = envutil.EnvList(projectConfig.ChildEnvironment(envutil.EnvMap(), false, nil))
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("AI CLI 执行失败：%w", err)
	}
	reply := redactSecrets(strings.TrimSpace(output.String()))
	if match := aiOperationPattern.FindStringSubmatch(reply); len(match) == 2 {
		var proposed Operation
		if json.Unmarshal([]byte(match[1]), &proposed) == nil {
			if proposed.Type != "" && s.validateOperation(&proposed) == nil {
				proposed.ProjectID = p.ID
				proposed.RequiresConfirmation = true
				proposed.Status = "preview"
				proposed.CreatedAt = time.Now().UTC()
				s.features.mu.Lock()
				proposed.ID = nextID("op", &s.features.seq)
				s.features.operations[proposed.ID] = &proposed
				s.features.mu.Unlock()
				s.persistOperation(&proposed)
				reply += "\n\n已生成待确认操作：" + proposed.ID
			}
		}
	}
	if strings.Contains(reply, "{") {
		var messages []string
		scanner := bufio.NewScanner(strings.NewReader(reply))
		for scanner.Scan() {
			messages = append(messages, tools.ExtractUserMessages(toolName, scanner.Text())...)
		}
		if len(messages) > 0 {
			reply = strings.Join(messages, "\n")
		}
	}
	if reply == "" {
		reply = "AI 已完成分析，但没有返回可显示的文字。"
	}
	s.features.mu.Lock()
	if c := s.features.conversations[conversationID]; c != nil {
		c.Messages = append(c.Messages, map[string]string{"role": "assistant", "content": reply})
	}
	s.features.mu.Unlock()
	return nil
}

var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*[^\s,;]+`)
var aiOperationPattern = regexp.MustCompile(`(?s)\[\[AM_OPERATION\]\]\s*(\{.*?\})`)

func redactSecrets(text string) string { return secretPattern.ReplaceAllString(text, "$1=[REDACTED]") }
func (s *Server) chatEventsAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/chat/")
	s.ensureFeatures()
	s.features.mu.RLock()
	c := s.features.conversations[id]
	var copyConversation conversation
	if c != nil {
		copyConversation = *c
		copyConversation.Messages = append([]map[string]string(nil), c.Messages...)
	}
	s.features.mu.RUnlock()
	if c == nil {
		s.error(w, 404, "conversation_not_found", "对话不存在")
		return
	}
	if r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.chatSSE(w, r, copyConversation)
		return
	}
	s.write(w, 200, copyConversation)
}

func (s *Server) chatSSE(w http.ResponseWriter, r *http.Request, conversation conversation) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for _, message := range conversation.Messages {
		body, _ := json.Marshal(map[string]any{"conversation_id": conversation.ID, "message": message})
		fmt.Fprintf(w, "event: chat.message\ndata: %s\n\n", body)
	}
	flusher.Flush()
	if r.URL.Query().Get("follow") != "1" {
		return
	}
	seen := len(conversation.Messages)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.features.mu.RLock()
			current := s.features.conversations[conversation.ID]
			running := false
			if current != nil {
				for _, task := range s.features.tasks {
					if task.Type == "ai.chat" && (task.Status == "queued" || task.Status == "running") {
						running = true
						break
					}
				}
			}
			var messages []map[string]string
			if current != nil {
				messages = append(messages, current.Messages...)
			}
			s.features.mu.RUnlock()
			for _, message := range messages[seen:] {
				body, _ := json.Marshal(map[string]any{"conversation_id": conversation.ID, "message": message})
				fmt.Fprintf(w, "event: chat.message\ndata: %s\n\n", body)
				flusher.Flush()
			}
			seen = len(messages)
			if !running {
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
	t := s.addTask("project.review", "运行成片审查")
	s.runTask(t, func(ctx context.Context) error {
		dir := filepath.Join(p.Path, "production", "review")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		video := ""
		for _, candidate := range []string{filepath.Join(p.Path, "production", "audio", "candidate.mp4"), filepath.Join(p.Path, "final.mp4"), filepath.Join(p.Path, "production", "final.mp4")} {
			if fileExists(candidate) {
				video = candidate
				break
			}
		}
		if video == "" {
			return errors.New("找不到可审查的成片，请先生成 candidate.mp4 或 final.mp4")
		}
		executable, e := os.Executable()
		if e != nil {
			return e
		}
		cmd := exec.CommandContext(ctx, executable, "review", video, "--out", dir)
		cmd.Dir = p.Path
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			return fmt.Errorf("成片审查失败：%w：%s", runErr, strings.TrimSpace(string(output)))
		}
		return nil
	})
	s.write(w, 202, map[string]any{"task_id": t.ID})
}
