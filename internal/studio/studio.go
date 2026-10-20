package studio

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/project"
)

//go:embed web/*
var webFiles embed.FS

type Config struct {
	ListenAddr  string
	ProjectPath string
	ConfigPath  string
}

type Project struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	LastOpenedAt string `json:"last_opened_at,omitempty"`
}

type registry struct {
	Schema   int       `json:"schema"`
	Projects []Project `json:"projects"`
}

type Server struct {
	config   Config
	mu       sync.RWMutex
	projects registry
	current  string
	started  time.Time
	features featureState
}

func New(cfg Config) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:4317"
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = defaultConfigPath()
	}
	s := &Server{config: cfg, started: time.Now()}
	if err := s.loadRegistry(); err != nil {
		return nil, err
	}
	if cfg.ProjectPath != "" {
		p, err := s.openProject(cfg.ProjectPath)
		if err != nil {
			return nil, err
		}
		s.current = p.ID
	}
	s.ensureFeatures()
	s.loadPersistedTasks()
	s.loadPersistedOperations()
	return s, nil
}

func defaultConfigPath() string {
	if p := os.Getenv("AM_STUDIO_CONFIG"); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "am", "studio", "projects.json")
	}
	return filepath.Join(os.TempDir(), "am-studio-projects.json")
}

func (s *Server) loadRegistry() error {
	s.projects.Schema = 1
	body, err := os.ReadFile(s.config.ConfigPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 Studio 项目登记失败：%w", err)
	}
	if err := json.Unmarshal(body, &s.projects); err != nil {
		return fmt.Errorf("Studio 项目登记损坏：%w", err)
	}
	if s.projects.Schema == 0 {
		s.projects.Schema = 1
	}
	return nil
}

func (s *Server) saveRegistry() error {
	if err := os.MkdirAll(filepath.Dir(s.config.ConfigPath), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.config.ConfigPath), ".projects-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Chmod(0o600)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, s.config.ConfigPath)
}

func canonicalProjectPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("项目路径不能为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("项目目录不可用：%w", err)
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", errors.New("项目路径必须是目录")
	}
	return clean, nil
}

func projectID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "project-" + hex.EncodeToString(sum[:8])
}

func (s *Server) openProject(path string) (Project, error) {
	path, err := canonicalProjectPath(path)
	if err != nil {
		return Project{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.projects.Projects {
		if s.projects.Projects[i].Path == path {
			s.projects.Projects[i].LastOpenedAt = time.Now().UTC().Format(time.RFC3339)
			_ = s.saveRegistry()
			return s.projects.Projects[i], nil
		}
	}
	p := Project{ID: projectID(path), Path: path, Name: filepath.Base(path), LastOpenedAt: time.Now().UTC().Format(time.RFC3339)}
	s.projects.Projects = append(s.projects.Projects, p)
	if err := s.saveRegistry(); err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/projects", s.projectsAPI)
	mux.HandleFunc("/api/projects/open", s.openAPI)
	mux.HandleFunc("/api/projects/create", s.createAPI)
	mux.HandleFunc("/api/directories", s.directoriesAPI)
	mux.HandleFunc("/api/projects/recent/remove", s.removeAPI)
	mux.HandleFunc("/api/workspaces/current", s.currentAPI)
	mux.HandleFunc("/api/workspaces/switch", s.switchAPI)
	mux.HandleFunc("/api/state", s.stateAPI)
	mux.HandleFunc("/api/scenes", s.scenesAPI)
	mux.HandleFunc("/api/script", s.scriptAPI)
	mux.HandleFunc("/api/assets", s.assetsAPI)
	mux.HandleFunc("/api/publish", s.publishAPI)
	s.featureRoutes(mux)
	mux.HandleFunc("/", s.static)
	return withJSONErrors(mux)
}

type directoryEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsProject bool   `json:"is_project"`
}

func studioBrowseRoot() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Clean(home)
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Clean(cwd)
	}
	return string(filepath.Separator)
}

