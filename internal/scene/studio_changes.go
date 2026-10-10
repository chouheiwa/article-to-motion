package scene

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// studioChangesSection reads confirmed Studio operation records without making
// scene.json accept UI-specific fields. The records live beside the project
// production tree and are fed into the renderer as explicit instructions.
func studioChangesSection(sceneDir, sceneID string) string {
	root := sceneDir
	for {
		dir := filepath.Join(root, "production", "studio")
		if entries, err := os.ReadDir(dir); err == nil {
			var paths []string
			for _, entry := range entries {
				if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
					paths = append(paths, filepath.Join(dir, entry.Name()))
				}
			}
			sort.Strings(paths)
			var sections []string
			for _, path := range paths {
				body, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var record struct {
					SceneID string         `json:"scene_id"`
					Type    string         `json:"type"`
					Changes map[string]any `json:"changes"`
				}
				if json.Unmarshal(body, &record) != nil || record.SceneID != sceneID {
					continue
				}
				changes, err := json.Marshal(record.Changes)
				if err != nil {
					continue
				}
				sections = append(sections, fmt.Sprintf("- %s：%s", record.Type, changes))
			}
			if len(sections) > 0 {
				return "\nStudio 已确认的修改（必须落实到本次渲染）：\n" + joinLines(sections) + "\n"
			}
		}
		if fileExists(filepath.Join(root, "frame.md")) || fileExists(filepath.Join(root, "article-to-motion.conf")) {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return ""
}

func joinLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
