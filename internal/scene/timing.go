// 本文件是口播镜头的时间合同：由 am scene cues 从字幕或短语锚点推导每个镜头的
// 全局帧区间，写进 scene.json 的 timing 块。
//
// 过去镜头时长由制作者手算（先定帧数，再写 duration_seconds = 帧数 ÷ 帧率），
// 渲染器还要自己从完整字幕里减去镜头起点。两处手算各有一个取整口径，错开一帧
// 只有拼完成片对轴时才看得出来。现在切点只由锚点和 beat.FrameOf 一个公式决定，
// 局部帧号由 am 算好交给渲染器。
package scene

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

// PhraseCopyFile 是 am scene cues 复制进镜头目录的短语时间轴文件名。
const PhraseCopyFile = "phrase-timeline.json"

// Timing 是 scene.json 的 timing 块：镜头在全片时间轴上的帧区间。
// 帧号都是全局帧，区间左闭右开。Plan 是分镜计划文件的 SHA-256，
// 用来发现「计划改了却没重跑 am scene cues」。Phrases 可省略，是镜头目录内
// 短语时间轴副本的相对路径。
//
// Chapter、Look、Enter、Exit 抄自分镜计划：渲染器互相看不到，相邻两个镜头只能
// 从这里拿到同一份配色与交接约定。字段全是可比较的值类型，--check 直接用 != 比对。
type Timing struct {
	FPS               int    `json:"fps"`
	StartFrame        int    `json:"start_frame"`
	EndFrameExclusive int    `json:"end_frame_exclusive"`
	Plan              string `json:"plan"`
	Phrases           string `json:"phrases,omitempty"`
	Chapter           string `json:"chapter,omitempty"`
	Look              Look   `json:"look,omitzero"`
	// Enter 是上一镜头交给本镜头的交接，Exit 是本镜头交给下一镜头的交接。
	Enter Handoff `json:"enter,omitzero"`
	Exit  Handoff `json:"exit,omitzero"`
}