func (s *Server) directoriesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	root := studioBrowseRoot()
	requested := r.URL.Query().Get("path")
	path := root
	if requested != "" {
		abs, err := filepath.Abs(requested)
		if err != nil {
			s.error(w, http.StatusBadRequest, "invalid_directory", "目录路径无效")
			return
		}
		path = filepath.Clean(abs)
	}
	withinRoot, err := filepath.Rel(root, path)
	if err != nil || withinRoot == ".." || strings.HasPrefix(withinRoot, ".."+string(filepath.Separator)) {
		s.error(w, http.StatusForbidden, "directory_outside_root", "只能浏览服务器用户目录下的文件夹")
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		s.error(w, http.StatusBadRequest, "directory_unavailable", "目录不存在或不可读取")
		return
	}
	items, err := os.ReadDir(path)
	if err != nil {
		s.error(w, http.StatusForbidden, "directory_unreadable", "没有权限读取这个目录")
		return
	}
	entries := make([]directoryEntry, 0)
	for _, item := range items {
		if !item.IsDir() || strings.HasPrefix(item.Name(), ".") {
			continue
		}
		child := filepath.Join(path, item.Name())
		entries = append(entries, directoryEntry{Name: item.Name(), Path: child, IsProject: isStudioProjectDir(child)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsProject != entries[j].IsProject {
			return entries[i].IsProject
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	parent := ""
	if path != root {
		parent = filepath.Dir(path)
	}
	s.write(w, http.StatusOK, map[string]any{"path": path, "parent": parent, "root": root, "entries": entries})
}

func isStudioProjectDir(path string) bool {
	if info, err := os.Stat(filepath.Join(path, "frame.md")); err == nil && !info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(path, "scenes")); err == nil && info.IsDir() {
		return true
	}
	return false
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return err
	}
	defer ln.Close()
	h := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = h.Shutdown(context.Background()) }()
	err = h.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.write(w, http.StatusOK, map[string]any{"ok": true, "service": "am-studio", "started_at": s.started.UTC()})
}
func (s *Server) projectsAPI(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.write(w, http.StatusOK, map[string]any{"projects": s.projects.Projects, "current_project_id": s.current})
}

type pathRequest struct {
	Path string `json:"path"`
}

type createProjectRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
	Canvas string `json:"canvas"`
	Style  string `json:"style"`
}

