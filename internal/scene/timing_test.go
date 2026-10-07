package scene

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

const testPlanSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func intPtr(n int) *int { return &n }

// 四条字幕：1 与 2 之间、3 与 4 之间各有一段空白，2 与 3 首尾相接。
// 30fps 下的帧区间：[0,30) [45,90) [90,120) [150,180)。
var testCues = []srt.Cue{
	{Index: 1, StartSeconds: 0, EndSeconds: 1},
	{Index: 2, StartSeconds: 1.5, EndSeconds: 3},
	{Index: 3, StartSeconds: 3, EndSeconds: 4},
	{Index: 4, StartSeconds: 5, EndSeconds: 6},
}

func planOf(entries ...PlanEntry) TimingPlan {
	return TimingPlan{Schema: TimingPlanSchema, Scenes: entries}
}

func entry(id string, sub int, cut string) PlanEntry {
	e := PlanEntry{ID: id, Dir: "scenes/" + id, Cut: cut}
	if sub > 0 {
		e.FirstSubtitle = intPtr(sub)
	}
	return e
}

func spans(segments []TimingSegment) string {
	parts := make([]string, len(segments))
	for i, s := range segments {
		parts[i] = fmt.Sprintf("[%d,%d)", s.StartFrame, s.EndFrameExclusive)
	}
	return strings.Join(parts, " ")
}

