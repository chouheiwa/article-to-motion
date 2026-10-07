package align

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
)

// heard 把一段文本按匹配单元切开，每个单元占 step 秒，从 start 开始首尾相接，
// 模拟对齐工具的逐字输出（标点已丢）。
func heard(text string, start, step float64) []Item {
	var items []Item
	for _, t := range Tokenize(text) {
		items = append(items, Item{Text: t.Text, Start: start, End: start + step})
		start += step
	}
	return items
}

func mustParse(t *testing.T, text string) *Script {
	t.Helper()
	s, err := ParseScript(text)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return s
}

func mustBuild(t *testing.T, script *Script, items []Item, opts Options) Result {
	t.Helper()
	if opts.FPS == 0 {
		opts.FPS = 30
	}
	result, err := Build(script, items, opts)
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}
	return result
}

func contains(list []string, sub string) bool {
	for _, item := range list {
		if strings.Contains(item, sub) {
			return true
		}
	}
	return false
}

func TestParseScriptMarkup(t *testing.T) {
	cases := []struct {
		name, input, spoken string
		phrases             map[string]string
	}{
		{"标题与分段", "# 标题\n第一段。\n\n第二段\n接着写。\n", "第一段。\n第二段接着写。\n", nil},
		{"显示与朗读", "<2024年|二零二四年>发布", "二零二四年发布\n", nil},
		{"只读与只显示", "好<|嗯>的<（注）|>。", "好嗯的。\n", nil},
		{"转义", `a\<b\>c\@d\|e\\f`, `a<b>c@d|e\f` + "\n", nil},
		{"段内换行补空格", "hello\nworld\n中\n文", "hello world中文\n", nil},
		{"嵌套短语", "@{outer}先@{inner}说@{/inner}完@{/outer}。", "先说完。\n", map[string]string{"outer": "先说完", "inner": "说"}},
		{"短语里的标记", "@{year}<2024年|二零二四年>@{/year}", "二零二四年\n", map[string]string{"year": "2024年"}},
		{"不成标记的 @", "邮件 a@b.c", "邮件 a@b.c\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustParse(t, tc.input)
			if got := s.SpokenText(); got != tc.spoken {
				t.Errorf("朗读文本 = %q，期望 %q", got, tc.spoken)
			}
			if len(s.Phrases) != len(tc.phrases) {
				t.Fatalf("短语数 = %d，期望 %d", len(s.Phrases), len(tc.phrases))
			}
			for _, p := range s.Phrases {
				if got := s.displayText(p.startPiece, p.endPiece); got != tc.phrases[p.ID] {
					t.Errorf("短语 %s 显示文本 = %q，期望 %q", p.ID, got, tc.phrases[p.ID])
				}
			}
		})
	}
}

func TestParseScriptPhraseOrder(t *testing.T) {
	s := mustParse(t, "@{a}@{b}甲@{/b}乙@{/a}@{c}丙@{/c}")
	want := []string{"a", "b", "c"}
	for i, p := range s.Phrases {
		if p.ID != want[i] || p.Order != i+1 {
			t.Errorf("第 %d 个短语 = %s/%d，期望 %s/%d", i, p.ID, p.Order, want[i], i+1)
		}
	}
}

func TestParseScriptErrorsCarryPosition(t *testing.T) {
	cases := []struct{ input, want string }{
		{"好<显示朗读>", "第 1 行第 2 列"},
		{"好\n  <a|b", "第 2 行第 3 列"},
		{"<a|b|c>", "第 1 行第 5 列"},
		{"<|>", "两侧都是空的"},
		{"x\\y", "第 1 行第 2 列：无效转义"},
		{"@{A}x@{/A}", "不合法"},
		{"@{a}x@{/a}@{a}y@{/a}", "重复"},
		{"@{a}x", "第 1 行第 1 列：短语 @{a} 没有闭合"},
		{"x@{/a}", "没有对应"},
		{"@{a}x@{b}y@{/a}@{/b}", "交叉"},
		{"@{a}@{/a}", "是空的"},
		{"@{a", "缺少 }"},
	}
	for _, tc := range cases {
		_, err := ParseScript(tc.input)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q：错误 = %v，期望包含 %q", tc.input, err, tc.want)
		}
	}
}

