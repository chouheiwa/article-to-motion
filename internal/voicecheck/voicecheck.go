// Package voicecheck 把字幕时间轴与配音里实际有声的区间逐条比对。
//
// 字幕时间来自 TTS 自带的时间戳、ASR 识别或强制对齐，三者都可能整体偏移或
// 局部错位：实测 TTS 自带的 SRT 会比实际发声早约 0.4 秒。字幕、镜头切点和
// 画面节拍全都挂在这条时间轴上，它错了，下游每一步都跟着错，而且没有一个
// 环节会报错。
//
// 这里只用最朴素的信号——音频能量——做独立参照：它不知道文字，但能确定
// 「这一刻有没有声音」。与字幕来源完全无关，所以能拿来交叉校验任何一种来源。
package voicecheck

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

// Segment 是一段连续有声的区间（秒）。
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Options 控制有声区间的判定与比对容差。
type Options struct {
	// Window 是响度采样窗长（秒）。
	Window float64
	// BelowPeakDB：电平低于「全片最响窗口 - BelowPeakDB」视为静音。
	// 用相对峰值而不是绝对阈值：不同 TTS 的输出电平相差十几 dB。
	BelowPeakDB float64
	// MinGap 是被当作停顿的最短静音；更短的静音（字间、爆破音前）并入有声区间。
	MinGap float64
	// MinSpeech 是被当作发声的最短区间；更短的视为咔哒声等噪声。
	MinSpeech float64
	// Tolerance 是字幕边界与发声边界允许的偏差（秒）。
	Tolerance float64
	// MinUncovered 是报告「有声却没有字幕」的最短区间。
	MinUncovered float64
}

// 缺省值。MinGap 取 0.12 秒：中文 TTS 句内逗号停顿通常在 0.15 秒以上，字间
// 间隙远短于此。Tolerance 取 0.2 秒：人眼对字幕早出现 0.2 秒以内基本无感。
var Defaults = Options{Window: 0.01, BelowPeakDB: 35, MinGap: 0.12, MinSpeech: 0.05, Tolerance: 0.2, MinUncovered: 0.3}

func (o Options) withDefaults() Options {
	if o.Window <= 0 {
		o.Window = Defaults.Window
	}
	if o.BelowPeakDB <= 0 {
		o.BelowPeakDB = Defaults.BelowPeakDB
	}
	if o.MinGap <= 0 {
		o.MinGap = Defaults.MinGap
	}
	if o.MinSpeech <= 0 {
		o.MinSpeech = Defaults.MinSpeech
	}
	if o.Tolerance <= 0 {
		o.Tolerance = Defaults.Tolerance
	}
	if o.MinUncovered <= 0 {
		o.MinUncovered = Defaults.MinUncovered
	}
	return o
}

