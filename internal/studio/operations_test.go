package studio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyOperationFileEdits(t *testing.T) {
	root := t.TempDir()
	scenePath := filepath.Join(root, "scenes", "scene-001", "scene.json")
	writeStudioFile(t, scenePath, `{"id":"scene-001","duration_seconds":2,"output":"out.mp4","text":"before"}`)
	writeStudioFile(t, filepath.Join(root, "script.md"), "old script")
	planPath := filepath.Join(root, "production", "scene-plan.json")
	writeStudioFile(t, planPath, `{"entries":[]}`)

	s, _ := startStudio(t, Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	ctx := context.Background()
	apply := func(op Operation) error {
		return s.applyOperation(ctx, &op)
	}

	if err := apply(Operation{ID: "op-caption", Type: "scene.update_caption", SceneID: "scene-001", Changes: map[string]any{"text": "after"}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(scenePath); !strings.Contains(string(body), "after") {
		t.Fatalf("caption was not applied: %s", body)
	}
	if err := apply(Operation{Type: "scene.update_caption", SceneID: "scene-001", Changes: map[string]any{}}); err == nil {
		t.Fatal("caption without text should fail")
	}
	if err := apply(Operation{Type: "scene.update_caption", SceneID: "missing", Changes: map[string]any{"text": "x"}}); err != nil {
		t.Fatalf("caption for missing scene should be a no-op: %v", err)
	}

	if err := apply(Operation{Type: "scene.update_timing_plan", Changes: map[string]any{"plan": map[string]any{"entries": []any{map[string]any{"id": "x"}}}}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(planPath); !strings.Contains(string(body), `"x"`) {
		t.Fatalf("timing plan was not applied: %s", body)
	}
	if err := apply(Operation{Type: "scene.update_timing_plan", Changes: map[string]any{}}); err == nil {
		t.Fatal("timing plan without changes.plan should fail")
	}
	writeStudioFile(t, planPath, "{")
	if err := apply(Operation{Type: "scene.update_timing_plan", Changes: map[string]any{"plan": map[string]any{}}}); err == nil {
		t.Fatal("invalid timing plan document should fail")
	}
	writeStudioFile(t, planPath, `{"entries":[]}`)

	for typ, key := range map[string]string{
		"scene.update_character_motion": "character_motion",
		"scene.update_camera":           "camera",
		"scene.update_style_parameters": "style_parameters",
	} {
		opID := "op-" + key
		if err := apply(Operation{ID: opID, Type: typ, SceneID: "scene-001", Changes: map[string]any{"value": 1}}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		body, err := os.ReadFile(filepath.Join(root, "production", "studio", opID+".json"))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if !strings.Contains(string(body), key) || !strings.Contains(string(body), "scene-001") {
			t.Fatalf("%s document = %s", typ, body)
		}
	}
	if err := apply(Operation{Type: "scene.update_camera", SceneID: "missing", Changes: map[string]any{}}); err == nil {
		t.Fatal("camera update for missing scene should fail")
	}

	if err := apply(Operation{Type: "script.revise", Changes: map[string]any{"text": "new script"}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "script.md")); string(body) != "new script" {
		t.Fatalf("script.md = %s", body)
	}
	if err := apply(Operation{Type: "script.revise", Changes: map[string]any{}}); err == nil {
		t.Fatal("script revise without text should fail")
	}
	writeStudioFile(t, filepath.Join(root, "SCRIPT.md"), "upper")
	if err := apply(Operation{Type: "script.revise", Changes: map[string]any{"text": "upper new"}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "SCRIPT.md")); string(body) != "upper new" {
		t.Fatalf("SCRIPT.md = %s", body)
	}

	if err := apply(Operation{Type: "scene.render", SceneID: "missing"}); err == nil {
		t.Fatal("render for missing scene should fail")
	}
	if err := apply(Operation{Type: "scene.capture_frames", SceneID: "missing"}); err == nil {
		t.Fatal("frame capture for missing scene should fail")
	}
	if err := apply(Operation{Type: "scene.capture_frames", SceneID: "scene-001"}); err == nil || !strings.Contains(err.Error(), "镜头产物不存在") {
		t.Fatalf("frame capture without output = %v", err)
	}
	if err := apply(Operation{Type: "scene.validate", SceneID: "missing"}); err == nil {
		t.Fatal("lint for missing scene should fail")
	}
	if err := apply(Operation{Type: "scene.validate", SceneID: "scene-001"}); err == nil || !strings.Contains(err.Error(), "镜头产物不存在") {
		t.Fatalf("lint without output = %v", err)
	}
	if err := apply(Operation{Type: "publish.check"}); err == nil || !strings.Contains(err.Error(), "publish.md") {
		t.Fatalf("publish check without file = %v", err)
	}
	if err := apply(Operation{Type: "project.review"}); err == nil || !strings.Contains(err.Error(), "成片") {
		t.Fatalf("review without video = %v", err)
	}
	if err := apply(Operation{Type: "project.inspect"}); err != nil {
		t.Fatalf("inspect should be a no-op: %v", err)
	}
}

func TestSnapshotForAndRestore(t *testing.T) {
	root := t.TempDir()
	// openProject 会把路径规范化（macOS 上是 /var → /private/var），
	// 快照里记录的是规范化后的路径，断言也要用同一份。
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = resolvedRoot
	scenePath := filepath.Join(root, "scenes", "scene-001", "scene.json")
	writeStudioFile(t, scenePath, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4"}`)
	planPath := filepath.Join(root, "production", "scene-plan.json")
	writeStudioFile(t, planPath, `{"entries":[]}`)
	writeStudioFile(t, filepath.Join(root, "script.md"), "lower")
	writeStudioFile(t, filepath.Join(root, "SCRIPT.md"), "upper")

	s, _ := startStudio(t, Config{ProjectPath: root, ConfigPath: filepath.Join(t.TempDir(), "projects.json")})

	snap, err := s.snapshotFor(&Operation{ID: "op-caption", Type: "scene.update_caption", SceneID: "scene-001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != scenePath || snap.Files[0].Before == "" || snap.Files[0].Backup == "" {
		t.Fatalf("caption snapshot = %#v", snap.Files)
	}
	if snap.Files[0].Body != nil {
		t.Fatal("snapshot body should be moved to the backup file")
	}
	if body, err := os.ReadFile(snap.Files[0].Backup); err != nil || !strings.Contains(string(body), "scene-001") {
		t.Fatalf("snapshot backup = %s, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, "production", "studio", "snapshots", snap.ID, "snapshot.json")); err != nil {
		t.Fatal(err)
	}

	snap, err = s.snapshotFor(&Operation{ID: "op-plan", Type: "scene.update_timing_plan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != planPath {
		t.Fatalf("plan snapshot = %#v", snap.Files)
	}

	snap, err = s.snapshotFor(&Operation{ID: "op-script", Type: "script.revise"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("script snapshot = %#v", snap.Files)
	}

	snap, err = s.snapshotFor(&Operation{ID: "op-camera", Type: "scene.update_camera", SceneID: "scene-001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("camera snapshot = %#v", snap.Files)
	}
	studioFile := filepath.Join(root, "production", "studio", "op-camera.json")
	if snap.Files[1].Path != studioFile || snap.Files[1].Before != "" {
		t.Fatalf("camera studio snapshot = %#v", snap.Files[1])
	}

	snap, err = s.snapshotFor(&Operation{ID: "op-inspect", Type: "project.inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Files) != 0 {
		t.Fatalf("inspect snapshot = %#v", snap.Files)
	}

	noProject, _ := startStudio(t, Config{ConfigPath: filepath.Join(t.TempDir(), "projects.json")})
	if _, err := noProject.snapshotFor(&Operation{ID: "op", Type: "project.inspect"}); err == nil {
		t.Fatal("snapshot without a project should fail")
	}

	restorePath := filepath.Join(root, "restore.txt")
	writeStudioFile(t, restorePath, "current")
	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: restorePath, Before: "hash", Body: []byte("restored")}}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(restorePath); string(body) != "restored" {
		t.Fatalf("restored file = %s", body)
	}

	removePath := filepath.Join(root, "remove.txt")
	writeStudioFile(t, removePath, "remove")
	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: removePath}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(removePath); !os.IsNotExist(err) {
		t.Fatalf("file was not removed: %v", err)
	}

	backupPath := filepath.Join(root, "backup.bin")
	writeStudioFile(t, backupPath, "backup")
	fromBackup := filepath.Join(root, "from-backup.txt")
	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: fromBackup, Before: "hash", Backup: backupPath}}}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(fromBackup); string(body) != "backup" {
		t.Fatalf("restored from backup = %s", body)
	}

	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: filepath.Join(root, "never-existed.txt")}}}); err != nil {
		t.Fatalf("removing a missing file should be ignored: %v", err)
	}
	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: fromBackup, Before: "hash", Backup: filepath.Join(root, "missing.bak")}}}); err == nil {
		t.Fatal("missing backup should fail")
	}
	dirPath := filepath.Join(root, "a-directory")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreSnapshot(&Snapshot{Files: []SnapshotFile{{Path: dirPath, Before: "hash", Body: []byte("x")}}}); err == nil {
		t.Fatal("writing over a directory should fail")
	}
}