func TestTokenizeMixedText(t *testing.T) {
	got := Tokenize("GPT-4 在2024年发布，价格3.5美元；don't ＡＢＣ v2.")
	var texts, norms []string
	for _, tok := range got {
		texts = append(texts, tok.Text)
		norms = append(norms, tok.Norm)
	}
	wantTexts := "GPT-4|在|2024|年|发|布|价|格|3.5|美|元|don't|ＡＢＣ|v2"
	wantNorms := "gpt4|在|2024|年|发|布|价|格|35|美|元|dont|abc|v2"
	if strings.Join(texts, "|") != wantTexts {
		t.Errorf("单元 = %s\n期望 %s", strings.Join(texts, "|"), wantTexts)
	}
	if strings.Join(norms, "|") != wantNorms {
		t.Errorf("规范化 = %s\n期望 %s", strings.Join(norms, "|"), wantNorms)
	}
}

func TestBuildExactMatch(t *testing.T) {
	s := mustParse(t, "@{hi}你好@{/hi}，世界。再见！")
	r := mustBuild(t, s, heard("你好世界再见", 1.0, 0.1), Options{})
	if !r.Report.OK || r.Report.MatchRate != 1 || r.Report.Labels[LabelExact] != 6 {
		t.Fatalf("报告不符：%+v", r.Report)
	}
	if len(r.Cues) != 2 || r.Cues[0].Text != "你好，世界。" || r.Cues[0].StartMs != 1000 || r.Cues[0].EndMs != 1400 {
		t.Fatalf("字幕不符：%+v", r.Cues)
	}
	if r.Cues[1].StartMs != 1400 || r.Cues[1].EndMs != 1600 || r.Cues[1].Text != "再见！" {
		t.Fatalf("第二条字幕不符：%+v", r.Cues[1])
	}
	p := r.Timeline.Phrases[0]
	if p.StartMs != 1000 || p.EndMs != 1200 || p.StartFrame != 30 || p.EndFrameExclusive != 36 ||
		p.AlignmentSource != SourceForced || p.Confidence != 1 || p.SubtitleIndex != 1 || p.SourceText != "你好" {
		t.Fatalf("短语不符：%+v", p)
	}
	if p.VisibleEventIDs == nil {
		t.Fatal("visible_event_ids 应写成 [] 而不是 null")
	}
}

func TestBuildReplacedCharIsHint(t *testing.T) {
	s := mustParse(t, "今天@{w}天气@{/w}很好，我们一起出去走走吧。")
	r := mustBuild(t, s, heard("今天天汽很好我们一起出去走走吧", 0, 0.2), Options{})
	if !r.Report.OK {
		t.Fatalf("单字读错不应判为问题：%v", r.Report.Problems)
	}
	if r.Report.Labels[LabelReplace] != 1 {
		t.Fatalf("应有 1 个 replacement，实际 %v", r.Report.Labels)
	}
	if !contains(r.Report.Hints, "对上的是「汽」") {
		t.Errorf("读法不同应进提示：%v", r.Report.Hints)
	}
	if p := r.Timeline.Phrases[0]; p.Confidence != 0.75 || p.AlignmentSource != SourceForced {
		t.Errorf("短语置信度应为 0.75：%+v", p)
	}
	if !contains(r.Report.Hints, "置信度 0.75") {
		t.Errorf("低置信度短语应进提示：%v", r.Report.Hints)
	}
	// 读错的字仍然拿到实测时间：第 4 个单元 0.6–0.8 秒。
	if tok := r.Tokens[3]; tok.Interpolated || !near(tok.Start, 0.6) || !near(tok.End, 0.8) {
		t.Errorf("读错的字应保留实测时间：%+v", tok)
	}
}

func TestBuildOmittedWordInPhraseIsProblem(t *testing.T) {
	s := mustParse(t, "我们明天@{when}早上八点@{/when}出发去北京看看那里的风景和朋友们。")
	r := mustBuild(t, s, heard("我们明天出发去北京看看那里的风景和朋友们", 0, 0.2), Options{})
	if r.Report.OK || !contains(r.Report.Problems, "短语 when") {
		t.Fatalf("短语被漏读应判为问题：%v", r.Report.Problems)
	}
	p := r.Timeline.Phrases[0]
	if p.AlignmentSource != SourceInterpolated || p.Confidence != 0 {
		t.Errorf("漏读短语应为插值、置信度 0：%+v", p)
	}
	// 漏读的四个字被挤在「天」与「出」之间的零宽缝里，起止都在 0.8 秒。
	if p.StartMs != 800 || p.EndMs != 800 || p.EndFrameExclusive != p.StartFrame+1 {
		t.Errorf("插值位置不符：%+v", p)
	}
	if r.Report.Labels[LabelOmission] != 4 {
		t.Errorf("应有 4 个 source-omission：%v", r.Report.Labels)
	}
}

