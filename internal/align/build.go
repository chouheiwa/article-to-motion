package align

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// 判据。
const (
	// MinMatchRate 是 exact+split+merge 占全部朗读单元的下限，低于它说明送进对齐的
	// 文本不是这份终稿，或者配音与终稿差得太多。
	MinMatchRate = 0.85
	// HintConfidence 以下的短语进 hints，提示人工复核。
	HintConfidence = 0.9
	// segmentSlack 是相邻两段对齐结果允许的重叠（秒）。超过它说明 --align 的偏移写错了。
	segmentSlack = 0.05
	// maxListed 是同类 hints 最多逐条列出的条数，其余汇总成一条。
	maxListed = 30
)

// Options 是 Build 的输入。
type Options struct {
	FPS int
	// Adjust 是修正层，可为 nil。
	Adjust *AdjustFile
	// Existing 是已有的短语时间轴，可为 nil。
	Existing *Timeline
}

// Result 是一次构建的全部产物。
type Result struct {
	Cues     []Cue
	Timeline Timeline
	Report   Report
	Tokens   []TimedToken
}

// PhraseStat 是报告里单个短语的对齐情况。
type PhraseStat struct {
	PhraseID                  string         `json:"phrase_id"`
	Confidence                float64        `json:"confidence"`
	AlignmentSource           string         `json:"alignment_source"`
	Labels                    map[string]int `json:"labels"`
	MeasuredStartFrame        int            `json:"measured_start_frame"`
	MeasuredEndFrameExclusive int            `json:"measured_end_frame_exclusive"`
	StartFrame                int            `json:"start_frame"`
	EndFrameExclusive         int            `json:"end_frame_exclusive"`
}

