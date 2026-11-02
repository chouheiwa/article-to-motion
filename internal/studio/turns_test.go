package studio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		writeStudioFile(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
}

func TestScanTreeSkipsStateAndStoresText(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"scenes/a/prompt.md":              "hello",
		"scenes/a/out.mp4":                "fake video",
		"scenes/a/attempts/old.log":       "skip",
		"node_modules/x/index.js":         "skip",
		"production/studio/tasks.json":    "skip",
		"production/scene-plan.json":      "{}",
		"assets/binary.dat":               "a\x00b",
		".agents/skills/s/SKILL.md":       "skip",
		"scenes/a/.studio-frames/f.jpg":   "skip",
		"production/studio-notes/keep.md": "keep",
	})
	tree, err := scanTree(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, skipped := range []string{"scenes/a/attempts/old.log", "node_modules/x/index.js", "production/studio/tasks.json", ".agents/skills/s/SKILL.md", "scenes/a/.studio-frames/f.jpg"} {
		if _, ok := tree[skipped]; ok {
			t.Fatalf("%s 不应进快照", skipped)
		}
	}
	if !isContentState(tree["scenes/a/prompt.md"]) || !isContentState(tree["production/studio-notes/keep.md"]) {
		t.Fatalf("文本文件应存内容：%v", tree)
	}
	for _, statOnly := range []string{"scenes/a/out.mp4", "assets/binary.dat"} {
		if !strings.HasPrefix(tree[statOnly], statPrefix) {
			t.Fatalf("%s 应只记 stat：%s", statOnly, tree[statOnly])
		}
	}
	body, err := readBlob(root, tree["scenes/a/prompt.md"])
	if err != nil || string(body) != "hello" {
		t.Fatalf("blob=%q err=%v", body, err)
	}
}

func TestRevertRestoresTextAndSkipsMedia(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"keep.md": "same", "edit.md": "old", "gone.md": "bye", "out.mp4": "v1"})
	before, err := scanTree(root)
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{"edit.md": "new", "added.md": "fresh", "out.mp4": "v2-longer"})
	os.Remove(filepath.Join(root, "gone.md"))
	after, err := scanTree(root)
	if err != nil {
		t.Fatal(err)
	}
	turn := &Turn{ID: "t-1", Status: "succeeded", Before: before, After: after, Changes: diffTrees(before, after)}
	want := map[string]string{"added.md": "added", "edit.md": "modified", "gone.md": "deleted", "out.mp4": "modified"}
	if len(turn.Changes) != len(want) {
		t.Fatalf("changes=%+v", turn.Changes)
	}
	for _, change := range turn.Changes {
		if want[change.Path] != change.Change || change.Revertable == (change.Path == "out.mp4") {
			t.Fatalf("change=%+v", change)
		}
	}
	skipped, err := revertTurn(root, turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0] != "out.mp4" {
		t.Fatalf("skipped=%v", skipped)
	}
	for rel, body := range map[string]string{"edit.md": "old", "gone.md": "bye", "keep.md": "same"} {
		if got, _ := os.ReadFile(filepath.Join(root, rel)); string(got) != body {
			t.Fatalf("%s=%q", rel, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "added.md")); !os.IsNotExist(err) {
		t.Fatal("本轮新增的文件应当删掉")
	}
}

func TestRevertRefusesFilesChangedLater(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.md": "1", "b.md": "1"})
	before, _ := scanTree(root)
	writeTree(t, root, map[string]string{"a.md": "2", "b.md": "2"})
	after, _ := scanTree(root)
	turn := &Turn{ID: "t-1", Status: "succeeded", Before: before, After: after, Changes: diffTrees(before, after)}
	time.Sleep(10 * time.Millisecond)
	writeTree(t, root, map[string]string{"b.md": "3"})
	_, err := revertTurn(root, turn)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || len(conflict.Paths) != 1 || conflict.Paths[0] != "b.md" {
		t.Fatalf("err=%v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.md")); string(got) != "2" {
		t.Fatal("有冲突时不应做部分回退")
	}
	for _, status := range []string{"running", "reverted"} {
		if _, err := revertTurn(root, &Turn{Status: status, After: after}); err == nil {
			t.Fatalf("%s 的轮次不能回退", status)
		}
	}
	if _, err := revertTurn(root, &Turn{Status: "failed"}); err == nil {
		t.Fatal("没有结束快照时不能回退")
	}
}

func TestStatTreeDetectsChanges(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.md": "1", "b.md": "1"})
	before := statTree(root)
	writeTree(t, root, map[string]string{"a.md": "22", "c.md": "new"})
	os.Remove(filepath.Join(root, "b.md"))
	got := changedPaths(before, statTree(root))
	if strings.Join(got, ",") != "a.md,b.md,c.md" {
		t.Fatalf("changed=%v", got)
	}
}

func TestValidIDRejectsTraversal(t *testing.T) {
	for _, id := range []string{"", "../x", "a/b", "A", strings.Repeat("a", 81)} {
		if validID(id) {
			t.Fatalf("%q 不应合法", id)
		}
	}
	if !validID(newID("t")) {
		t.Fatal("自己生成的 id 必须合法")
	}
	if _, err := loadTurn(t.TempDir(), "../../etc/passwd"); err == nil {
		t.Fatal("非法 id 不应读文件")
	}
}