func (s *Server) createAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	var req createProjectRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Name) == "" {
		s.error(w, http.StatusBadRequest, "invalid_request", "请填写项目名称")
		return
	}
	if req.Canvas == "" {
		req.Canvas = preset.Default().ID
	}
	if req.Style == "" {
		// 工作台与 CLI 保持一致：内置风格是可选风格包，默认进入模型自主
		// 定风格流程，避免新项目被清晰系统蓝图预先定向。
		req.Style = preset.CustomStyle.ID
	}
	parent, err := filepath.Abs(req.Parent)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_directory", "父目录无效")
		return
	}
	if !isWithinStudioBrowseRoot(parent) {
		s.error(w, http.StatusForbidden, "directory_outside_root", "只能在服务器用户目录下创建项目")
		return
	}
	if info, statErr := os.Stat(parent); statErr != nil || !info.IsDir() {
		s.error(w, http.StatusBadRequest, "directory_unavailable", "父目录不存在")
		return
	}
	name := filepath.Base(filepath.Clean(req.Name))
	if name == "." || name == ".." || name != req.Name || strings.ContainsAny(name, `/\\`) {
		s.error(w, http.StatusBadRequest, "invalid_project_name", "项目名称只能是一个文件夹名称")
		return
	}
	target := filepath.Join(parent, name)
	if _, statErr := os.Stat(target); statErr == nil {
		s.error(w, http.StatusConflict, "project_exists", "这个目录已经存在")
		return
	}
	shared, err := assets.Shared()
	if err != nil {
		s.error(w, http.StatusInternalServerError, "template_unavailable", err.Error())
		return
	}
	presetFiles, err := assets.Preset(req.Canvas)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_canvas", err.Error())
		return
	}
	styleMeta := preset.CustomStyle
	if req.Style != preset.CustomStyle.ID {
		var ok bool
		styleMeta, ok = preset.StyleByID(req.Style)
		if !ok {
			s.error(w, http.StatusBadRequest, "invalid_style", "未知风格")
			return
		}
	}
	styleFiles, err := assets.Style(req.Style, req.Canvas)
	if err != nil {
		s.error(w, http.StatusBadRequest, "invalid_style", err.Error())
		return
	}
	extra, err := assets.StyleFonts(styleMeta)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "template_unavailable", err.Error())
		return
	}
	if req.Style == preset.CustomStyle.ID {
		extra[preset.CustomStyleBriefPath] = []byte("# 风格描述\n\n" +
			"> 由工作台的自主定风格流程写入。制作开始前按这里的描述生成 frame.md、" +
			"`" + preset.CustomStyle.GuideDoc() + "` 与四张示例图。\n\n" +
			preset.AutomaticStyleBrief + "\n")
	}
	if _, err := project.InitializeWithFiles(target, extra, shared, presetFiles, styleFiles); err != nil {
		s.error(w, http.StatusBadRequest, "project_create_failed", err.Error())
		return
	}
	p, err := s.openProject(target)
	if err != nil {
		s.error(w, http.StatusInternalServerError, "project_register_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.current = p.ID
	_ = s.saveRegistry()
	s.mu.Unlock()
	s.write(w, http.StatusCreated, map[string]any{"project": p})
}

func isWithinStudioBrowseRoot(path string) bool {
	root := studioBrowseRoot()
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	var req pathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, 400, "invalid_json", "请求不是合法 JSON")
		return
	}
	p, err := s.openProject(req.Path)
	if err != nil {
		s.error(w, 400, "project_unavailable", err.Error())
		return
	}
	s.mu.Lock()
	s.current = p.ID
	_ = s.saveRegistry()
	s.mu.Unlock()
	s.write(w, 200, map[string]any{"project": p})
}
func (s *Server) removeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	var req struct {
		ID string `json:"project_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.ID == "" {
		s.error(w, 400, "invalid_request", "缺少 project_id")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.projects.Projects[:0]
	for _, p := range s.projects.Projects {
		if p.ID != req.ID {
			out = append(out, p)
		}
	}
	s.projects.Projects = out
	if s.current == req.ID {
		s.current = ""
	}
	if err := s.saveRegistry(); err != nil {
		s.error(w, 500, "registry_write_failed", err.Error())
		return
	}
	s.write(w, 200, map[string]any{"ok": true})
}
func (s *Server) currentAPI(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.projects.Projects {
		if p.ID == s.current {
			s.write(w, 200, map[string]any{"project": p})
			return
		}
	}
	s.write(w, 200, map[string]any{"project": nil})
}
func (s *Server) switchAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.method(w, http.MethodPost)
		return
	}
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		s.error(w, 400, "invalid_json", "请求不是合法 JSON")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects.Projects {
		if p.ID == req.ProjectID {
			s.current = p.ID
			_ = s.saveRegistry()
			s.write(w, 200, map[string]any{"project": p})
			return
		}
	}
	s.error(w, 404, "project_not_found", "项目未登记")
}

func (s *Server) currentProject() (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.projects.Projects {
		if p.ID == s.current {
			info, err := os.Stat(p.Path)
			if err != nil || !info.IsDir() {
				return Project{}, errors.New("当前项目目录不存在或不可访问")
			}
			return p, nil
		}
	}
	return Project{}, errors.New("尚未打开项目")
}
func (s *Server) stateAPI(w http.ResponseWriter, _ *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	s.write(w, 200, inspectProject(p))
}
func (s *Server) scenesAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.currentProject()
	if err != nil {
		s.error(w, 404, "no_project", err.Error())
		return
	}
	if r.URL.Path != "/api/scenes" {
		id := strings.TrimPrefix(r.URL.Path, "/api/scenes/")
		s.sceneAPI(w, p, id)
		return
	}
	s.write(w, 200, map[string]any{"scenes": discoverScenes(p.Path)})
}
func (s *Server) sceneAPI(w http.ResponseWriter, p Project, id string) {
	for _, scene := range discoverScenes(p.Path) {
		if scene.ID == id {
			body, err := os.ReadFile(filepath.Join(scene.Directory, "scene.json"))
			var data map[string]any
			if err == nil {
				_ = json.Unmarshal(body, &data)
			}
			s.write(w, 200, map[string]any{"id": scene.ID, "directory": scene.Directory, "duration_seconds": scene.DurationSeconds, "output": scene.Output, "exists": scene.Exists, "stale": scene.Stale, "data": data})
			return
		}
	}
	s.error(w, 404, "scene_not_found", "镜头不存在")
}

type SceneSummary struct {
	ID              string  `json:"id"`
	Directory       string  `json:"directory"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	Output          string  `json:"output,omitempty"`
	Exists          bool    `json:"exists"`
	Stale           bool    `json:"stale"`
}