func TestBuildInterpolatesAcrossGapByCharCount(t *testing.T) {
	s := mustParse(t, "甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉。")
	items := heard("甲乙丙丁戊己庚辛壬癸", 0, 0.1)
	items = append(items, heard("寅卯辰巳午未申酉", 1.6, 0.1)...)
	r := mustBuild(t, s, items, Options{})
	a, b := r.Tokens[10], r.Tokens[11]
	if !a.Interpolated || !b.Interpolated || !near(a.Start, 1.0) || !near(b.End, 1.6) || !near(a.End, 1.3) {
		t.Fatalf("漏读的两个字应平分 1.0–1.6 秒的空隙：%+v %+v", a, b)
	}
}

func TestBuildInsertedTokenIsHint(t *testing.T) {
	s := mustParse(t, "@{p}今天我们讲对齐@{/p}。")
	items := heard("今天", 0, 0.2)
	items = append(items, Item{Text: "嗯", Start: 0.4, End: 0.6})
	items = append(items, heard("我们讲对齐", 0.6, 0.2)...)
	r := mustBuild(t, s, items, Options{})
	if !r.Report.OK {
		t.Fatalf("多读不应判为问题：%v", r.Report.Problems)
	}
	if r.Report.Labels[LabelInsertion] != 1 || !contains(r.Report.Hints, "配音里多出「嗯」（0.40 秒）") {
		t.Fatalf("多读应进提示：%v %v", r.Report.Labels, r.Report.Hints)
	}
	if p := r.Timeline.Phrases[0]; p.Confidence != 1 || p.EndMs != 1600 {
		t.Errorf("短语不受多读影响：%+v", p)
	}
}

func TestBuildDigitsSpokenAsChinese(t *testing.T) {
	s := mustParse(t, "@{year}<2024|二零二四>年@{/year}发布了新版本。")
	r := mustBuild(t, s, heard("二零二四年发布了新版本", 2, 0.1), Options{})
	if !r.Report.OK || r.Report.MatchRate != 1 {
		t.Fatalf("改写的数字应逐字精确匹配：%+v", r.Report)
	}
	if r.Cues[0].Text != "2024年发布了新版本。" {
		t.Errorf("字幕应显示终稿原文：%q", r.Cues[0].Text)
	}
	p := r.Timeline.Phrases[0]
	if p.SourceText != "2024年" || p.StartMs != 2000 || p.EndMs != 2500 {
		t.Errorf("短语不符：%+v", p)
	}
}

func TestBuildSpelledAcronymMerges(t *testing.T) {
	s := mustParse(t, "调用<API|A P I>接口完成对齐任务。")
	r := mustBuild(t, s, heard("调用API接口完成对齐任务", 0, 0.1), Options{})
	if r.Report.Labels[LabelMerge] != 3 || !r.Report.OK {
		t.Fatalf("A P I 对 API 应为 merge：%v %v", r.Report.Labels, r.Report.Problems)
	}
	if !near(r.Tokens[2].Start, 0.2) || !near(r.Tokens[4].End, 0.3) || !near(r.Tokens[3].Start, 0.2+0.1/3) {
		t.Errorf("合并分组的首尾应贴住窗口：%+v %+v", r.Tokens[2], r.Tokens[4])
	}
}

func TestBuildLowMatchRateIsProblem(t *testing.T) {
	s := mustParse(t, "今天天气很好。")
	r := mustBuild(t, s, heard("明年风雨太差", 0, 0.1), Options{})
	if r.Report.OK || !contains(r.Report.Problems, "匹配率") {
		t.Fatalf("匹配率过低应判为问题：%v", r.Report.Problems)
	}
}

