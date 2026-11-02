package studio

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// 每轮对话前后各扫一遍项目，记下每个文件的状态；两次之差就是这一轮的改动。
//
// Studio 自己不改项目文件，所以「回退这一轮」只能靠快照：文本文件的内容按 sha256
// 存进 production/studio/blobs/，相同内容只存一份；媒体与超大文件只记大小和修改
// 时间，能列出变化，但不回退——一段渲染产物几十兆，每轮存一份不现实，重渲染即可。

const (
	// maxSnapshotBytes 以上的文件只记 stat，不存内容。
	maxSnapshotBytes = 2 << 20
	statPrefix       = "stat:"
	shaPrefix        = "sha256:"
)

// skippedDirs 是快照不进入的目录。production/studio 是 Studio 自己的状态，
// attempts 与 .studio-frames 是渲染与抽帧的副产物，其余是依赖与工具缓存。
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "attempts": true, ".studio-frames": true,
	".agents": true, "__pycache__": true, ".venv": true, ".cache": true,
}

// mediaExtensions 直接按 stat 处理，不读内容。
var mediaExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".webm": true, ".mkv": true, ".wav": true, ".mp3": true,
	".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".png": true, ".jpg": true,
	".jpeg": true, ".webp": true, ".gif": true, ".woff": true, ".woff2": true, ".ttf": true,
	".otf": true, ".pdf": true, ".zip": true, ".psd": true,
}

// fileTree 是相对项目根的斜杠路径到状态的映射。状态是 "sha256:<hex>"（内容已存）
// 或 "stat:<大小>:<修改时间纳秒>"（只记 stat）。
type fileTree map[string]string

// FileChange 是一轮里一个文件的变化。
type FileChange struct {
	Path       string `json:"path"`
	Change     string `json:"change"` // added | modified | deleted
	Revertable bool   `json:"revertable"`
}

