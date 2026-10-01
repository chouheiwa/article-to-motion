package reads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnits(t *testing.T) {
	cases := map[string]int{
		"配音、字幕和渲染":        7,
		"GPU 算力提升 3 倍":    7, // GPU、算、力、提、升、3、倍
		"Claude Opus 5.5": 4, // Claude、Opus、5、5
		"！？……":            0,
		"":                0,
	}
	for text, want := range cases {
		if got := Units(text); got != want {
			t.Errorf("Units(%q) = %d，期望 %d", text, got, want)
		}
	}
}

func TestRequiredFrames(t *testing.T) {
	// 4 个字：4/9 秒不到 5/6 秒，取下限 25 帧（30fps）。
	if got := RequiredFrames(4, 30, Defaults); got != 25 {
		t.Errorf("短文本应取最短显示时长：%d", got)
	}
	// 18 个字：2 秒，60 帧。
	if got := RequiredFrames(18, 30, Defaults); got != 60 {
		t.Errorf("18 字应需 60 帧：%d", got)
	}
}

func event(id, text string, start, end int) Event {
	return Event{ID: id, SceneID: "scene-001", Text: text, Role: "label", PhraseID: "p-" + id,
		EnterFrame: start - 5, ReadableStartFrame: start, ReadableEndFrameExc: end, ExitEndFrame: end + 5}
}

func TestShortReadIsAProblem(t *testing.T) {
	inv := Inventory{FPS: 30, Events: []Event{
		event("a", "一共分三步", 10, 60),                // 5 字需 25 帧，有 50 帧
		event("b", "写稿配音渲染然后拼接混音最后交付成片", 100, 130), // 18 字需 60 帧，只有 30 帧
	}}
	report := Check(inv, nil, Defaults)
	if report.OK || len(report.Problems) != 1 || !strings.Contains(report.Problems[0], "b（scene-001）可读 30 帧") || !strings.Contains(report.Problems[0], "还差 30 帧") {
		t.Fatalf("只有 b 不够读：%v", report.Problems)
	}
}

func TestDecorativeIsSkippedAndFrameOrderChecked(t *testing.T) {
	decorative := event("d", "很长很长的装饰文字不需要读完的", 0, 2)
	decorative.Role = DecorativeRole
	broken := event("x", "好", 50, 40)
	duplicate := event("a", "一", 200, 260)
	report := Check(Inventory{FPS: 30, Events: []Event{decorative, broken, event("a", "一", 100, 160), duplicate, {Text: "无编号"}}}, nil, Defaults)
	joined := strings.Join(report.Problems, "\n")
	for _, want := range []string{"x 的帧号顺序不成立", "event_id 重复：a", "缺少 event_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少问题 %q：\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "d（") {
		t.Error("装饰元素不应检查阅读时间")
	}
}

func TestPhraseBindingAndOrder(t *testing.T) {
	timeline := &Timeline{FPS: 30, Phrases: []Phrase{
		{ID: "p-a", Order: 1, StartFrame: 0},
		{ID: "p-b", Order: 2, StartFrame: 100},
		{ID: "p-c", Order: 3, StartFrame: 200},
	}}
	early := event("b", "第二件事", 90, 150)   // 第 85 帧出现，短语 100 才开始
	reversed := event("c", "第三件事", 40, 80) // 比 a 先可读，但口播在后
	unbound := event("e", "无短语", 300, 340)
	unbound.PhraseID = ""
	report := Check(Inventory{FPS: 30, Events: []Event{event("a", "第一件事", 60, 100), early, reversed, unbound}}, timeline, Defaults)
	joined := strings.Join(report.Problems, "\n")
	for _, want := range []string{"b 在第 85 帧出现，早于绑定短语 p-b 的开始帧 100", "c 在第 35 帧出现", "c（短语顺序 3）比 a（短语顺序 1）先可读", "e 没有绑定 phrase_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少问题 %q：\n%s", want, joined)
		}
	}
}

func TestMismatchedFPSDisablesPhraseChecks(t *testing.T) {
	report := Check(Inventory{FPS: 30, Events: []Event{event("a", "一", 10, 60)}}, &Timeline{FPS: 24, Phrases: []Phrase{{ID: "p-a"}}}, Defaults)
	if report.OK || !strings.Contains(report.Problems[0], "fps") || len(report.Problems) != 1 {
		t.Fatalf("fps 不一致应报一次并跳过短语比对：%v", report.Problems)
	}
}

// TestCrowdingIsOnlyAHint：卡片组一起出现是合法版式，只提示。
func TestCrowdingIsOnlyAHint(t *testing.T) {
	report := Check(Inventory{FPS: 30, Events: []Event{
		event("a", "甲", 100, 160), event("b", "乙", 105, 165), event("c", "丙", 300, 360),
	}}, nil, Defaults)
	if !report.OK || len(report.Hints) != 1 || !strings.Contains(report.Hints[0], "[a b]") {
		t.Fatalf("同时出现应只进 hints：%+v", report)
	}
}

func TestLoadAndWrite(t *testing.T) {
	dir := t.TempDir()
	inventory := filepath.Join(dir, "inv.json")
	os.WriteFile(inventory, []byte(`{"schema":1,"fps":30,"events":[{"event_id":"a","text":"一","enter_frame":0,"readable_start_frame":0,"readable_end_frame_exclusive":30,"exit_end_frame":30}]}`), 0o644)
	inv, err := LoadInventory(inventory)
	if err != nil || inv.Events[0].ReadableEndFrameExc != 30 {
		t.Fatalf("读取失败：%v %+v", err, inv)
	}
	for name, body := range map[string]string{"nofps.json": `{"events":[{}]}`, "empty.json": `{"fps":30,"events":[]}`, "bad.json": `{`} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := LoadInventory(path); err == nil {
			t.Errorf("%s 应报错", name)
		}
	}
	timeline := filepath.Join(dir, "phrases.json")
	os.WriteFile(timeline, []byte(`{"fps":30,"phrases":[{"phrase_id":"p","semantic_order":1,"start_frame":0,"end_frame_exclusive":10}]}`), 0o644)
	if tl, err := LoadTimeline(timeline); err != nil || tl.Phrases[0].EndFrameExclu != 10 {
		t.Fatalf("短语时间轴读取失败：%v", err)
	}
	os.WriteFile(timeline, []byte(`{"fps":30,"phrases":[]}`), 0o644)
	if _, err := LoadTimeline(timeline); err == nil {
		t.Error("空短语时间轴应报错")
	}
	if err := Check(inv, nil, Defaults).WriteJSON(filepath.Join(dir, "r.json")); err != nil {
		t.Fatal(err)
	}
}
