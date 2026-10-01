// Package reads 检查画面上每个可读元素是否给了观众足够的阅读时间。
//
// 生成式动画最常见的失败是节奏：元素一个接一个飞快出现，作者知道它写了什么，
// 观众只看一遍、第一次看，没读完就切走了。制作流程要求为每个可读元素记下
// 可读区间（visible-event-inventory.json），但过去只是写下来，没有任何环节
// 去算「这段时间够不够读完这些字」。
//
// 默认阈值取自 Netflix 简体中文字幕规范：成人节目每秒不超过 9 个字，每条至少
// 显示 5/6 秒。字幕是观众已经在盯着的固定位置，MG 画面里的元素还要先被找到，
// 所以这是下限中的下限——宁可漏判，不可误判。
package reads

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"unicode"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
)

// Event 是 visible-event-inventory.json 里的一个可读元素。帧号都是全片全局帧。
type Event struct {
	ID                  string `json:"event_id"`
	SceneID             string `json:"scene_id"`
	Selector            string `json:"selector"`
	Text                string `json:"text"`
	Role                string `json:"role"`
	PhraseID            string `json:"phrase_id"`
	EnterFrame          int    `json:"enter_frame"`
	ReadableStartFrame  int    `json:"readable_start_frame"`
	ReadableEndFrameExc int    `json:"readable_end_frame_exclusive"`
	ExitEndFrame        int    `json:"exit_end_frame"`
}

// Inventory 是 visible-event-inventory.json 的结构。
type Inventory struct {
	Schema int     `json:"schema"`
	FPS    int     `json:"fps"`
	Events []Event `json:"events"`
}

// Phrase 是 phrase-timeline.json 里本检查用到的字段。
type Phrase struct {
	ID            string `json:"phrase_id"`
	Order         int    `json:"semantic_order"`
	StartFrame    int    `json:"start_frame"`
	EndFrameExclu int    `json:"end_frame_exclusive"`
}

// Timeline 是 phrase-timeline.json 的结构（与 internal/song 导出的一致）。
type Timeline struct {
	FPS     int      `json:"fps"`
	Phrases []Phrase `json:"phrases"`
}

// DecorativeRole 的元素不承载信息，不检查阅读时间。
const DecorativeRole = "decorative"

// Options 是阅读时间判据。
type Options struct {
	CharsPerSecond float64
	MinSeconds     float64
	// CrowdSeconds：两个可读元素的可读起点相距不到这么久，视为同时出现。
	CrowdSeconds float64
}

// Defaults 见包注释的出处。CrowdSeconds 取 0.3 秒：视线在画面上找到一个新元素
// 大约要这么久，再短就是同时出现。
var Defaults = Options{CharsPerSecond: 9, MinSeconds: 5.0 / 6.0, CrowdSeconds: 0.3}

// EventResult 是单个元素的检查结果。
type EventResult struct {
	ID             string `json:"event_id"`
	Units          int    `json:"units"`
	ReadableFrames int    `json:"readable_frames"`
	RequiredFrames int    `json:"required_frames"`
}

// Report 是机器可读结果。OK 只由 Problems 决定。
type Report struct {
	SchemaVersion int           `json:"schema_version"`
	OK            bool          `json:"ok"`
	FPS           int           `json:"fps"`
	Events        []EventResult `json:"events"`
	Problems      []string      `json:"problems"`
	Hints         []string      `json:"hints,omitempty"`
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

// LoadInventory 读取并做结构校验。
func LoadInventory(path string) (Inventory, error) {
	var inv Inventory
	if err := readJSON(path, &inv); err != nil {
		return Inventory{}, err
	}
	if inv.FPS <= 0 {
		return Inventory{}, fmt.Errorf("%s 缺少正整数 fps", path)
	}
	if len(inv.Events) == 0 {
		return Inventory{}, fmt.Errorf("%s 的 events 为空", path)
	}
	return inv, nil
}

// LoadTimeline 读取短语时间轴。
func LoadTimeline(path string) (Timeline, error) {
	var t Timeline
	if err := readJSON(path, &t); err != nil {
		return Timeline{}, err
	}
	if len(t.Phrases) == 0 {
		return Timeline{}, fmt.Errorf("%s 的 phrases 为空", path)
	}
	return t, nil
}

func readJSON(path string, target any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("无法读取 %s：%w", path, err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("%s 不是合法 JSON 或字段类型不符：%w", path, err)
	}
	return nil
}

// Units 统计阅读量：一个汉字算一个单位，一串连续的字母或数字（一个英文词、
// 一个数）也算一个单位，标点和空白不算。按字母计数会把「GPU」算成三个字，
// 明显高估阅读量，制造误判。
func Units(text string) int {
	units := 0
	inWord := false
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			units++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				units++
			}
			inWord = true
		default:
			inWord = false
		}
	}
	return units
}

// RequiredFrames 返回读完 units 个单位所需的最少帧数。
func RequiredFrames(units, fps int, opts Options) int {
	seconds := math.Max(opts.MinSeconds, float64(units)/opts.CharsPerSecond)
	return int(math.Ceil(seconds*float64(fps) - 1e-9))
}

