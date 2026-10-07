package align

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
)

// 短语时间来源。
const (
	SourceForced       = "forced-alignment" // 首尾单元都是测出来的
	SourceInterpolated = "interpolated"     // 首或尾单元是插值出来的
	SourceAdjusted     = "adjusted"         // 被 phrase-adjust.json 改过边界
)

// Phrase 是 phrase-timeline.json 的一条。字段名由制作提示词第四阶段与 internal/reads 固定。
type Phrase struct {
	PhraseID          string   `json:"phrase_id"`
	SubtitleIndex     int      `json:"subtitle_index"`
	SourceText        string   `json:"source_text"`
	SemanticOrder     int      `json:"semantic_order"`
	StartMs           int64    `json:"start_ms"`
	EndMs             int64    `json:"end_ms"`
	StartFrame        int      `json:"start_frame"`
	EndFrameExclusive int      `json:"end_frame_exclusive"`
	AlignmentSource   string   `json:"alignment_source"`
	Confidence        float64  `json:"confidence"`
	Reviewed          bool     `json:"reviewed"`
	VisibleEventIDs   []string `json:"visible_event_ids"`
}

// Timeline 是 phrase-timeline.json。
type Timeline struct {
	Schema  int      `json:"schema"`
	FPS     int      `json:"fps"`
	Phrases []Phrase `json:"phrases"`
}

// TimelineSchema 是 phrase-timeline.json 的结构版本。
const TimelineSchema = 1

// LoadExistingTimeline 读取已有的短语时间轴，用来保留操作者填写的 visible_event_ids 与 reviewed。
// 文件不存在返回 nil；存在但读不懂时报错，不覆盖别人写过的内容。
func LoadExistingTimeline(path string) (*Timeline, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法读取已有短语时间轴：%w", err)
	}
	var t Timeline
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("已有短语时间轴 %s 不是合法 JSON 或字段类型不符，为免覆盖其中手填的 visible_event_ids，先修好或移走它：%w", path, err)
	}
	return &t, nil
}

// Adjustment 是对一个短语边界的人工修正。
type Adjustment struct {
	PhraseID string `json:"phrase_id"`
	Edge     string `json:"edge"`
	Frame    int    `json:"frame"`
	Reason   string `json:"reason"`
}

// AdjustFile 是 production/phrase-adjust.json。
type AdjustFile struct {
	Schema      int          `json:"schema"`
	Adjustments []Adjustment `json:"adjustments"`
}

// LoadAdjust 读取修正层。
func LoadAdjust(path string) (*AdjustFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取修正文件：%w", err)
	}
	var file AdjustFile
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s 不是合法 JSON 或含未知字段：%w", path, err)
	}
	if file.Schema != 1 {
		return nil, fmt.Errorf("%s 的 schema 应为 1，收到 %d", path, file.Schema)
	}
	return &file, nil
}

// phraseTiming 是一个短语的实测结果，修正之前。
type phraseTiming struct {
	span         PhraseSpan
	text         string
	tok0, tok1   int
	start, end   float64
	source       string
	confidence   float64
	labels       map[string]int
	omitted      bool
	allInterp    bool
	subtitle     int
	measuredFrom int
	measuredTo   int
}