// CueResult 是一条字幕的比对结果。StartDelta = 字幕开始 - 其中首个发声开始，
// 负数表示字幕早于声音；EndDelta = 字幕结束 - 其中最后发声结束，正数表示
// 字幕比声音停留得久。没有覆盖任何发声时两者为空。
type CueResult struct {
	Index       int      `json:"index"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Text        string   `json:"text"`
	SpeechStart *float64 `json:"speech_start,omitempty"`
	SpeechEnd   *float64 `json:"speech_end,omitempty"`
	StartDelta  *float64 `json:"start_delta,omitempty"`
	EndDelta    *float64 `json:"end_delta,omitempty"`
	// OwnsOnset 表示首段发声从本条字幕开始，而不是从前一条延续过来。
	// 只有这样的字幕，StartDelta 才反映时间戳偏移，才计入整体偏移中位数。
	OwnsOnset bool `json:"owns_onset"`
}

// Report 是比对的机器可读结果。OK 只由 Problems 决定。
type Report struct {
	SchemaVersion int         `json:"schema_version"`
	OK            bool        `json:"ok"`
	ThresholdDB   float64     `json:"threshold_db"`
	Speech        []Segment   `json:"speech"`
	Cues          []CueResult `json:"cues"`
	// MedianStartDelta 是全部字幕 StartDelta 的中位数，用来识别整体偏移。
	MedianStartDelta *float64 `json:"median_start_delta,omitempty"`
	Problems         []string `json:"problems"`
	Hints            []string `json:"hints,omitempty"`
}

// ReportSchemaVersion 是 Report 的结构版本。
const ReportSchemaVersion = 1

// WriteJSON 原子写出报告。
func (r Report) WriteJSON(target string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(target, append(body, '\n'), 0o644)
}

// quietestSpeechDB 是全片最响窗口的下限：再安静就视为整段没有发声。
// 正常配音的峰值在 -20 dBFS 以上，-60 dBFS 已是录音底噪的量级。
const quietestSpeechDB = -60.0

// Speech 从逐窗电平里切出有声区间，返回区间与所用阈值。
func Speech(levels []float64, opts Options) ([]Segment, float64) {
	opts = opts.withDefaults()
	peak := math.Inf(-1)
	for _, level := range levels {
		peak = math.Max(peak, level)
	}
	threshold := peak - opts.BelowPeakDB
	// 阈值相对峰值，全片静音时峰值本身就是底噪，不设下限会把底噪当成发声。
	if peak < quietestSpeechDB {
		return nil, threshold
	}
	var raw []Segment
	start := -1
	for i, level := range levels {
		loud := level >= threshold
		switch {
		case loud && start < 0:
			start = i
		case !loud && start >= 0:
			raw = append(raw, Segment{float64(start) * opts.Window, float64(i) * opts.Window})
			start = -1
		}
	}
	if start >= 0 {
		raw = append(raw, Segment{float64(start) * opts.Window, float64(len(levels)) * opts.Window})
	}
	var merged []Segment
	for _, segment := range raw {
		if n := len(merged); n > 0 && segment.Start-merged[n-1].End < opts.MinGap {
			merged[n-1].End = segment.End
			continue
		}
		merged = append(merged, segment)
	}
	speech := merged[:0:0]
	for _, segment := range merged {
		if segment.End-segment.Start >= opts.MinSpeech {
			speech = append(speech, round(segment))
		}
	}
	return speech, threshold
}

func round(s Segment) Segment {
	return Segment{math.Round(s.Start*1000) / 1000, math.Round(s.End*1000) / 1000}
}

// Check 逐条比对字幕与有声区间。
func Check(cues []srt.Cue, speech []Segment, threshold float64, opts Options) Report {
	opts = opts.withDefaults()
	report := Report{SchemaVersion: ReportSchemaVersion, ThresholdDB: math.Round(threshold*10) / 10,
		Speech: speech, Cues: []CueResult{}, Problems: []string{}}
	if report.Speech == nil {
		report.Speech = []Segment{}
	}
	if len(speech) == 0 {
		report.Problems = append(report.Problems, "音频里没有检测到任何发声")
		return report
	}
	var deltas []float64
	for _, cue := range cues {
		if cue.EndSeconds <= cue.StartSeconds {
			report.Hints = append(report.Hints, fmt.Sprintf(
				"第 %d 条字幕时长为 0（%.3f 秒），未参与比对；若不是纯标点条目，检查时间戳来源", cue.Index, cue.StartSeconds))
			continue
		}
		result := checkCue(&report, cue, speech, opts)
		if result.OwnsOnset {
			deltas = append(deltas, *result.StartDelta)
		}
		report.Cues = append(report.Cues, result)
	}
	checkUncovered(&report, cues, speech, opts)
	if len(deltas) > 0 {
		median := medianOf(deltas)
		report.MedianStartDelta = &median
		if math.Abs(median) > opts.Tolerance/2 {
			direction := "早"
			if median > 0 {
				direction = "晚"
			}
			report.Hints = append(report.Hints, fmt.Sprintf(
				"字幕整体比声音%s约 %.3f 秒：多条同向偏移说明时间戳来源有系统误差，优先整体校正而不是逐条修",
				direction, math.Abs(median)))
		}
	}
	report.OK = len(report.Problems) == 0
	return report
}

// checkCue 取字幕区间内的发声，比较两端。
//
// 字幕开始早于声音、或开始时声音早已响起，都判问题：前者让字幕先于语音
// 出现，后者让镜头切点落在句中。字幕结束晚于声音只进 hints：终稿字幕按可读
// 语义单元延长停留是 PROMPT 允许的做法。
func checkCue(report *Report, cue srt.Cue, speech []Segment, opts Options) CueResult {
	result := CueResult{Index: cue.Index, Start: cue.StartSeconds, End: cue.EndSeconds, Text: cue.Text}
	first, last := meaningfulOverlap(cue, speech, opts.Tolerance)
	if first < 0 {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"第 %d 条字幕（%.3f–%.3f 秒）整段落在静音里：%s", cue.Index, cue.StartSeconds, cue.EndSeconds, cue.Text))
		return result
	}
	speechStart, speechEnd := speech[first].Start, speech[last].End
	startDelta := roundMillis(cue.StartSeconds - speechStart)
	endDelta := roundMillis(cue.EndSeconds - speechEnd)
	result.SpeechStart, result.SpeechEnd = &speechStart, &speechEnd
	result.StartDelta, result.EndDelta = &startDelta, &endDelta
	result.OwnsOnset = !previousCueCovers(report, speechStart)
	switch {
	case startDelta < -opts.Tolerance:
		report.Problems = append(report.Problems, fmt.Sprintf(
			"第 %d 条字幕比声音早 %.3f 秒开始（字幕 %.3f 秒，发声 %.3f 秒）：%s",
			cue.Index, -startDelta, cue.StartSeconds, speechStart, cue.Text))
	case startDelta > opts.Tolerance && result.OwnsOnset:
		report.Problems = append(report.Problems, fmt.Sprintf(
			"第 %d 条字幕比声音晚 %.3f 秒开始（字幕 %.3f 秒，发声 %.3f 秒）：%s",
			cue.Index, startDelta, cue.StartSeconds, speechStart, cue.Text))
	}
	if endDelta > opts.Tolerance {
		report.Hints = append(report.Hints, fmt.Sprintf(
			"第 %d 条字幕在声音结束后又停留 %.3f 秒：若这是 TTS 或对齐直接产出的时间戳，说明它偏晚",
			cue.Index, endDelta))
	}
	return result
}

// meaningfulOverlap 找出与字幕真正重叠的首、末段发声，没有时返回 -1。
//
// 只擦到边的发声不算：上一句的尾音延伸进本条字幕开头几十毫秒时，若把它当成
// 本条的首段发声，本条「比声音早开始」就被掩盖了——那正是这个命令要抓的
// 系统性提前。所以首段要求在字幕开始后仍持续超过容差，末段要求在字幕结束前
// 已开始超过容差；都找不到时退回任意重叠，避免把很短的字幕误判成落在静音里。
func meaningfulOverlap(cue srt.Cue, speech []Segment, tolerance float64) (int, int) {
	first, last := -1, -1
	anyFirst, anyLast := -1, -1
	for i, segment := range speech {
		if segment.End <= cue.StartSeconds || segment.Start >= cue.EndSeconds {
			continue
		}
		if anyFirst < 0 {
			anyFirst = i
		}
		anyLast = i
		if first < 0 && segment.End > cue.StartSeconds+tolerance {
			first = i
		}
		if segment.Start < cue.EndSeconds-tolerance {
			last = i
		}
	}
	if first < 0 || last < 0 || first > last {
		return anyFirst, anyLast
	}
	return first, last
}

// previousCueCovers 报告 at 这一刻是否已被前面的字幕覆盖。连续发声被拆成
// 两条字幕时，后一条的开始本就落在发声中间，不是「晚了」。
func previousCueCovers(report *Report, at float64) bool {
	for _, previous := range report.Cues {
		if previous.Start <= at && at < previous.End {
			return true
		}
	}
	return false
}

// checkUncovered 找出有声但没有任何字幕覆盖的区间。
func checkUncovered(report *Report, cues []srt.Cue, speech []Segment, opts Options) {
	covered := make([]Segment, 0, len(cues))
	for _, cue := range cues {
		covered = append(covered, Segment{cue.StartSeconds, cue.EndSeconds})
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i].Start < covered[j].Start })
	for _, segment := range speech {
		cursor := segment.Start
		for _, cover := range covered {
			if cover.End <= cursor || cover.Start >= segment.End {
				continue
			}
			if cover.Start > cursor {
				reportGap(report, cursor, cover.Start, opts)
			}
			cursor = math.Max(cursor, cover.End)
			if cursor >= segment.End {
				break
			}
		}
		if cursor < segment.End {
			reportGap(report, cursor, segment.End, opts)
		}
	}
}

func reportGap(report *Report, start, end float64, opts Options) {
	if end-start >= opts.MinUncovered {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"%.3f–%.3f 秒有声音但没有字幕覆盖（%.3f 秒）", start, end, end-start))
	}
}

func roundMillis(value float64) float64 { return math.Round(value*1000) / 1000 }

func medianOf(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return roundMillis((sorted[mid-1] + sorted[mid]) / 2)
}