// Check 逐个检查可读元素；timeline 为 nil 时跳过与短语相关的两项。
func Check(inv Inventory, timeline *Timeline, opts Options) Report {
	report := Report{SchemaVersion: ReportSchemaVersion, FPS: inv.FPS, Events: []EventResult{}, Problems: []string{}}
	phrases := map[string]Phrase{}
	if timeline != nil {
		if timeline.FPS > 0 && timeline.FPS != inv.FPS {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"短语时间轴 fps=%d 与可读元素清单 fps=%d 不一致，帧号无法比对", timeline.FPS, inv.FPS))
			timeline = nil
		} else {
			for _, phrase := range timeline.Phrases {
				phrases[phrase.ID] = phrase
			}
		}
	}
	var semantic []Event
	seen := map[string]bool{}
	for _, event := range inv.Events {
		if event.ID == "" {
			report.Problems = append(report.Problems, "存在缺少 event_id 的可读元素")
			continue
		}
		if seen[event.ID] {
			report.Problems = append(report.Problems, fmt.Sprintf("event_id 重复：%s", event.ID))
			continue
		}
		seen[event.ID] = true
		if !framesOrdered(&report, event) || event.Role == DecorativeRole {
			continue
		}
		semantic = append(semantic, event)
		checkDuration(&report, event, inv.FPS, opts)
		if timeline != nil {
			checkPhrase(&report, event, phrases)
		}
	}
	sort.SliceStable(semantic, func(i, j int) bool { return semantic[i].ReadableStartFrame < semantic[j].ReadableStartFrame })
	if timeline != nil {
		checkOrder(&report, semantic, phrases)
	}
	checkCrowding(&report, semantic, inv.FPS, opts)
	report.OK = len(report.Problems) == 0
	return report
}

// framesOrdered 要求 进入 ≤ 可读开始 < 可读结束 ≤ 退出完成。
func framesOrdered(report *Report, e Event) bool {
	if e.EnterFrame < 0 || e.EnterFrame > e.ReadableStartFrame || e.ReadableStartFrame >= e.ReadableEndFrameExc || e.ReadableEndFrameExc > e.ExitEndFrame {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"%s 的帧号顺序不成立，应满足 enter ≤ readable_start < readable_end ≤ exit_end：%d / %d / %d / %d",
			e.ID, e.EnterFrame, e.ReadableStartFrame, e.ReadableEndFrameExc, e.ExitEndFrame))
		return false
	}
	return true
}

func checkDuration(report *Report, e Event, fps int, opts Options) {
	units := Units(e.Text)
	readable := e.ReadableEndFrameExc - e.ReadableStartFrame
	required := RequiredFrames(units, fps, opts)
	report.Events = append(report.Events, EventResult{ID: e.ID, Units: units, ReadableFrames: readable, RequiredFrames: required})
	if readable < required {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"%s（%s）可读 %d 帧，读完 %d 个字至少要 %d 帧，还差 %d 帧：「%s」",
			e.ID, e.SceneID, readable, units, required, required-readable, e.Text))
	}
}

// checkPhrase：非装饰元素不得早于它绑定的短语开始出现，否则画面在剧透口播。
func checkPhrase(report *Report, e Event, phrases map[string]Phrase) {
	if e.PhraseID == "" {
		report.Problems = append(report.Problems, fmt.Sprintf("%s 没有绑定 phrase_id", e.ID))
		return
	}
	phrase, ok := phrases[e.PhraseID]
	if !ok {
		report.Problems = append(report.Problems, fmt.Sprintf("%s 绑定的短语 %s 不在短语时间轴里", e.ID, e.PhraseID))
		return
	}
	if e.EnterFrame < phrase.StartFrame {
		report.Problems = append(report.Problems, fmt.Sprintf(
			"%s 在第 %d 帧出现，早于绑定短语 %s 的开始帧 %d：画面先于口播说出了这件事",
			e.ID, e.EnterFrame, phrase.ID, phrase.StartFrame))
	}
}

// checkOrder：按可读开始排序后，绑定短语的语义顺序不得倒退。
func checkOrder(report *Report, events []Event, phrases map[string]Phrase) {
	var previous *Event
	for i := range events {
		current := &events[i]
		phrase, ok := phrases[current.PhraseID]
		if !ok {
			continue
		}
		if previous != nil {
			if before := phrases[previous.PhraseID]; before.Order > phrase.Order {
				report.Problems = append(report.Problems, fmt.Sprintf(
					"%s（短语顺序 %d）比 %s（短语顺序 %d）先可读：画面顺序与口播顺序相反",
					previous.ID, before.Order, current.ID, phrase.Order))
			}
		}
		previous = current
	}
}

// checkCrowding 找出可读起点挤在一起的元素。只进 hints：卡片组、标题加副标题
// 一起出现是常见的合法版式，机器分不清那是一个信息点还是几个。
func checkCrowding(report *Report, events []Event, fps int, opts Options) {
	window := int(math.Round(opts.CrowdSeconds * float64(fps)))
	for i := 0; i < len(events); {
		j := i + 1
		for j < len(events) && events[j].ReadableStartFrame-events[i].ReadableStartFrame < window {
			j++
		}
		if j-i > 1 {
			ids := make([]string, 0, j-i)
			for _, e := range events[i:j] {
				ids = append(ids, e.ID)
			}
			report.Hints = append(report.Hints, fmt.Sprintf(
				"第 %d 帧附近 %d 个可读元素同时开始可读（%v）：观众一次只能读一个，确认它们是同一个信息点，否则错开",
				events[i].ReadableStartFrame, j-i, ids))
		}
		i = j
	}
}