// timePhrases 给每个受保护短语定时。只显示、不朗读的短语借前一个朗读单元的终点。
func timePhrases(s *Script, timed []TimedToken, cues []Cue, fps int) []phraseTiming {
	out := make([]phraseTiming, 0, len(s.Phrases))
	for _, span := range s.Phrases {
		p := phraseTiming{span: span, text: s.displayText(span.startPiece, span.endPiece), labels: map[string]int{}}
		p.tok0, p.tok1 = s.tokenRange(span.startPiece, span.endPiece)
		if p.tok1 == p.tok0 {
			// 没有朗读内容：时间点落在前一个朗读单元的终点（或下一个的起点）。
			at := 0.0
			if p.tok0 > 0 {
				at = timed[p.tok0-1].End
			} else if p.tok0 < len(timed) {
				at = timed[p.tok0].Start
			}
			p.start, p.end = at, at
			p.source = SourceInterpolated
			p.allInterp = true
			p.subtitle = cueOfPiece(cues, span.startPiece)
		} else {
			first, last := timed[p.tok0], timed[p.tok1-1]
			p.start, p.end = first.Start, last.End
			p.source = SourceForced
			if first.Interpolated || last.Interpolated {
				p.source = SourceInterpolated
			}
			score := 0.0
			p.allInterp = true
			for _, t := range timed[p.tok0:p.tok1] {
				p.labels[t.Label]++
				switch t.Label {
				case LabelExact, LabelSplit, LabelMerge:
					score++
				case LabelReplace:
					score += 0.5
				case LabelOmission:
					p.omitted = true
				}
				if !t.Interpolated {
					p.allInterp = false
				}
			}
			p.confidence = round3(score / float64(p.tok1-p.tok0))
			p.subtitle = cueOfPiece(cues, first.firstPiece)
		}
		p.measuredFrom, p.measuredTo = frames(toMs(p.start), toMs(p.end), fps)
		out = append(out, p)
	}
	return out
}

// frames 把毫秒起止换算成帧：起点走 beat.FrameOf（全项目唯一的秒→帧换算），终点至少比起点多一帧。
func frames(startMs, endMs int64, fps int) (int, int) {
	start := beat.FrameOf(float64(startMs)/1000, fps)
	end := max(beat.FrameOf(float64(endMs)/1000, fps), start+1)
	return start, end
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// applyAdjustments 在实测结果之上叠加修正层，返回每个短语最终的帧区间与被修正过的短语。
// 实测值本身不改：报告里同时给出实测帧与最终帧。
func applyAdjustments(phrases []phraseTiming, adjust *AdjustFile, totalFrames int) (map[string][2]int, map[string]bool, error) {
	final := make(map[string][2]int, len(phrases))
	for _, p := range phrases {
		final[p.span.ID] = [2]int{p.measuredFrom, p.measuredTo}
	}
	adjusted := map[string]bool{}
	if adjust == nil {
		return final, adjusted, nil
	}
	seen := map[string]bool{}
	var problems []string
	for i, a := range adjust.Adjustments {
		where := fmt.Sprintf("第 %d 条修正（%s %s）", i+1, a.PhraseID, a.Edge)
		current, ok := final[a.PhraseID]
		switch {
		case !ok:
			problems = append(problems, where+"：终稿里没有这个短语")
			continue
		case a.Edge != "start" && a.Edge != "end":
			problems = append(problems, where+"：edge 只能是 start 或 end")
			continue
		case strings.TrimSpace(a.Reason) == "":
			problems = append(problems, where+"：reason 不能为空，写清楚为什么不信实测")
			continue
		case seen[a.PhraseID+"\x00"+a.Edge]:
			problems = append(problems, where+"：同一短语的同一边界重复修正")
			continue
		}
		seen[a.PhraseID+"\x00"+a.Edge] = true
		// 起点必须是片内的帧；终点是开区间，最大可以等于总帧数。
		limit := totalFrames - 1
		if a.Edge == "end" {
			limit = totalFrames
		}
		if a.Frame < 0 || a.Frame > limit {
			problems = append(problems, fmt.Sprintf("%s：帧 %d 超出 [0, %d]（全片共 %d 帧）", where, a.Frame, limit, totalFrames))
			continue
		}
		if a.Edge == "start" {
			current[0] = a.Frame
		} else {
			current[1] = a.Frame
		}
		final[a.PhraseID] = current
		adjusted[a.PhraseID] = true
	}
	for _, p := range phrases {
		if r := final[p.span.ID]; adjusted[p.span.ID] && r[0] >= r[1] {
			problems = append(problems, fmt.Sprintf("短语 %s 修正后起点帧 %d 不早于终点帧 %d", p.span.ID, r[0], r[1]))
		}
	}
	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("修正文件不合格：\n  - %s", strings.Join(problems, "\n  - "))
	}
	return final, adjusted, nil
}