// Frames 是镜头的帧数。
func (t Timing) Frames() int { return t.EndFrameExclusive - t.StartFrame }

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VerifyTimingInputs 核对镜头的 timing 块；渲染前与复用已有产物前都要过一遍。
//
// 与 VerifyBeatInputs 不同，没有 timing 块的镜头直接放行：老项目的镜头时长
// 是手写的，强制要求这一块会让它们全部渲染不了。帧数比对是精确的，
// 任何偏差都说明有人手改了 duration_seconds。全部问题一次报出。
func VerifyTimingInputs(s Scene) error {
	t := s.Timing
	if t == nil {
		return nil
	}
	fail := func(problems ...string) error {
		return &VerificationError{Output: s.Output, Problems: problems}
	}
	if s.Song != nil || s.Beat != nil {
		return fail("timing 块不能与 song 或 beat 块同时出现：口播、歌曲、卡点的时间轴来源互斥")
	}
	canvas, err := CanvasOf(s.Directory, s.StyleGuide)
	if err != nil {
		return fail(err.Error())
	}
	var problems []string
	if t.FPS != canvas.FPS {
		problems = append(problems, fmt.Sprintf("timing.fps 是 %d，画幅是 %dfps，请重新运行 am scene cues", t.FPS, canvas.FPS))
	}
	if t.StartFrame < 0 || t.EndFrameExclusive <= t.StartFrame {
		problems = append(problems, fmt.Sprintf("timing 的帧区间无效：[%d, %d)", t.StartFrame, t.EndFrameExclusive))
	}
	if !sha256Pattern.MatchString(t.Plan) {
		problems = append(problems, "timing.plan 必须是分镜计划文件的 SHA-256（64 位小写十六进制）")
	}
	if t.FPS > 0 && t.EndFrameExclusive > t.StartFrame {
		if got := int(math.Round(s.DurationSeconds * float64(t.FPS))); got != t.Frames() {
			problems = append(problems, fmt.Sprintf("duration_seconds %g 秒是 %d 帧，timing 块是 %d 帧：镜头时长由 am scene cues 写入，不要手改",
				s.DurationSeconds, got, t.Frames()))
		}
	}
	if err := VerifyFrameAlignment(s); err != nil {
		problems = append(problems, err.Error())
	}
	if t.Look != (Look{}) {
		if err := verifyLook(&t.Look); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for _, side := range []struct {
		name string
		h    Handoff
	}{{"enter", t.Enter}, {"exit", t.Exit}} {
		if side.h != (Handoff{}) && (!handoffDeviceValid(side.h.Device) || side.h.Peer == "" || strings.TrimSpace(side.h.Bridge) == "") {
			problems = append(problems, fmt.Sprintf("timing.%s 不完整或手法未知：交接约定由 am scene cues 写入，不要手改", side.name))
		}
	}
	if t.Phrases != "" {
		if _, err := LoadPhraseTimelineIn(s.Directory, t.Phrases, t.FPS); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fail(problems...)
	}
	return nil
}

// PhraseEntry 是 phrase-timeline.json 里时间合同用到的字段。帧号是全片全局帧。
type PhraseEntry struct {
	ID                string `json:"phrase_id"`
	SourceText        string `json:"source_text"`
	StartFrame        int    `json:"start_frame"`
	EndFrameExclusive int    `json:"end_frame_exclusive"`
}

// PhraseTimeline 是 phrase-timeline.json 的结构（只取时间合同用到的字段）。
type PhraseTimeline struct {
	FPS     int           `json:"fps"`
	Phrases []PhraseEntry `json:"phrases"`
}

// ParsePhraseTimeline 解析并校验短语时间轴：fps 与期望一致、短语 id 唯一非空、
// 每条短语至少一帧。wantFPS 为 0 时不比对帧率。
func ParsePhraseTimeline(body []byte, wantFPS int) (PhraseTimeline, error) {
	var t PhraseTimeline
	if err := json.Unmarshal(body, &t); err != nil {
		return PhraseTimeline{}, fmt.Errorf("短语时间轴不是合法 JSON 或字段类型不符：%w", err)
	}
	if t.FPS <= 0 {
		return PhraseTimeline{}, fmt.Errorf("短语时间轴缺少正整数 fps")
	}
	if wantFPS > 0 && t.FPS != wantFPS {
		return PhraseTimeline{}, fmt.Errorf("短语时间轴是 %dfps，镜头是 %dfps", t.FPS, wantFPS)
	}
	if len(t.Phrases) == 0 {
		return PhraseTimeline{}, fmt.Errorf("短语时间轴的 phrases 为空")
	}
	seen := make(map[string]bool, len(t.Phrases))
	for i, p := range t.Phrases {
		if strings.TrimSpace(p.ID) == "" {
			return PhraseTimeline{}, fmt.Errorf("短语时间轴第 %d 条缺少 phrase_id", i+1)
		}
		if seen[p.ID] {
			return PhraseTimeline{}, fmt.Errorf("短语时间轴的 phrase_id 重复：%s", p.ID)
		}
		seen[p.ID] = true
		if p.StartFrame < 0 || p.EndFrameExclusive <= p.StartFrame {
			return PhraseTimeline{}, fmt.Errorf("短语 %s 的帧区间无效：[%d, %d)", p.ID, p.StartFrame, p.EndFrameExclusive)
		}
	}
	return t, nil
}

// LoadPhraseTimelineIn 读取镜头目录内的短语时间轴副本，路径不得逃出镜头目录。
func LoadPhraseTimelineIn(directory, relative string, wantFPS int) (PhraseTimeline, error) {
	path, err := contained(directory, relative, "timing.phrases")
	if err != nil {
		return PhraseTimeline{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return PhraseTimeline{}, fmt.Errorf("timing.phrases 指向的文件无法读取：%s", relative)
	}
	t, err := ParsePhraseTimeline(body, wantFPS)
	if err != nil {
		return PhraseTimeline{}, fmt.Errorf("%s：%w", relative, err)
	}
	return t, nil
}

// TimingPlanSchema 是分镜计划文件的结构版本。
const TimingPlanSchema = 1

// 切点规则。锚点前一个文字单位结束到锚点开始之间的空白归谁，由它决定。
const (
	// CutNextStart 是缺省规则：切在锚点开始处，空白并入前一个镜头。
	CutNextStart = "next_start"
	// CutPrevEnd 切在前一个文字单位结束处，空白并入本镜头。
	CutPrevEnd = "prev_end"
	// CutMidGap 切在空白正中（向下取整）。
	CutMidGap = "mid_gap"
)

// TimingPlan 是 am scene cues 的分镜计划。
type TimingPlan struct {
	Schema int         `json:"schema"`
	Look   *Look       `json:"look,omitempty"`
	Scenes []PlanEntry `json:"scenes"`
}

// PlanEntry 是计划里的一个镜头。FirstSubtitle 是字幕文件里的序号（按出现顺序，
// 从 1 开始）；FirstPhrase 是短语时间轴里的 phrase_id。二者恰好填一个，
// 第一个镜头可以都不填（它固定从第 0 帧开始）。Dir 相对项目根。
type PlanEntry struct {
	ID            string `json:"id"`
	Dir           string `json:"dir"`
	FirstSubtitle *int   `json:"first_subtitle,omitempty"`
	FirstPhrase   string `json:"first_phrase,omitempty"`
	Cut           string `json:"cut,omitempty"`
	// VisualRole 是制作提示词第五阶段要求的主要视觉角色，不参与计时，
	// 但写错了就该在这里报：计划是严格解析的，拼错的键不会被静默丢掉。
	VisualRole string `json:"visual_role,omitempty"`
	// Summary 是给人看的一句话镜头说明，不参与计时。
	Summary string `json:"summary,omitempty"`
	// Chapter 是镜头所属章节。写了就每个镜头都要写；相邻镜头章节不同即章节交界。
	Chapter string `json:"chapter,omitempty"`
	// Handoff 是从上一镜头进入本镜头的交接，章节交界必填。
	Handoff *PlanHandoff `json:"handoff,omitempty"`
}

// Look 是全片共用的配色。镜头各自选色时，跨章节的两个镜头常常连底色都不同，
// 转场做得再顺也像换了一条视频。Surface 与 Accent2 可省略。
type Look struct {
	Background string `json:"background"`
	Surface    string `json:"surface,omitempty"`
	Ink        string `json:"ink"`
	Accent     string `json:"accent"`
	Accent2    string `json:"accent_2,omitempty"`
}

// PlanHandoff 是计划里一个交界的交接约定：用哪种手法，以及交界那一帧
// 两侧共有的画面（哪个物件、在哪、多大、什么状态，底色是什么）。
type PlanHandoff struct {
	Device string `json:"device"`
	Bridge string `json:"bridge"`
}

// Handoff 是写进 timing 块的一侧交接。Peer 是交界另一侧的镜头 id；
// CrossChapter 标记这是章节交界。零值表示该侧没有交接约定（硬切）。
type Handoff struct {
	Peer         string `json:"peer"`
	Device       string `json:"device"`
	Bridge       string `json:"bridge"`
	CrossChapter bool   `json:"cross_chapter,omitempty"`
}

// 交接手法。全部是「靠画面里的东西接过去」，没有一种是叠加在画面上的通用擦除：
// 章节交界要像章节内部一样自然，不能每次套同一个转场。
const (
	HandoffCarry  = "carry"
	HandoffMatch  = "match"
	HandoffPushIn = "push_in"
	HandoffFlood  = "flood"
	HandoffPan    = "pan"
	HandoffSettle = "settle"
)

// HandoffDevices 是 handoff.device 的合法取值与给渲染器的说明，顺序即文档顺序。
var HandoffDevices = []struct{ ID, Guide string }{
	{HandoffCarry, "接力：同一个物件跨过交界，交界帧上它的位置、大小、颜色与状态两侧一致，下一镜头接着让它变化"},
	{HandoffMatch, "形状匹配：上一镜头某元素在末帧的轮廓、位置与大小，与下一镜头某个不同元素在首帧的完全重合（圆点变成地球、进度条变成时间轴）"},
	{HandoffPushIn, "推进穿越：镜头推进到上一镜头某元素内部，该元素的表面铺满画面，成为下一镜头的底面"},
	{HandoffFlood, "色块铺满：上一镜头某元素扩张到铺满画面，它的颜色成为下一镜头开场的底色，再从中长出新内容"},
	{HandoffPan, "平移续接：同一张底面朝一个方向平移，上一镜头内容移出画面，下一镜头内容从另一侧移入，交界帧只剩共享底面与跨界物件"},
	{HandoffSettle, "退回底色：上一镜头语义元素退场，交界帧只剩全片底色与一个保留元素，下一镜头从这个元素长出"},
}

func handoffDeviceValid(device string) bool {
	for _, d := range HandoffDevices {
		if d.ID == device {
			return true
		}
	}
	return false
}

// HandoffGuide 返回手法的说明；未知手法返回空串。
func HandoffGuide(device string) string {
	for _, d := range HandoffDevices {
		if d.ID == device {
			return d.Guide
		}
	}
	return ""
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxBridgeRunes 限制 bridge 的长度：它描述的是一帧画面，写成段落说明分镜还没想清楚。
const maxBridgeRunes = 200

// VisualRoles 是 visual_role 的合法取值，与制作提示词第五阶段的四种视觉角色一致。
var VisualRoles = []string{"语境", "证据", "机制", "节奏"}

// ParseTimingPlan 严格解析分镜计划：未知字段、缺字段、锚点写法不合法都直接报错。
func ParseTimingPlan(body []byte) (TimingPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var plan TimingPlan
	if err := decoder.Decode(&plan); err != nil {
		return TimingPlan{}, fmt.Errorf("分镜计划不是合法 JSON 或含未知字段：%w", err)
	}
	if plan.Schema != TimingPlanSchema {
		return TimingPlan{}, fmt.Errorf("分镜计划的 schema 必须是 %d，收到 %d", TimingPlanSchema, plan.Schema)
	}
	if len(plan.Scenes) == 0 {
		return TimingPlan{}, fmt.Errorf("分镜计划的 scenes 为空")
	}
	ids := map[string]bool{}
	dirs := map[string]bool{}
	for i, entry := range plan.Scenes {
		where := fmt.Sprintf("分镜计划第 %d 个镜头", i+1)
		if entry.ID == "" || entry.Dir == "" {
			return TimingPlan{}, fmt.Errorf("%s缺少 id 或 dir", where)
		}
		if ids[entry.ID] {
			return TimingPlan{}, fmt.Errorf("%s的 id 重复：%s", where, entry.ID)
		}
		ids[entry.ID] = true
		if dirs[entry.Dir] {
			return TimingPlan{}, fmt.Errorf("%s的 dir 重复：%s", where, entry.Dir)
		}
		dirs[entry.Dir] = true
		hasSub, hasPhrase := entry.FirstSubtitle != nil, entry.FirstPhrase != ""
		if hasSub && hasPhrase {
			return TimingPlan{}, fmt.Errorf("%s（%s）同时写了 first_subtitle 与 first_phrase，只能填一个", where, entry.ID)
		}
		if i > 0 && !hasSub && !hasPhrase {
			return TimingPlan{}, fmt.Errorf("%s（%s）缺少锚点：除第一个镜头外都要写 first_subtitle 或 first_phrase", where, entry.ID)
		}
		if hasSub && *entry.FirstSubtitle < 1 {
			return TimingPlan{}, fmt.Errorf("%s（%s）的 first_subtitle 从 1 开始，收到 %d", where, entry.ID, *entry.FirstSubtitle)
		}
		switch entry.Cut {
		case "", CutNextStart, CutPrevEnd, CutMidGap:
		default:
			return TimingPlan{}, fmt.Errorf("%s（%s）的 cut 只能是 %s、%s 或 %s，收到 %q", where, entry.ID, CutNextStart, CutPrevEnd, CutMidGap, entry.Cut)
		}
		if entry.VisualRole != "" && !slices.Contains(VisualRoles, entry.VisualRole) {
			return TimingPlan{}, fmt.Errorf("%s（%s）的 visual_role 只能是 %s，收到 %q", where, entry.ID, strings.Join(VisualRoles, "、"), entry.VisualRole)
		}
		if i == 0 && entry.Cut != "" {
			return TimingPlan{}, fmt.Errorf("%s（%s）固定从第 0 帧开始，不能写 cut", where, entry.ID)
		}
	}
	if err := verifyLook(plan.Look); err != nil {
		return TimingPlan{}, err
	}
	if err := verifyHandoffs(plan.Scenes); err != nil {
		return TimingPlan{}, err
	}
	return plan, nil
}

func verifyLook(look *Look) error {
	if look == nil {
		return nil
	}
	var problems []string
	for _, field := range []struct {
		key, value string
		required   bool
	}{
		{"background", look.Background, true}, {"surface", look.Surface, false}, {"ink", look.Ink, true},
		{"accent", look.Accent, true}, {"accent_2", look.Accent2, false},
	} {
		switch {
		case field.value == "" && field.required:
			problems = append(problems, fmt.Sprintf("look.%s 必填", field.key))
		case field.value != "" && !hexColorPattern.MatchString(field.value):
			problems = append(problems, fmt.Sprintf("look.%s 必须是 #RRGGBB，收到 %q", field.key, field.value))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("分镜计划的 look 不合格：%s", strings.Join(problems, "；"))
	}
	return nil
}

// verifyHandoffs 校验章节与交接约定。
//
// 章节交界必须写 handoff：不写就是两个各自成立的镜头硬切在一起，正是要消除的生硬。
// 相邻两个章节交界不得用同一种手法——每次都同一个动作，观众第二次就看出套路了。
func verifyHandoffs(entries []PlanEntry) error {
	chaptered := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.Chapter) != "" {
			chaptered++
		}
	}
	if chaptered != 0 && chaptered != len(entries) {
		return fmt.Errorf("分镜计划的 chapter 要么每个镜头都写，要么都不写（%d 个镜头里写了 %d 个）", len(entries), chaptered)
	}
	var problems []string
	lastChapterDevice := ""
	for i, entry := range entries {
		where := fmt.Sprintf("镜头 %s", entry.ID)
		h := entry.Handoff
		if i == 0 {
			if h != nil {
				problems = append(problems, where+"是第一个镜头，没有上一镜头可交接，不能写 handoff")
			}
			continue
		}
		crossChapter := chaptered > 0 && entry.Chapter != entries[i-1].Chapter
		if h == nil {
			if crossChapter {
				problems = append(problems, fmt.Sprintf("%s开始新章节「%s」，必须写 handoff 说明如何从上一章接过来", where, entry.Chapter))
			}
			continue
		}
		if !handoffDeviceValid(h.Device) {
			ids := make([]string, len(HandoffDevices))
			for j, d := range HandoffDevices {
				ids[j] = d.ID
			}
			problems = append(problems, fmt.Sprintf("%s的 handoff.device 只能是 %s，收到 %q", where, strings.Join(ids, "、"), h.Device))
		}
		bridge := strings.TrimSpace(h.Bridge)
		switch {
		case bridge == "":
			problems = append(problems, where+"的 handoff.bridge 为空：要写清交界那一帧两侧共有的画面")
		case len([]rune(bridge)) > maxBridgeRunes:
			problems = append(problems, fmt.Sprintf("%s的 handoff.bridge 超过 %d 字：只描述交界那一帧", where, maxBridgeRunes))
		}
		if crossChapter {
			if h.Device == lastChapterDevice {
				problems = append(problems, fmt.Sprintf("%s与上一个章节交界都用 %s：相邻章节交界换一种手法", where, h.Device))
			}
			lastChapterDevice = h.Device
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("分镜计划的章节与交接不合格：\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// PlanHandoffs 把计划里的交接约定展开成每个镜头两侧的 Handoff，下标与 plan.Scenes 一致。
func PlanHandoffs(plan TimingPlan) (enter, exit []Handoff) {
	enter = make([]Handoff, len(plan.Scenes))
	exit = make([]Handoff, len(plan.Scenes))
	for i := 1; i < len(plan.Scenes); i++ {
		h := plan.Scenes[i].Handoff
		if h == nil {
			continue
		}
		cross := plan.Scenes[i].Chapter != plan.Scenes[i-1].Chapter
		bridge := strings.TrimSpace(h.Bridge)
		enter[i] = Handoff{Peer: plan.Scenes[i-1].ID, Device: h.Device, Bridge: bridge, CrossChapter: cross}
		exit[i-1] = Handoff{Peer: plan.Scenes[i].ID, Device: h.Device, Bridge: bridge, CrossChapter: cross}
	}
	return enter, exit
}

// TimingSegment 是 LayoutTiming 算出的一个镜头区间（全局帧，左闭右开）。
type TimingSegment struct {
	ID                string
	Dir               string
	StartFrame        int
	EndFrameExclusive int
}

// Frames 是镜头的帧数。
func (s TimingSegment) Frames() int { return s.EndFrameExclusive - s.StartFrame }

// anchorUnit 是一个可作锚点的文字单位（一条字幕或一条短语），帧号是全局帧。
type anchorUnit struct {
	start, end int
	order      int
}

// unitsFromCues 把字幕换成帧区间。秒到帧只经 beat.FrameOf 一个公式。
func unitsFromCues(cues []srt.Cue, fps int) []anchorUnit {
	units := make([]anchorUnit, len(cues))
	for i, c := range cues {
		units[i] = anchorUnit{start: beat.FrameOf(c.StartSeconds, fps), end: beat.FrameOf(c.EndSeconds, fps), order: i}
	}
	return units
}

// previousEnd 返回按时间排序后紧挨在 target 之前的那个单位的结束帧；
// target 是最早的单位时返回 ok=false。
func previousEnd(units []anchorUnit, target int) (int, bool) {
	sorted := append([]anchorUnit(nil), units...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	for i, u := range sorted {
		if u.order == target {
			if i == 0 {
				return 0, false
			}
			return sorted[i-1].end, true
		}
	}
	return 0, false
}

// cutFrame 按切点规则算出镜头边界。前一个单位与锚点重叠（结束晚于锚点开始）
// 时没有空白可分，三种规则都切在锚点开始处，保证锚点的文字不会落进上一个镜头。
func cutFrame(rule string, anchorStart, prevEnd int, hasPrev bool) int {
	if !hasPrev || prevEnd >= anchorStart {
		return anchorStart
	}
	switch rule {
	case CutPrevEnd:
		return prevEnd
	case CutMidGap:
		return (prevEnd + anchorStart) / 2
	default:
		return anchorStart
	}
}

// LayoutTiming 由分镜计划的锚点推导每个镜头的全局帧区间。
//
// 第一个镜头从第 0 帧开始，最后一个镜头到 total 结束；其余边界由锚点与切点
// 规则决定。锚点必须严格递增，每个镜头至少一帧，合计正好 total 帧。
// phrases 为 nil 时计划里不能出现 first_phrase。
func LayoutTiming(plan TimingPlan, cues []srt.Cue, phrases *PhraseTimeline, fps, total int) ([]TimingSegment, error) {
	if fps <= 0 {
		return nil, fmt.Errorf("帧率必须是正整数，收到 %d", fps)
	}
	if total <= 0 {
		return nil, fmt.Errorf("总帧数必须是正整数，收到 %d", total)
	}
	if phrases != nil && phrases.FPS != fps {
		return nil, fmt.Errorf("短语时间轴是 %dfps，镜头是 %dfps", phrases.FPS, fps)
	}
	subUnits := unitsFromCues(cues, fps)
	var phraseUnits []anchorUnit
	phraseIndex := map[string]int{}
	if phrases != nil {
		for i, p := range phrases.Phrases {
			phraseUnits = append(phraseUnits, anchorUnit{start: p.StartFrame, end: p.EndFrameExclusive, order: i})
			phraseIndex[p.ID] = i
		}
	}
	boundaries := make([]int, len(plan.Scenes))
	lastAnchor, lastAnchorName := -1, ""
	for i, entry := range plan.Scenes {
		var units []anchorUnit
		var target int
		var name string
		switch {
		case entry.FirstSubtitle != nil:
			n := *entry.FirstSubtitle
			if n < 1 || n > len(subUnits) {
				return nil, fmt.Errorf("镜头 %s 的 first_subtitle %d 超出字幕范围（共 %d 条）", entry.ID, n, len(subUnits))
			}
			units, target, name = subUnits, n-1, fmt.Sprintf("第 %d 条字幕", n)
		case entry.FirstPhrase != "":
			if phrases == nil {
				return nil, fmt.Errorf("镜头 %s 用短语 %s 作锚点，但没有提供短语时间轴（--phrases）", entry.ID, entry.FirstPhrase)
			}
			index, ok := phraseIndex[entry.FirstPhrase]
			if !ok {
				return nil, fmt.Errorf("镜头 %s 的 first_phrase %s 不在短语时间轴里", entry.ID, entry.FirstPhrase)
			}
			units, target, name = phraseUnits, index, "短语 "+entry.FirstPhrase
		default:
			// 只有第一个镜头可以不写锚点，ParseTimingPlan 已保证。
			boundaries[i] = 0
			continue
		}
		anchorStart := units[target].start
		if anchorStart <= lastAnchor {
			return nil, fmt.Errorf("锚点必须严格递增：镜头 %s 的%s从第 %d 帧开始，不晚于上一个锚点%s（第 %d 帧）",
				entry.ID, name, anchorStart, lastAnchorName, lastAnchor)
		}
		lastAnchor, lastAnchorName = anchorStart, name
		if i == 0 {
			boundaries[i] = 0
			continue
		}
		prevEnd, hasPrev := previousEnd(units, target)
		boundaries[i] = cutFrame(entry.Cut, anchorStart, prevEnd, hasPrev)
	}
	segments := make([]TimingSegment, len(plan.Scenes))
	for i, entry := range plan.Scenes {
		end := total
		if i+1 < len(boundaries) {
			end = boundaries[i+1]
		}
		if end <= boundaries[i] {
			if i+1 == len(boundaries) {
				return nil, fmt.Errorf("最后一个镜头 %s 从第 %d 帧开始，不早于总帧数 %d：锚点超出了全片", entry.ID, boundaries[i], total)
			}
			return nil, fmt.Errorf("镜头 %s 不足一帧：边界第 %d 帧到第 %d 帧，切点必须严格递增", entry.ID, boundaries[i], end)
		}
		segments[i] = TimingSegment{ID: entry.ID, Dir: entry.Dir, StartFrame: boundaries[i], EndFrameExclusive: end}
	}
	sum := 0
	for _, s := range segments {
		sum += s.Frames()
	}
	if sum != total {
		return nil, fmt.Errorf("镜头帧数合计 %d，不等于总帧数 %d", sum, total)
	}
	return segments, nil
}

// DurationLiteral 是写进 scene.json 的 duration_seconds 字面量：帧数 ÷ 帧率，
// 保留 6 位小数。6 位小数在常见帧率下离整数帧不到 1e-4 帧，
// round(时长 × 帧率) 总能还原帧数，也能通过 VerifyFrameAlignment。
func DurationLiteral(frames, fps int) string {
	return fmt.Sprintf("%.6f", float64(frames)/float64(fps))
}