func TestBuildEmptyEvidenceIsProblem(t *testing.T) {
	r := mustBuild(t, mustParse(t, "你好。"), nil, Options{})
	if r.Report.OK || !contains(r.Report.Problems, "没有任何可用") {
		t.Fatalf("空对齐结果应判为问题：%v", r.Report.Problems)
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEvidenceShapes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, body string
		want       []Item
	}{
		{"规范形状", `[{"text":"你","start":0.1,"end":0.2},{"text":"好","start":0.2,"end":0.35}]`,
			[]Item{{Text: "你", Start: 0.1, End: 0.2}, {Text: "好", Start: 0.2, End: 0.35}}},
		{"funasr", `[{"key":"voice","text":"你 好","timestamp":[[100,200],[200,350]]}]`,
			[]Item{{Text: "你", Start: 0.1, End: 0.2}, {Text: "好", Start: 0.2, End: 0.35}}},
		{"嵌套与毫秒字段", `{"language":"zh","segments":[{"word":"你","start_ms":100,"end_ms":200}]}`,
			[]Item{{Text: "你", Start: 0.1, End: 0.2}}},
		{"start_time", `{"items":[{"char":"你","start_time":0.1,"end_time":0.2}]}`,
			[]Item{{Text: "你", Start: 0.1, End: 0.2}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := LoadEvidence(writeFile(t, dir, "a.json", tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(tc.want) {
				t.Fatalf("条数 %d，期望 %d", len(items), len(tc.want))
			}
			for i := range items {
				if items[i].Text != tc.want[i].Text || abs64(items[i].Start-tc.want[i].Start) > 1e-9 || abs64(items[i].End-tc.want[i].End) > 1e-9 {
					t.Errorf("第 %d 条 = %+v，期望 %+v", i, items[i], tc.want[i])
				}
			}
		})
	}
}

func near(a, b float64) bool { return abs64(a-b) < 1e-9 }

func abs64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestLoadEvidenceRejectsUnknownAndBadShapes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ body, want string }{
		{`{"result":{"foo":1},"text":"你好"}`, "键：result, text"},
		{`[{"text":"你 好","timestamp":[[0,100]]}]`, "无法逐一对应"},
		{`[{"text":"你","start":0.3,"end":0.1}]`, "起止时间不合法"},
		{`[{"text":"你","start":100,"end":30000}]`, "像是毫秒"},
	}
	for _, tc := range cases {
		_, err := LoadEvidence(writeFile(t, dir, "bad.json", tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：错误 = %v，期望包含 %q", tc.body, err, tc.want)
		}
		if err != nil && strings.Contains(tc.want, "键") && !strings.Contains(err.Error(), `"start"`) {
			t.Errorf("认不出时应给出规范形状：%v", err)
		}
	}
}

func TestSegmentsAreShiftedByOffset(t *testing.T) {
	dir := t.TempDir()
	first := writeFile(t, dir, "align-01.json", `[{"text":"你","start":0,"end":0.2},{"text":"好","start":0.2,"end":0.4}]`)
	second := writeFile(t, dir, "align-02.json", `[{"text":"世","start":0,"end":0.2},{"text":"界","start":0.2,"end":0.4}]`)
	seg2, err := ParseSegmentFlag(second + "@10.5")
	if err != nil {
		t.Fatal(err)
	}
	items, err := LoadSegments([]Segment{{Path: first}, seg2})
	if err != nil {
		t.Fatal(err)
	}
	r := mustBuild(t, mustParse(t, "你好。世界。"), items, Options{})
	if !r.Report.OK || r.Cues[1].StartMs != 10500 || r.Cues[1].EndMs != 10900 || r.Report.TotalFrames != beat.TotalFrames(10.9, 30) {
		t.Fatalf("第二段应平移 10.5 秒：%+v %+v", r.Cues, r.Report)
	}
	// 偏移写错导致两段重叠，判为问题。
	seg2.Offset = 0.1
	items, err = LoadSegments([]Segment{{Path: first}, seg2})
	if err != nil {
		t.Fatal(err)
	}
	r = mustBuild(t, mustParse(t, "你好。世界。"), items, Options{})
	if r.Report.OK || !contains(r.Report.Problems, "偏移写错") {
		t.Fatalf("重叠的分段应判为问题：%v", r.Report.Problems)
	}
	for _, bad := range []string{"a.json@x", "@1", "a.json@-1"} {
		if _, err := ParseSegmentFlag(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}

func TestAdjustLayer(t *testing.T) {
	script := "@{a}你好@{/a}，@{b}世界@{/b}。"
	items := heard("你好世界", 1, 0.25) // 1.0–2.0 秒，全片 60 帧
	cases := []struct {
		name string
		adj  []Adjustment
		want string
	}{
		{"未知短语", []Adjustment{{PhraseID: "x", Edge: "start", Frame: 1, Reason: "r"}}, "没有这个短语"},
		{"边界名", []Adjustment{{PhraseID: "a", Edge: "middle", Frame: 1, Reason: "r"}}, "edge 只能是"},
		{"空理由", []Adjustment{{PhraseID: "a", Edge: "start", Frame: 1, Reason: " "}}, "reason 不能为空"},
		{"重复", []Adjustment{{PhraseID: "a", Edge: "start", Frame: 31, Reason: "r"}, {PhraseID: "a", Edge: "start", Frame: 32, Reason: "r"}}, "重复修正"},
		{"越界", []Adjustment{{PhraseID: "a", Edge: "start", Frame: 60, Reason: "r"}}, "超出"},
		{"负帧", []Adjustment{{PhraseID: "a", Edge: "end", Frame: -1, Reason: "r"}}, "超出"},
		{"起点不早于终点", []Adjustment{{PhraseID: "a", Edge: "start", Frame: 45, Reason: "r"}}, "不早于终点"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(mustParse(t, script), items, Options{FPS: 30, Adjust: &AdjustFile{Schema: 1, Adjustments: tc.adj}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 = %v，期望包含 %q", err, tc.want)
			}
		})
	}

	r := mustBuild(t, mustParse(t, script), items, Options{Adjust: &AdjustFile{Schema: 1, Adjustments: []Adjustment{
		{PhraseID: "b", Edge: "end", Frame: 60, Reason: "尾音被对齐截短"},
	}}})
	a, b := r.Timeline.Phrases[0], r.Timeline.Phrases[1]
	if a.AlignmentSource != SourceForced || a.Reviewed {
		t.Errorf("未修正的短语不应受影响：%+v", a)
	}
	if b.AlignmentSource != SourceAdjusted || !b.Reviewed || b.EndFrameExclusive != 60 || b.EndMs != 2000 || b.StartFrame != 45 {
		t.Errorf("修正后的短语不符：%+v", b)
	}
	if stat := r.Report.Phrases[1]; stat.MeasuredEndFrameExclusive != 60 || stat.EndFrameExclusive != 60 {
		t.Errorf("报告应同时给出实测与最终帧：%+v", stat)
	}
}

func TestLoadAdjustRejectsUnknownFieldsAndSchema(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadAdjust(writeFile(t, dir, "a.json", `{"schema":1,"adjustments":[{"phrase_id":"a","edge":"start","frame":1,"reason":"r","note":1}]}`)); err == nil {
		t.Error("未知字段应被拒绝")
	}
	if _, err := LoadAdjust(writeFile(t, dir, "b.json", `{"schema":2,"adjustments":[]}`)); err == nil {
		t.Error("schema 不是 1 应被拒绝")
	}
}

func TestExistingTimelineFieldsArePreserved(t *testing.T) {
	existing := &Timeline{Schema: 1, FPS: 30, Phrases: []Phrase{
		{PhraseID: "a", Reviewed: true, VisibleEventIDs: []string{"title-1"}, StartFrame: 999},
		{PhraseID: "gone", VisibleEventIDs: []string{"card-2"}},
	}}
	r := mustBuild(t, mustParse(t, "@{a}你好@{/a}，@{b}世界@{/b}。"), heard("你好世界", 0, 0.1), Options{Existing: existing})
	a, b := r.Timeline.Phrases[0], r.Timeline.Phrases[1]
	if !a.Reviewed || len(a.VisibleEventIDs) != 1 || a.VisibleEventIDs[0] != "title-1" || a.StartFrame != 0 {
		t.Errorf("应保留 reviewed 与 visible_event_ids、但时间按实测重算：%+v", a)
	}
	if b.Reviewed || b.VisibleEventIDs == nil || len(b.VisibleEventIDs) != 0 {
		t.Errorf("新短语不应继承别的短语：%+v", b)
	}
	if len(r.Timeline.Phrases) != 2 || !contains(r.Report.Hints, "短语 gone 已不在终稿里") || !contains(r.Report.Hints, "card-2") {
		t.Errorf("删掉的短语应移除并提示：%v", r.Report.Hints)
	}
}

func TestLoadExistingTimeline(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadExistingTimeline(filepath.Join(dir, "none.json")); got != nil || err != nil {
		t.Errorf("文件不存在应返回 nil：%v %v", got, err)
	}
	if _, err := LoadExistingTimeline(writeFile(t, dir, "bad.json", `{"phrases":"x"}`)); err == nil {
		t.Error("读不懂的已有时间轴应报错，不能被覆盖")
	}
}

func TestCueSegmentation(t *testing.T) {
	long := "第一部分说的是背景和来由，第二部分说的是方法与步骤，第三部分说的是结果和后续的计划安排。"
	script := "开头一句！「引号里的话。」然后\n\n" + long
	s := mustParse(t, script)
	var spoken strings.Builder
	for _, tok := range s.Tokens {
		spoken.WriteString(tok.Text)
	}
	r := mustBuild(t, s, heard(spoken.String(), 0, 0.1), Options{})
	var texts []string
	for _, c := range r.Cues {
		texts = append(texts, c.Text)
	}
	want := []string{
		"开头一句！",
		"「引号里的话。」",
		"然后",
		"第一部分说的是背景和来由，第二部分说的是方法与步骤，",
		"第三部分说的是结果和后续的计划安排。",
	}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("字幕切分 = %q\n期望 %q", texts, want)
	}
	for i := 1; i < len(r.Cues); i++ {
		if r.Cues[i].StartMs < r.Cues[i-1].EndMs || r.Cues[i].Index != i+1 {
			t.Errorf("字幕 %d 与前一条重叠或编号不连续：%+v", i+1, r.Cues[i])
		}
	}
	srt := FormatSRT(r.Cues[:1])
	if srt != "1\n00:00:00,000 --> 00:00:00,400\n开头一句！\n" {
		t.Errorf("SRT 格式不符：%q", srt)
	}
}

func TestCueClampsOverlapAndAbsorbsSilentText(t *testing.T) {
	s := mustParse(t, "甲。<|嗯>乙。<图|>")
	items := []Item{{Text: "甲", Start: 0, End: 0.5}, {Text: "嗯", Start: 0.4, End: 0.6}, {Text: "乙", Start: 0.6, End: 0.8}}
	r := mustBuild(t, s, items, Options{})
	if len(r.Cues) != 2 || r.Cues[1].Text != "乙。图" {
		t.Fatalf("只显示与只朗读的内容应并进相邻字幕：%+v", r.Cues)
	}
	if r.Cues[1].StartMs != 500 || r.Cues[0].EndMs != 500 {
		t.Errorf("重叠应被夹住：%+v", r.Cues)
	}
}

func TestPhraseFramesUseBeatFrameOf(t *testing.T) {
	s := mustParse(t, "@{p}一二三@{/p}。")
	items := []Item{{Text: "一", Start: 0.517, End: 0.6}, {Text: "二", Start: 0.6, End: 0.7}, {Text: "三", Start: 0.7, End: 0.718}}
	for _, fps := range []int{24, 25, 30, 60} {
		r := mustBuild(t, s, items, Options{FPS: fps})
		p := r.Timeline.Phrases[0]
		if p.StartFrame != beat.FrameOf(0.517, fps) || p.EndFrameExclusive != max(beat.FrameOf(0.718, fps), p.StartFrame+1) {
			t.Errorf("fps %d：帧号 %d–%d 与 beat.FrameOf 不一致", fps, p.StartFrame, p.EndFrameExclusive)
		}
	}
}

func TestDisplayOnlyPhraseIsProblem(t *testing.T) {
	r := mustBuild(t, mustParse(t, "你好@{pic}<【图】|>@{/pic}世界。"), heard("你好世界", 0, 0.1), Options{})
	if r.Report.OK || !contains(r.Report.Problems, "没有朗读内容") {
		t.Fatalf("没有朗读内容的短语应判为问题：%v", r.Report.Problems)
	}
	if p := r.Timeline.Phrases[0]; p.StartMs != 200 || p.EndFrameExclusive != p.StartFrame+1 {
		t.Errorf("只显示的短语应落在前一个朗读单元的终点：%+v", p)
	}
}

func TestMatchingIsDeterministicOnLongInput(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("我们今天讨论强制对齐的实现细节，")
	}
	b.WriteString("完。")
	s := mustParse(t, b.String())
	var spoken strings.Builder
	for i, tok := range s.Tokens {
		if i%97 == 5 { // 隔一段漏读一个字
			continue
		}
		spoken.WriteString(tok.Text)
	}
	items := heard(spoken.String(), 0, 0.05)
	first := mustBuild(t, s, items, Options{})
	second := mustBuild(t, s, items, Options{})
	if first.Report.Labels[LabelOmission] == 0 || first.Report.MatchRate < 0.95 {
		t.Fatalf("长文本匹配不符：%v %v", first.Report.Labels, first.Report.MatchRate)
	}
	for i := range first.Tokens {
		if first.Tokens[i].Start != second.Tokens[i].Start || first.Tokens[i].Label != second.Tokens[i].Label {
			t.Fatal("两次构建结果不同")
		}
	}
}