func TestParseTimingPlanRejectsMalformedPlans(t *testing.T) {
	cases := map[string]string{
		`{"schema":2,"scenes":[{"id":"a","dir":"a"}]}`:                                                            "schema",
		`{"schema":1,"scenes":[]}`:                                                                                "为空",
		`{"schema":1,"scenes":[{"id":"a","dir":"a","extra":1}]}`:                                                  "未知字段",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"b","dir":"b"}]}`:                                       "缺少锚点",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"b","dir":"b","first_subtitle":2,"first_phrase":"p"}]}`: "只能填一个",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"b","dir":"b","first_subtitle":0}]}`:                    "从 1 开始",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"b","dir":"b","first_subtitle":2,"cut":"later"}]}`:      "cut 只能是",
		`{"schema":1,"scenes":[{"id":"a","dir":"a","cut":"mid_gap"}]}`:                                            "不能写 cut",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"a","dir":"b","first_subtitle":2}]}`:                    "id 重复",
		`{"schema":1,"scenes":[{"id":"a","dir":"a"},{"id":"b","dir":"a","first_subtitle":2}]}`:                    "dir 重复",
		`{"schema":1,"scenes":[{"id":"","dir":"a"}]}`:                                                             "缺少 id 或 dir",
		`{"schema":1,"scenes":[{"id":"a","dir":"a","visual_role":"装饰"}]}`:                                         "visual_role 只能是",
		`not json`: "不是合法 JSON",
	}
	for body, want := range cases {
		if _, err := ParseTimingPlan([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s 应报 %q，实际 %v", body, want, err)
		}
	}
	plan, err := ParseTimingPlan([]byte(`{"schema":1,"scenes":[{"id":"a","dir":"a","visual_role":"语境","summary":"开场"},{"id":"b","dir":"b","first_phrase":"p-2","cut":"prev_end","visual_role":"证据"}]}`))
	if err != nil || len(plan.Scenes) != 2 || plan.Scenes[1].FirstPhrase != "p-2" || plan.Scenes[1].VisualRole != "证据" {
		t.Fatalf("合法计划解析失败：%+v %v", plan, err)
	}
}

func TestLayoutTimingCutModes(t *testing.T) {
	cases := []struct {
		cut  string
		want string
	}{
		{"", "[0,45) [45,200)"},
		{CutNextStart, "[0,45) [45,200)"},
		{CutPrevEnd, "[0,30) [30,200)"},
		{CutMidGap, "[0,37) [37,200)"},
	}
	for _, c := range cases {
		segments, err := LayoutTiming(planOf(entry("a", 0, ""), entry("b", 2, c.cut)), testCues, nil, 30, 200)
		if err != nil {
			t.Fatalf("cut=%q：%v", c.cut, err)
		}
		if got := spans(segments); got != c.want {
			t.Errorf("cut=%q 得到 %s，应为 %s", c.cut, got, c.want)
		}
	}
}

// 2 与 3 首尾相接没有空白：三种规则都只能切在锚点开始处。
func TestLayoutTimingWithoutGapCutsAtAnchor(t *testing.T) {
	for _, cut := range []string{CutNextStart, CutPrevEnd, CutMidGap} {
		segments, err := LayoutTiming(planOf(entry("a", 0, ""), entry("b", 3, cut)), testCues, nil, 30, 200)
		if err != nil {
			t.Fatal(err)
		}
		if got := spans(segments); got != "[0,90) [90,200)" {
			t.Errorf("cut=%s 得到 %s", cut, got)
		}
	}
}

func TestLayoutTimingSumsToTotalAndAbsorbsGaps(t *testing.T) {
	segments, err := LayoutTiming(planOf(entry("a", 1, ""), entry("b", 2, ""), entry("c", 4, CutPrevEnd)), testCues, nil, 30, 210)
	if err != nil {
		t.Fatal(err)
	}
	if got := spans(segments); got != "[0,45) [45,120) [120,210)" {
		t.Errorf("得到 %s", got)
	}
	sum := 0
	for _, s := range segments {
		sum += s.Frames()
	}
	if sum != 210 {
		t.Errorf("合计 %d 帧", sum)
	}
}

func TestLayoutTimingPhraseAnchors(t *testing.T) {
	// 文件里乱序：「前一条」按时间找，不按文件顺序。
	phrases := &PhraseTimeline{FPS: 30, Phrases: []PhraseEntry{
		{ID: "p-3", StartFrame: 100, EndFrameExclusive: 130},
		{ID: "p-1", StartFrame: 0, EndFrameExclusive: 40},
		{ID: "p-2", StartFrame: 50, EndFrameExclusive: 80},
	}}
	plan := planOf(PlanEntry{ID: "a", Dir: "a"},
		PlanEntry{ID: "b", Dir: "b", FirstPhrase: "p-2", Cut: CutMidGap},
		PlanEntry{ID: "c", Dir: "c", FirstPhrase: "p-3", Cut: CutPrevEnd})
	segments, err := LayoutTiming(plan, testCues, phrases, 30, 150)
	if err != nil {
		t.Fatal(err)
	}
	if got := spans(segments); got != "[0,45) [45,80) [80,150)" {
		t.Errorf("得到 %s", got)
	}
}

func TestLayoutTimingRejectsBadAnchors(t *testing.T) {
	phrases := &PhraseTimeline{FPS: 30, Phrases: []PhraseEntry{{ID: "p-1", StartFrame: 0, EndFrameExclusive: 40}}}
	cases := []struct {
		plan    TimingPlan
		phrases *PhraseTimeline
		total   int
		want    string
	}{
		{planOf(entry("a", 0, ""), entry("b", 3, ""), entry("c", 2, "")), nil, 200, "严格递增"},
		{planOf(entry("a", 0, ""), entry("b", 2, ""), entry("c", 2, "")), nil, 200, "严格递增"},
		{planOf(entry("a", 0, ""), entry("b", 9, "")), nil, 200, "超出字幕范围"},
		{planOf(entry("a", 0, ""), entry("b", 4, "")), nil, 150, "不早于总帧数"},
		{planOf(entry("a", 0, ""), entry("b", 1, "")), nil, 200, "不足一帧"},
		{planOf(entry("a", 0, ""), PlanEntry{ID: "b", Dir: "b", FirstPhrase: "p-1"}), nil, 200, "--phrases"},
		{planOf(entry("a", 0, ""), PlanEntry{ID: "b", Dir: "b", FirstPhrase: "p-9"}), phrases, 200, "不在短语时间轴"},
		{planOf(entry("a", 0, "")), &PhraseTimeline{FPS: 25, Phrases: phrases.Phrases}, 200, "25fps"},
	}
	for i, c := range cases {
		_, err := LayoutTiming(c.plan, testCues, c.phrases, 30, c.total)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("第 %d 例应报 %q，实际 %v", i, c.want, err)
		}
	}
}

// 写进 scene.json 的 6 位小数必须能还原成原帧数，并通过整数帧校验。
func TestDurationLiteralRoundTrips(t *testing.T) {
	for _, fps := range []int{24, 25, 30, 50, 60} {
		for frames := 1; frames <= 20000; frames++ {
			value, err := strconv.ParseFloat(DurationLiteral(frames, fps), 64)
			if err != nil {
				t.Fatal(err)
			}
			exact := value * float64(fps)
			if int(math.Round(exact)) != frames || math.Abs(exact-math.Round(exact)) > frameAlignmentTolerance {
				t.Fatalf("%d 帧 @%dfps 写成 %s 后还原为 %.6f 帧", frames, fps, DurationLiteral(frames, fps), exact)
			}
		}
	}
}

func timedScene(t *testing.T, duration string, timing string) Scene {
	t.Helper()
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":`+duration+`,"output":"out.mp4","transcript":"transcript.srt","text":"hello","timing":`+timing+`}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyTimingInputs(t *testing.T) {
	ok := `{"fps":30,"start_frame":120,"end_frame_exclusive":431,"plan":"` + testPlanSHA + `"}`
	if err := VerifyTimingInputs(timedScene(t, "10.366667", ok)); err != nil {
		t.Fatalf("一致的 timing 块应通过：%v", err)
	}
	cases := map[string][2]string{
		"手改时长":   {"10.4", ok},
		"非整数帧":   {"10.367", ok},
		"帧率不符":   {"10.366667", `{"fps":25,"start_frame":120,"end_frame_exclusive":431,"plan":"` + testPlanSHA + `"}`},
		"区间倒置":   {"10.366667", `{"fps":30,"start_frame":431,"end_frame_exclusive":120,"plan":"` + testPlanSHA + `"}`},
		"缺计划摘要":  {"10.366667", `{"fps":30,"start_frame":120,"end_frame_exclusive":431,"plan":""}`},
		"短语文件缺失": {"10.366667", `{"fps":30,"start_frame":120,"end_frame_exclusive":431,"plan":"` + testPlanSHA + `","phrases":"phrase-timeline.json"}`},
		"短语路径逃逸": {"10.366667", `{"fps":30,"start_frame":120,"end_frame_exclusive":431,"plan":"` + testPlanSHA + `","phrases":"../x.json"}`},
	}
	for name, c := range cases {
		err := VerifyTimingInputs(timedScene(t, c[0], c[1]))
		var verification *VerificationError
		if !errors.As(err, &verification) {
			t.Errorf("%s：应返回 VerificationError，实际 %v", name, err)
		}
	}
	s := timedScene(t, "10.366667", ok)
	s.Beat = &beat.Cue{}
	if err := VerifyTimingInputs(s); err == nil || !strings.Contains(err.Error(), "互斥") {
		t.Errorf("timing 与 beat 同时出现应拒绝：%v", err)
	}
	s = timedScene(t, "10.366667", ok)
	os.WriteFile(filepath.Join(s.Directory, "p.json"), []byte(`{"schema":1,"fps":25,"phrases":[{"phrase_id":"p","start_frame":0,"end_frame_exclusive":3}]}`), 0o644)
	s.Timing.Phrases = "p.json"
	if err := VerifyTimingInputs(s); err == nil || !strings.Contains(err.Error(), "25fps") {
		t.Errorf("短语时间轴帧率不符应拒绝：%v", err)
	}
}

// 没有 timing 块的老镜头不受影响。
func TestVerifyTimingInputsIgnoresLegacyScenes(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1.25,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTimingInputs(s); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPromptRejectsHandEditedTiming(t *testing.T) {
	s := timedScene(t, "10.4", `{"fps":30,"start_frame":120,"end_frame_exclusive":431,"plan":"`+testPlanSHA+`"}`)
	if _, err := BuildPrompt(s, nil); err == nil {
		t.Fatal("时长与 timing 块不符时 BuildPrompt 应失败")
	}
}

func TestTimingIsAnOptionalField(t *testing.T) {
	found := false
	for _, f := range OptionalFields() {
		found = found || f == "timing"
	}
	if !found {
		t.Fatal("timing 应是 scene.json 的可选字段")
	}
}
