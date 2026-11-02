package studio

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

// maxTreeEntries 限制 /api/tree 返回的文件数。项目里真正要看的文件远少于这个数，
// 超过说明扫进了依赖或缓存目录，截断比让网页卡死好。
const maxTreeEntries = 5000

func (s *Server) uiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/styles", s.stylesAPI)
	mux.HandleFunc("/api/styles/", s.styleExampleAPI)
	mux.HandleFunc("/api/tree", s.treeAPI)
	mux.HandleFunc("/api/raw", s.rawAPI)
	mux.HandleFunc("/api/beat", s.beatAPI)
}

type canvasOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Orientation string `json:"orientation"`
}

type styleOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// stylesAPI 列出新建项目可选的画幅与风格，示例图经 /api/styles/{风格}/{画幅}/{骨架}.png 取。
func (s *Server) stylesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	canvases := []canvasOption{}
	for _, p := range preset.All() {
		canvases = append(canvases, canvasOption{ID: p.ID, Label: p.Label, Width: p.Canvas.WidthPx, Height: p.Canvas.HeightPx, Orientation: p.Canvas.Orientation})
	}
	styles := []styleOption{}
	for _, st := range preset.AllStyles() {
		styles = append(styles, styleOption{ID: st.ID, Name: st.Name, Label: st.Label})
	}
	s.write(w, http.StatusOK, map[string]any{
		"canvases":       canvases,
		"styles":         styles,
		"custom":         styleOption{ID: preset.CustomStyle.ID, Name: preset.CustomStyle.Name, Label: "由模型按描述自主定风格"},
		"archetypes":     preset.ArchetypeIDs,
		"default_canvas": preset.Default().ID,
	})
}

func (s *Server) styleExampleAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/styles/"), "/")
	if len(parts) != 3 || !strings.HasSuffix(parts[2], ".png") {
		http.NotFound(w, r)
		return
	}
	styleID, canvasID, archetype := parts[0], parts[1], strings.TrimSuffix(parts[2], ".png")
	if _, ok := preset.StyleByID(styleID); !ok {
		http.NotFound(w, r)
		return
	}
	known := false
	for _, id := range preset.ArchetypeIDs {
		known = known || id == archetype
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	files, err := assets.Style(styleID, canvasID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := fs.ReadFile(files, "assets/style-guide/examples/"+archetype+".png")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

type treeEntry struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// treeAPI 列出项目文件，跳过的目录与快照一致，.env 不列出。
func (s *Server) treeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.method(w, http.MethodGet)
		return
	}
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	entries := []treeEntry{}
	truncated := false
	studioDir := studioRoot(p.Path)
	_ = filepath.WalkDir(p.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != p.Path && (skippedDirs[d.Name()] || path == studioDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || isEnvFile(path) {
			return nil
		}
		if len(entries) >= maxTreeEntries {
			truncated = true
			return filepath.SkipAll
		}
		info, infoErr := d.Info()
		rel, relErr := filepath.Rel(p.Path, path)
		if infoErr == nil && relErr == nil {
			entries = append(entries, treeEntry{Path: filepath.ToSlash(rel), Size: info.Size(), MTime: info.ModTime().UnixMilli()})
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	s.write(w, http.StatusOK, map[string]any{"files": entries, "truncated": truncated})
}

// rawAPI 原样返回项目里的一个文件（成片、联系表、封面、附件等），支持 Range。
func (s *Server) rawAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.method(w, http.MethodGet)
		return
	}
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	path, err := containedPath(p.Path, r.URL.Query().Get("path"))
	if err != nil || isEnvFile(path) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// beatAPI 返回卡点模式的确认状态，网页据此决定是否显示试听与确认按钮。
func (s *Server) beatAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectOr404(w)
	if !ok {
		return
	}
	exists := func(rel string) bool { return fileExists(filepath.Join(p.Path, filepath.FromSlash(rel))) }
	s.write(w, http.StatusOK, map[string]any{
		"enabled":    exists(beat.ConfigFile),
		"draft":      exists(beat.DraftFile),
		"click":      exists(beat.ClickFile),
		"confirmed":  exists(beat.GridFile),
		"click_path": beat.ClickFile,
	})
}
