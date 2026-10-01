package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateReadsCommand(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	os.MkdirAll(production, 0o755)
	inventory := filepath.Join(production, "visible-event-inventory.json")
	os.WriteFile(inventory, []byte(`{"schema":1,"fps":30,"events":[
 {"event_id":"a","scene_id":"scene-001","text":"一共分三步","role":"title","phrase_id":"p1","enter_frame":0,"readable_start_frame":5,"readable_end_frame_exclusive":60,"exit_end_frame":70}]}`), 0o644)
	var out, errOut bytes.Buffer
	if code := Execute([]string{"validate", "reads", "--project-root", root}, &out, &errOut); code != 0 {
		t.Fatalf("够读的元素应通过：%d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "跳过短语绑定") {
		t.Errorf("没有短语时间轴时应提示跳过：%s", out.String())
	}
	os.WriteFile(filepath.Join(production, "phrase-timeline.json"), []byte(`{"fps":30,"phrases":[{"phrase_id":"p1","semantic_order":1,"start_frame":30,"end_frame_exclusive":60}]}`), 0o644)
	out.Reset()
	errOut.Reset()
	report := filepath.Join(root, "reads.json")
	if code := Execute([]string{"validate", "reads", "--project-root", root, "--report-json", report}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "早于绑定短语 p1") {
		t.Fatalf("早于短语出现应失败：%d %s", code, errOut.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Errorf("应写出报告：%v", err)
	}
}

func TestValidateReadsRejectsBadOptionsAndMissingFile(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Execute([]string{"validate", "reads", "--chars-per-second", "0"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--chars-per-second") {
		t.Errorf("非正阅读速度应报错：%d %s", code, errOut.String())
	}
	errOut.Reset()
	if code := Execute([]string{"validate", "reads", "--project-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "visible-event-inventory.json") {
		t.Errorf("缺清单应报错并点名文件：%d %s", code, errOut.String())
	}
}