// Irregular 是一处非精确匹配，带终稿位置，方便回去查。
type Irregular struct {
	Label   string `json:"label"`
	Script  string `json:"script,omitempty"`
	Heard   string `json:"heard,omitempty"`
	Line    int    `json:"line,omitempty"`
	Col     int    `json:"col,omitempty"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
}

// Report 是 align-report.json。OK 只由 Problems 决定。
type Report struct {
	SchemaVersion  int            `json:"schema_version"`
	OK             bool           `json:"ok"`
	FPS            int            `json:"fps"`
	Tokens         int            `json:"tokens"`
	EvidenceTokens int            `json:"evidence_tokens"`
	Labels         map[string]int `json:"labels"`
	MatchRate      float64        `json:"match_rate"`
	Interpolated   int            `json:"interpolated_tokens"`
	SpeechStartMs  int64          `json:"speech_start_ms"`
	SpeechEndMs    int64          `json:"speech_end_ms"`
	TotalFrames    int            `json:"total_frames"`
	Cues           int            `json:"cues"`
	Phrases        []PhraseStat   `json:"phrases"`
	Irregular      []Irregular    `json:"irregular"`
	Problems       []string       `json:"problems"`
	Hints          []string       `json:"hints,omitempty"`
}

// ReportSchemaVersion 是 Report 的结构版本。
const ReportSchemaVersion = 1

// Build 把对齐结果映射回终稿，产出字幕、短语时间轴与报告。修正层不合格时返回错误、不产出；
// 对齐质量问题只写进 Report.Problems，产物照常给出，便于对照排查。
func Build(script *Script, items []Item, opts Options) (Result, error) {
	if opts.FPS <= 0 {
		return Result{}, fmt.Errorf("fps 必须是正整数，收到 %d", opts.FPS)
	}
	if len(script.Tokens) == 0 {
		return Result{}, fmt.Errorf("终稿没有任何朗读内容")
	}
	report := Report{SchemaVersion: ReportSchemaVersion, FPS: opts.FPS, Labels: map[string]int{}, Problems: []string{}, Phrases: []PhraseStat{}, Irregular: []Irregular{}}
	checkSegmentOrder(items, &report)

	evidence := evidenceTokens(items)
	source := make([]string, len(script.Tokens))
	for i, t := range script.Tokens {
		source[i] = t.Norm
	}
	target := make([]string, len(evidence))
	for i, e := range evidence {
		target[i] = e.Norm
	}
	groups := matchTokens(source, target)
	timed := locate(script.Tokens, evidence, groups)
	cues := buildCues(script, timed)

	report.Tokens, report.EvidenceTokens, report.Cues = len(timed), len(evidence), len(cues)
	matched := 0
	for _, t := range timed {
		report.Labels[t.Label]++
		if t.Label == LabelExact || t.Label == LabelSplit || t.Label == LabelMerge {
			matched++
		}
		if t.Interpolated {
			report.Interpolated++
		}
	}
	report.MatchRate = round3(float64(matched) / float64(len(timed)))
	describeGroups(groups, timed, evidence, &report)

	if len(evidence) == 0 {
		report.Problems = append(report.Problems, "对齐结果里没有任何可用的字或词：确认对齐工具跑完了，且输入是实际送进 TTS 的文本")
	} else {
		first, last := evidence[0].start, evidence[0].end
		for _, e := range evidence {
			first, last = min(first, e.start), max(last, e.end)
		}
		report.SpeechStartMs, report.SpeechEndMs = toMs(first), toMs(last)
		if report.SpeechEndMs <= report.SpeechStartMs {
			report.Problems = append(report.Problems, "对齐结果的总时间跨度为空")
		}
	}
	if len(timed) > 0 {
		lastEnd := timed[0].End
		for _, t := range timed {
			lastEnd = max(lastEnd, t.End)
		}
		report.TotalFrames = beat.TotalFrames(float64(toMs(lastEnd))/1000, opts.FPS)
	}
	if report.MatchRate < MinMatchRate {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"精确匹配率 %.1f%% 低于 %.0f%%：送进对齐的文本可能不是 am align text 产出的那份，或配音与终稿差异过大",
			report.MatchRate*100, MinMatchRate*100))
	}

	phrases := timePhrases(script, timed, cues, opts.FPS)
	final, adjusted, err := applyAdjustments(phrases, opts.Adjust, report.TotalFrames)
	if err != nil {
		return Result{}, err
	}
	timeline := Timeline{Schema: TimelineSchema, FPS: opts.FPS, Phrases: make([]Phrase, 0, len(phrases))}
	existing := map[string]Phrase{}
	if opts.Existing != nil {
		for _, p := range opts.Existing.Phrases {
			existing[p.PhraseID] = p
		}
	}
	for _, p := range phrases {
		r := final[p.span.ID]
		out := Phrase{
			PhraseID: p.span.ID, SubtitleIndex: p.subtitle, SourceText: p.text, SemanticOrder: p.span.Order,
			StartMs: toMs(p.start), EndMs: toMs(p.end), StartFrame: r[0], EndFrameExclusive: r[1],
			AlignmentSource: p.source, Confidence: p.confidence, VisibleEventIDs: []string{},
		}
		if old, ok := existing[p.span.ID]; ok {
			out.Reviewed = old.Reviewed
			if old.VisibleEventIDs != nil {
				out.VisibleEventIDs = old.VisibleEventIDs
			}
		}
		if adjusted[p.span.ID] {
			out.AlignmentSource, out.Reviewed = SourceAdjusted, true
			out.StartMs = frameMs(r[0], opts.FPS)
			out.EndMs = frameMs(r[1], opts.FPS)
		}
		timeline.Phrases = append(timeline.Phrases, out)
		report.Phrases = append(report.Phrases, PhraseStat{
			PhraseID: p.span.ID, Confidence: p.confidence, AlignmentSource: out.AlignmentSource, Labels: p.labels,
			MeasuredStartFrame: p.measuredFrom, MeasuredEndFrameExclusive: p.measuredTo,
			StartFrame: r[0], EndFrameExclusive: r[1],
		})
		judgePhrase(p, &report)
	}
	if opts.Existing != nil {
		current := map[string]bool{}
		for _, p := range phrases {
			current[p.span.ID] = true
		}
		for _, old := range opts.Existing.Phrases {
			if !current[old.PhraseID] {
				note := ""
				if len(old.VisibleEventIDs) > 0 {
					note = fmt.Sprintf("，它绑定的可见事件 %v 需要改绑", old.VisibleEventIDs)
				}
				report.Hints = append(report.Hints, fmt.Sprintf("短语 %s 已不在终稿里，已从时间轴移除%s", old.PhraseID, note))
			}
		}
	}
	report.OK = len(report.Problems) == 0
	return Result{Cues: cues, Timeline: timeline, Report: report, Tokens: timed}, nil
}

func frameMs(frame, fps int) int64 { return toMs(float64(frame) / float64(fps)) }

// judgePhrase 判定短语级问题与提示。
func judgePhrase(p phraseTiming, report *Report) {
	where := fmt.Sprintf("短语 %s「%s」（终稿第 %d 行）", p.span.ID, p.text, p.span.Line)
	switch {
	case p.tok1 == p.tok0:
		report.Problems = append(report.Problems, where+"没有朗读内容，无法从配音定时；改成朗读侧有字，或去掉短语标记")
	case p.omitted:
		report.Problems = append(report.Problems, where+"里有字在对齐结果中找不到：TTS 漏读或对齐丢了，重新合成或检查送进对齐的文本")
	case p.allInterp:
		report.Problems = append(report.Problems, where+"全部时间都是插值得来，没有任何实测依据")
	case p.confidence < HintConfidence:
		report.Hints = append(report.Hints, fmt.Sprintf("%s置信度 %.2f，低于 %.2f，复核它的起止帧", where, p.confidence, HintConfidence))
	}
}

// describeGroups 把非精确的分组写进 irregular，并把多读、读错列进 hints。
func describeGroups(groups []group, timed []TimedToken, evidence []evToken, report *Report) {
	var insertions, replacements []string
	for _, g := range groups {
		if g.label == LabelExact {
			continue
		}
		item := Irregular{Label: g.label}
		var scriptText []string
		for _, t := range timed[g.s0:g.s1] {
			scriptText = append(scriptText, t.Text)
		}
		item.Script = strings.Join(scriptText, "")
		var heard []string
		for _, e := range evidence[g.e0:g.e1] {
			heard = append(heard, e.Text)
		}
		item.Heard = strings.Join(heard, " ")
		switch {
		case g.s1 > g.s0:
			item.Line, item.Col = timed[g.s0].Line, timed[g.s0].Col
			item.StartMs, item.EndMs = toMs(timed[g.s0].Start), toMs(timed[g.s1-1].End)
		case g.e1 > g.e0:
			item.StartMs, item.EndMs = toMs(evidence[g.e0].start), toMs(evidence[g.e1-1].end)
		}
		report.Irregular = append(report.Irregular, item)
		at := fmt.Sprintf("%.2f 秒", float64(item.StartMs)/1000)
		switch g.label {
		case LabelInsertion:
			report.Labels[LabelInsertion] += g.e1 - g.e0
			insertions = append(insertions, fmt.Sprintf("配音里多出「%s」（%s），终稿没有", item.Heard, at))
		case LabelReplace:
			replacements = append(replacements, fmt.Sprintf("终稿「%s」（第 %d 行第 %d 列）对上的是「%s」（%s）", item.Script, item.Line, item.Col, item.Heard, at))
		}
	}
	report.Hints = append(report.Hints, capList(insertions, "处多读")...)
	report.Hints = append(report.Hints, capList(replacements, "处读法不同")...)
}

func capList(items []string, kind string) []string {
	if len(items) <= maxListed {
		return items
	}
	out := append([]string{}, items[:maxListed]...)
	return append(out, fmt.Sprintf("另有 %d %s，见报告 irregular", len(items)-maxListed, kind))
}

// checkSegmentOrder 检查多段对齐结果平移后是否首尾相接：后一段的起点不能明显早于前一段的终点。
func checkSegmentOrder(items []Item, report *Report) {
	type bounds struct {
		first, last float64
		seen        bool
	}
	var segs []bounds
	for _, item := range items {
		for len(segs) <= item.Segment {
			segs = append(segs, bounds{})
		}
		b := &segs[item.Segment]
		if !b.seen {
			b.first, b.last, b.seen = item.Start, item.End, true
		}
		b.first, b.last = min(b.first, item.Start), max(b.last, item.End)
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].seen && segs[i-1].seen && segs[i].first < segs[i-1].last-segmentSlack {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"第 %d 段对齐结果平移后起点 %.3f 秒早于第 %d 段终点 %.3f 秒：--align 的偏移写错了，偏移应是该段音频在整条配音里的起点",
				i+1, segs[i].first, i, segs[i-1].last))
		}
	}
}

// WriteJSON 原子写出带缩进的 JSON。
func WriteJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(body, '\n'), 0o644)
}

// SortedLabels 返回报告里出现过的标签，按字典序，供命令行摘要输出。
func (r Report) SortedLabels() []string {
	labels := make([]string, 0, len(r.Labels))
	for label := range r.Labels {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}