// Turn 是一轮对话的记录。
type Turn struct {
	ID        string       `json:"turn_id"`
	SessionID string       `json:"session_id"`
	Kind      string       `json:"kind"` // chat | production
	Message   string       `json:"message"`
	Status    string       `json:"status"` // running | succeeded | failed | cancelled | interrupted | reverted
	Error     string       `json:"error,omitempty"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   *time.Time   `json:"ended_at,omitempty"`
	Changes   []FileChange `json:"changes"`
	Before    fileTree     `json:"before"`
	After     fileTree     `json:"after,omitempty"`
}

// studioRoot 是项目里 Studio 状态的目录。
func studioRoot(project string) string {
	return filepath.Join(project, "production", "studio")
}

func blobPath(project, state string) string {
	return filepath.Join(studioRoot(project), "blobs", strings.TrimPrefix(state, shaPrefix))
}

// scanTree 扫描项目，必要时把文本内容写进 blob 存储。
func scanTree(project string) (fileTree, error) {
	tree := fileTree{}
	studioDir := studioRoot(project)
	err := filepath.WalkDir(project, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == project {
				return walkErr
			}
			return nil
		}
		if d.IsDir() {
			if path != project && (skippedDirs[d.Name()] || path == studioDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(project, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		state, err := fileState(project, path, info)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(rel)] = state
		return nil
	})
	return tree, err
}

func statState(info fs.FileInfo) string {
	return fmt.Sprintf("%s%d:%d", statPrefix, info.Size(), info.ModTime().UnixNano())
}

// fileState 计算一个文件的状态；文本文件顺带存进 blob。
func fileState(project, path string, info fs.FileInfo) (string, error) {
	if info.Size() > maxSnapshotBytes || mediaExtensions[strings.ToLower(filepath.Ext(path))] {
		return statState(info), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		// 读不了（权限、竞态删除）就退回 stat，不让整轮失败。
		return statState(info), nil
	}
	head := body
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return statState(info), nil
	}
	sum := sha256.Sum256(body)
	state := shaPrefix + hex.EncodeToString(sum[:])
	blob := blobPath(project, state)
	if _, err := os.Stat(blob); os.IsNotExist(err) {
		if err := fsutil.AtomicWrite(blob, body, 0o644); err != nil {
			return "", fmt.Errorf("写快照：%w", err)
		}
	}
	return state, nil
}

// currentState 读取单个文件的当前状态；不存在时返回空串。
func currentState(project, rel string) (string, error) {
	path := filepath.Join(project, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fileState(project, path, info)
}

func isContentState(state string) bool { return strings.HasPrefix(state, shaPrefix) }

// diffTrees 比较前后两次扫描，按路径排序返回变化。
func diffTrees(before, after fileTree) []FileChange {
	var changes []FileChange
	for path, old := range before {
		now, ok := after[path]
		switch {
		case !ok:
			changes = append(changes, FileChange{Path: path, Change: "deleted", Revertable: isContentState(old)})
		case now != old:
			changes = append(changes, FileChange{Path: path, Change: "modified", Revertable: isContentState(old) && isContentState(now)})
		}
	}
	for path, now := range after {
		if _, ok := before[path]; !ok {
			changes = append(changes, FileChange{Path: path, Change: "added", Revertable: isContentState(now)})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	if changes == nil {
		changes = []FileChange{}
	}
	return changes
}

// readBlob 读出某个状态对应的内容；stat 状态与空状态返回 nil。
func readBlob(project, state string) ([]byte, error) {
	if !isContentState(state) {
		return nil, nil
	}
	return os.ReadFile(blobPath(project, state))
}

// ConflictError 表示回退时有文件在这一轮之后又被改过。
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	return "这些文件在本轮之后又被改过，不能直接回退：" + strings.Join(e.Paths, "、")
}

// revertTurn 把一轮里可回退的改动恢复成本轮开始前的样子。
//
// 先全部检查、再统一写入：只要有一个文件的当前状态不是本轮结束时的状态，
// 就整体拒绝，不做部分回退。返回跳过的（不可回退的）路径。
func revertTurn(project string, turn *Turn) ([]string, error) {
	if turn.Status == "running" {
		return nil, errors.New("本轮还在进行，不能回退")
	}
	if turn.Status == "reverted" {
		return nil, errors.New("本轮已经回退过")
	}
	if turn.After == nil {
		return nil, errors.New("本轮没有结束快照，无法判断改动")
	}
	var conflicts, skipped []string
	for _, change := range turn.Changes {
		if !change.Revertable {
			skipped = append(skipped, change.Path)
			continue
		}
		now, err := currentState(project, change.Path)
		if err != nil {
			return nil, err
		}
		if now != turn.After[change.Path] {
			conflicts = append(conflicts, change.Path)
		}
	}
	if len(conflicts) > 0 {
		return nil, &ConflictError{Paths: conflicts}
	}
	for _, change := range turn.Changes {
		if !change.Revertable {
			continue
		}
		path := filepath.Join(project, filepath.FromSlash(change.Path))
		if change.Change == "added" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		body, err := readBlob(project, turn.Before[change.Path])
		if err != nil {
			return nil, fmt.Errorf("读取 %s 的快照：%w", change.Path, err)
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
		if err := fsutil.AtomicWrite(path, body, mode); err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

func turnPath(project, id string) string {
	return filepath.Join(studioRoot(project), "turns", id+".json")
}

func saveTurn(project string, turn *Turn) error {
	body, err := json.MarshalIndent(turn, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(turnPath(project, turn.ID), append(body, '\n'), 0o644)
}

func loadTurn(project, id string) (*Turn, error) {
	if !validID(id) {
		return nil, os.ErrNotExist
	}
	body, err := os.ReadFile(turnPath(project, id))
	if err != nil {
		return nil, err
	}
	var turn Turn
	if err := json.Unmarshal(body, &turn); err != nil {
		return nil, err
	}
	return &turn, nil
}

// statTree 只取 stat，不读内容，供对话进行中频繁检查「有没有文件变了」。
func statTree(project string) map[string]string {
	out := map[string]string{}
	studioDir := studioRoot(project)
	_ = filepath.WalkDir(project, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != project && (skippedDirs[d.Name()] || path == studioDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		rel, relErr := filepath.Rel(project, path)
		if infoErr == nil && relErr == nil {
			out[filepath.ToSlash(rel)] = statState(info)
		}
		return nil
	})
	return out
}

// changedPaths 比较两次 stat，返回变化过的路径。
func changedPaths(before, after map[string]string) []string {
	var paths []string
	for path, state := range after {
		if before[path] != state {
			paths = append(paths, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// validID 只允许 Studio 自己生成的 id 字符，防止路径穿越。
func validID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