func discoverScenes(root string) []SceneSummary {
	var out []SceneSummary
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "scene.json" {
			return nil
		}
		body, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		var v struct {
			ID       string  `json:"id"`
			Duration float64 `json:"duration_seconds"`
			Output   string  `json:"output"`
		}
		if json.Unmarshal(body, &v) != nil || v.ID == "" {
			return nil
		}
		output := filepath.Join(filepath.Dir(path), v.Output)
		rel, relErr := filepath.Rel(filepath.Dir(path), output)
		contained := relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(v.Output)
		exists := contained && fileExists(output)
		stale := false
		if exists {
			sceneInfo, sceneErr := os.Stat(path)
			outputInfo, outputErr := os.Stat(output)
			stale = sceneErr == nil && outputErr == nil && sceneInfo.ModTime().After(outputInfo.ModTime())
		}
		out = append(out, SceneSummary{ID: v.ID, Directory: filepath.Dir(path), DurationSeconds: v.Duration, Output: v.Output, Exists: exists, Stale: stale})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
func inspectProject(p Project) map[string]any {
	scenes := discoverScenes(p.Path)
	done := 0
	stale := 0
	for _, sc := range scenes {
		if sc.Exists {
			done++
		}
		if sc.Stale {
			stale++
		}
	}
	meta := projectMetadata(p.Path)
	return map[string]any{"project": p, "stage": stageFor(p.Path), "metadata": meta, "media": map[string]any{"audio": fileExists(filepath.Join(p.Path, "production", "audio", "candidate.mp4")), "final": fileExists(filepath.Join(p.Path, "final.mp4")) || fileExists(filepath.Join(p.Path, "production", "final.mp4")), "publish": fileExists(filepath.Join(p.Path, "publish.md"))}, "scenes": map[string]any{"total": len(scenes), "completed": done, "stale": stale}, "issues": projectIssues(p.Path)}
}

func projectIssues(root string) any {
	body, err := os.ReadFile(filepath.Join(root, "production", "review", "review.json"))
	if err != nil {
		return []any{}
	}
	var report map[string]any
	if json.Unmarshal(body, &report) != nil {
		return []any{}
	}
	if issues, ok := report["issues"]; ok {
		return issues
	}
	return []any{}
}

func projectMetadata(root string) map[string]string {
	result := map[string]string{}
	body, err := os.ReadFile(filepath.Join(root, "frame.md"))
	if err != nil {
		return result
	}
	// frame.md uses a small YAML front matter block. Keep this reader deliberately
	// permissive: Studio is an observer and should still show partial state when a
	// project is mid-edit or has an invalid document.
	inFrontMatter := false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontMatter {
				inFrontMatter = true
				continue
			}
			break
		}
		if !inFrontMatter {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		switch key {
		case "canvas", "canvas_id", "style_id", "style_name", "narration", "mode":
			if value != "" {
				result[key] = value
			}
		}
	}
	return result
}
func stageFor(root string) string {
	if fileExists(filepath.Join(root, "production", "run-report.json")) {
		return "review"
	}
	if fileExists(filepath.Join(root, "script.md")) || fileExists(filepath.Join(root, "transcription.srt")) {
		return "render"
	}
	return "setup"
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.error(w, http.StatusNotFound, "not_found", "Studio API 路径不存在")
		return
	}
	name := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	body, err := fs.ReadFile(webFiles, "web/"+name)
	if err != nil {
		body, err = fs.ReadFile(webFiles, "web/index.html")
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Write(body)
}
func (s *Server) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) error(w http.ResponseWriter, status int, code, message string) {
	s.write(w, status, map[string]any{"code": code, "message": message, "details": []any{}, "retryable": false})
}
func (s *Server) method(w http.ResponseWriter, want string) {
	w.Header().Set("Allow", want)
	s.error(w, 405, "method_not_allowed", "只允许 "+want)
}
func withJSONErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
