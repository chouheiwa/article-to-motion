// 本文件生成镜头局部数据文件 am-scene.js，并据此拼装提示词里的时间合同段。
//
// 渲染器过去要自己从完整字幕里减去镜头的全局起点，再按帧率换算——每个镜头
// 各算一遍，取整口径各不相同。现在局部帧号由 am 统一算好，写成
// window.AM_SCENE 交给 composition 直接读，提示词里的表格与它同源。
package scene

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/reads"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

// SceneDataFile 是写进镜头目录的局部数据文件名。
const SceneDataFile = "am-scene.js"

// SceneDataSchema 是 am-scene.js 的结构版本。字段名由运行时库按名读取，不得改名。
const SceneDataSchema = 1

// SceneData 是 window.AM_SCENE 的内容。帧号除 StartFrame 外都是镜头局部帧：
// 0 是本镜头第一帧。区间左闭右开。
type SceneData struct {
	Schema          int            `json:"schema"`
	SceneID         string         `json:"scene_id"`
	FPS             int            `json:"fps"`
	Frames          int            `json:"frames"`
	WidthPx         int            `json:"width_px"`
	HeightPx        int            `json:"height_px"`
	DurationSeconds float64        `json:"duration_seconds"`
	StartFrame      *int           `json:"start_frame"`
	SafeArea        map[string]any `json:"safe_area"`
	Subtitles       any            `json:"subtitles"`
	Cues            []LocalCue     `json:"cues"`
	Phrases         []LocalPhrase  `json:"phrases"`
	// Look 是全片配色，分镜计划没写 look 时省略。
	Look *Look `json:"look,omitempty"`
}

// LocalCue 是落在本镜头内的一条字幕，已裁到 [0, frames)。
type LocalCue struct {
	Index             int    `json:"index"`
	Text              string `json:"text"`
	StartFrame        int    `json:"start_frame"`
	EndFrameExclusive int    `json:"end_frame_exclusive"`
}

// LocalPhrase 是落在本镜头内的一条短语，已裁到 [0, frames)。
// MinReadableFrames 按 internal/reads 的阅读速度判据算出。
type LocalPhrase struct {
	PhraseID          string `json:"phrase_id"`
	Text              string `json:"text"`
	StartFrame        int    `json:"start_frame"`
	EndFrameExclusive int    `json:"end_frame_exclusive"`
	MinReadableFrames int    `json:"min_readable_frames"`
}

// styleExtras 是局部数据要从视觉规范里原样搬运的两块。用 any 接收，
// 写错类型也不会让整份 frontmatter 解析失败。
type styleExtras struct {
	SafeArea  any `yaml:"safe_area"`
	Subtitles any `yaml:"subtitles"`
}

// BuildSceneData 算出镜头的局部数据。没有 timing 块的镜头不知道自己在全片里
// 的位置，cues 与 phrases 为空数组、start_frame 为 null。
func BuildSceneData(s Scene) (SceneData, error) {
	canvas, err := CanvasOf(s.Directory, s.StyleGuide)
	if err != nil {
		return SceneData{}, err
	}
	data := SceneData{
		Schema: SceneDataSchema, SceneID: s.ID, FPS: canvas.FPS,
		Frames:  int(math.Round(s.DurationSeconds * float64(canvas.FPS))),
		WidthPx: canvas.WidthPx, HeightPx: canvas.HeightPx, DurationSeconds: s.DurationSeconds,
		SafeArea: map[string]any{}, Cues: []LocalCue{}, Phrases: []LocalPhrase{},
	}
	if s.StyleGuide != "" {
		var extras styleExtras
		if err := readStyleFrontmatter(s.Directory, s.StyleGuide, &extras); err != nil {
			return SceneData{}, err
		}
		if area, ok := extras.SafeArea.(map[string]any); ok {
			data.SafeArea = area
		}
		data.Subtitles = extras.Subtitles
	}
	t := s.Timing
	if t == nil {
		return data, nil
	}
	start := t.StartFrame
	data.StartFrame = &start
	if t.Look != (Look{}) {
		look := t.Look
		data.Look = &look
	}
	transcript, err := contained(s.Directory, s.Transcript, "transcript")
	if err != nil {
		return SceneData{}, err
	}
	cues, err := srt.ReadCues(transcript)
	if err != nil {
		return SceneData{}, fmt.Errorf("transcript %s：%w", s.Transcript, err)
	}
	for _, c := range cues {
		from, to, ok := clipLocal(beat.FrameOf(c.StartSeconds, canvas.FPS), beat.FrameOf(c.EndSeconds, canvas.FPS), start, data.Frames)
		if !ok {
			continue
		}
		data.Cues = append(data.Cues, LocalCue{Index: c.Index, Text: c.Text, StartFrame: from, EndFrameExclusive: to})
	}
	if t.Phrases != "" {
		timeline, err := LoadPhraseTimelineIn(s.Directory, t.Phrases, canvas.FPS)
		if err != nil {
			return SceneData{}, err
		}
		for _, p := range timeline.Phrases {
			from, to, ok := clipLocal(p.StartFrame, p.EndFrameExclusive, start, data.Frames)
			if !ok {
				continue
			}
			data.Phrases = append(data.Phrases, LocalPhrase{
				PhraseID: p.ID, Text: p.SourceText, StartFrame: from, EndFrameExclusive: to,
				MinReadableFrames: reads.RequiredFrames(reads.Units(p.SourceText), canvas.FPS, reads.Defaults),
			})
		}
	}
	return data, nil
}

// clipLocal 把全局区间 [from, to) 换成相对 offset 的局部区间并裁到 [0, frames)。
// 与本镜头不重叠（含裁完为空）时 ok 为 false。
func clipLocal(from, to, offset, frames int) (int, int, bool) {
	from, to = max(from-offset, 0), min(to-offset, frames)
	return from, to, from < to
}

// EncodeSceneData 返回 am-scene.js 的完整字节。map 的键由 encoding/json 排序，
// 同样的输入总是得到同样的字节。
func EncodeSceneData(data SceneData) ([]byte, error) {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("镜头局部数据无法编码为 JSON：%w", err)
	}
	var b bytes.Buffer
	b.WriteString("// 由 am 生成，勿手改。\n")
	b.WriteString("window.AM_SCENE = ")
	b.Write(body)
	b.WriteString(";\n")
	return b.Bytes(), nil
}

// WriteSceneData 原子写出 <镜头目录>/am-scene.js。内容不变时不重写。
func WriteSceneData(s Scene) error {
	data, err := BuildSceneData(s)
	if err != nil {
		return err
	}
	body, err := EncodeSceneData(data)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Directory, SceneDataFile)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, body) {
		return nil
	}
	return fsutil.AtomicWrite(path, body, 0o644)
}

// timingTableLimit 是提示词里每张表最多列出的行数，其余让渲染器去读 am-scene.js。
const timingTableLimit = 40

// timingTextLimit 是表格里单条文字最多保留的字符数。
const timingTextLimit = 40

// promptText 把字幕或短语原文压成表格里的一格：去掉换行、阶段消息与定界标记，
// 过长截断。原文来自字幕文件，不能让它在提示词里伪装成指令或阶段消息。
func promptText(text string) string {
	text = strings.ReplaceAll(text, "[[USER_MESSAGE]]", "")
	text = delimiterMatch.ReplaceAllString(text, "")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, "｜", "|")
	if runes := []rune(text); len(runes) > timingTextLimit {
		text = string(runes[:timingTextLimit]) + "…"
	}
	return text
}

// timingSection 拼装时间合同段；没有 timing 块时返回空字符串，老镜头的提示词
// 与引入这块之前逐字节相同。
func timingSection(s Scene) (string, error) {
	t := s.Timing
	if t == nil {
		return "", nil
	}
	data, err := BuildSceneData(s)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\n时间合同（强制，由 am 计算）：\n")
	fmt.Fprintf(&b, "- 本镜头覆盖全片第 %d 帧到第 %d 帧（不含），共 %d 帧。下面所有帧号都是本镜头局部帧：0 = 本镜头第一帧，已减去全片偏移，不要再自行换算。\n",
		t.StartFrame, t.EndFrameExclusive, data.Frames)
	if len(data.Phrases) > 0 {
		b.WriteString("- 短语（编号｜原文｜局部起止帧，左闭右开｜最少可读帧数）：\n")
		for i, p := range data.Phrases {
			if i == timingTableLimit {
				fmt.Fprintf(&b, "  其余 %d 条见 %s\n", len(data.Phrases)-i, SceneDataFile)
				break
			}
			fmt.Fprintf(&b, "  %s｜%s｜%d–%d｜%d\n", p.PhraseID, promptText(p.Text), p.StartFrame, p.EndFrameExclusive, p.MinReadableFrames)
		}
	}
	if len(data.Cues) > 0 {
		b.WriteString("- 字幕（序号｜原文｜局部起止帧，左闭右开）：\n")
		for i, c := range data.Cues {
			if i == timingTableLimit {
				fmt.Fprintf(&b, "  其余 %d 条见 %s\n", len(data.Cues)-i, SceneDataFile)
				break
			}
			fmt.Fprintf(&b, "  %d｜%s｜%d–%d\n", c.Index, promptText(c.Text), c.StartFrame, c.EndFrameExclusive)
		}
	} else {
		b.WriteString("- 本镜头范围内没有字幕，画面不得出现任何字幕文字。\n")
	}
	if len(data.Phrases) > 0 {
		b.WriteString("- 每个可读元素（文字、数字、标签）不得早于它对应短语的起始帧出现，可读停留不少于该短语的最少可读帧数；上表之外的短语属于其他镜头，不得出现在本镜头画面里。\n")
	} else {
		b.WriteString("- 每个可读元素（文字、数字、标签）不得早于它对应字幕的起始帧出现；上表之外的字幕属于其他镜头，其内容不得出现在本镜头画面里。\n")
	}
	fmt.Fprintf(&b, "- 本镜头目录里的 %s 以 window.AM_SCENE 提供同一份数据（帧率、帧数、画布、安全区、字幕样式、字幕与短语的局部帧）。"+
		"在 composition 里用 <script src=\"%s\"></script> 加载，帧号从中读取，不要把帧号写死在代码里；该文件由 am 生成，不要修改。\n",
		SceneDataFile, SceneDataFile)
	b.WriteString(lookSection(t.Look))
	b.WriteString(handoffSection(t, data))
	return b.String(), nil
}

// lookSection 是全片配色段。没有 look 时为空，镜头照旧自主选色。
func lookSection(look Look) string {
	if look == (Look{}) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n全片配色（强制，全片共用，window.AM_SCENE.look 是同一份）：\n")
	fmt.Fprintf(&b, "- 底色 %s：画面大面积背景只用它；正文与主要文字 %s；强调 %s", look.Background, look.Ink, look.Accent)
	if look.Accent2 != "" {
		fmt.Fprintf(&b, "，第二强调 %s", look.Accent2)
	}
	if look.Surface != "" {
		fmt.Fprintf(&b, "；卡片、面板等承载面 %s", look.Surface)
	}
	b.WriteString("。\n- 允许在这几种颜色之间调透明度、明暗做层次，不得另起一套主色或换底色；跨章节时观众靠底色与配色认出这还是同一条视频。\n")
	return b.String()
}

// handoffMinTail 是交接动作至少要有的帧数（30fps 下约 0.3 秒），少于它时
// 提示词改为允许与字幕重叠。
const handoffMinTail = 9

// handoffSection 是镜头交接段。相邻两个镜头分别渲染、互相看不到，
// 交界能否接上只取决于两侧是否照同一份约定画出同一帧，所以这里把约定原样给两边，
// 再把交接动作可用的空档帧算好，不让渲染器拿可读区间去做转场。
func handoffSection(t *Timing, data SceneData) string {
	if t.Enter == (Handoff{}) && t.Exit == (Handoff{}) {
		return ""
	}
	head, tail := data.Frames, data.Frames
	if len(data.Cues) > 0 {
		head, tail = data.Frames, 0
		for _, c := range data.Cues {
			head = min(head, c.StartFrame)
			tail = max(tail, c.EndFrameExclusive)
		}
		tail = data.Frames - tail
	}
	var b strings.Builder
	b.WriteString("\n镜头交接（强制，由分镜计划约定；相邻镜头分别渲染、互相看不到，两侧拿到的是同一份约定）：\n")
	side := func(label string, h Handoff, peerRole string) {
		scope := "章节内"
		if h.CrossChapter {
			scope = "章节交界"
		}
		fmt.Fprintf(&b, "- %s：%s %s（%s），手法 %s——%s。\n", label, peerRole, h.Peer, scope, h.Device, HandoffGuide(h.Device))
		fmt.Fprintf(&b, "  交界帧：%s\n", bridgeText(h.Bridge))
	}
	if t.Enter != (Handoff{}) {
		side("入场", t.Enter, "承接")
		fmt.Fprintf(&b, "  本镜头第 0 帧必须就是这一帧，从它接着上一镜头的动作继续，不得重新从静止起势或另起一个开场画面。")
		if len(data.Cues) == 0 {
			b.WriteString("本镜头没有字幕，接续动作的长短按画面需要决定。\n")
		} else if head >= handoffMinTail {
			fmt.Fprintf(&b, "接续动作放在第一条字幕出现之前（局部第 0 帧到第 %d 帧，共 %d 帧）。\n", head, head)
		} else {
			fmt.Fprintf(&b, "第一条字幕前只有 %d 帧，接续动作可以与开头字幕重叠，但不得遮挡字幕，可读元素照常不早于对应字幕或短语出现。\n", head)
		}
	}
	if t.Exit != (Handoff{}) {
		side("出场", t.Exit, "交给")
		fmt.Fprintf(&b, "  本镜头最后一帧（局部第 %d 帧）必须就是这一帧；这条覆盖「镜头结尾停在可读的终态」——有出场交接的镜头结尾停在交界帧。", data.Frames-1)
		if len(data.Cues) == 0 {
			b.WriteString("本镜头没有字幕，出场动作的长短按画面需要决定。\n")
		} else if tail >= handoffMinTail {
			fmt.Fprintf(&b, "出场动作放在最后一条字幕结束之后（局部第 %d 帧到第 %d 帧，共 %d 帧），之前所有可读元素保持可读。\n", data.Frames-tail, data.Frames-1, tail)
		} else {
			fmt.Fprintf(&b, "最后一条字幕之后只有 %d 帧，出场动作可以与结尾字幕重叠，但可读元素在最少可读帧数内不得被遮挡或移出。\n", tail)
		}
	}
	b.WriteString("- 运动跨过交界要连续：出场动作以加速（ease-in）进入交界帧，入场动作从交界帧以减速（ease-out）离开，两侧方向、速度一致；不得在交界帧两侧各停一次，那会让观众看到一次顿挫。\n")
	b.WriteString("- 交界帧以外的构图、版式和动效仍由你决定。整片复查会逐帧比对交界前后两帧，画面差异过大判为不合格、需要重渲染。\n")
	return b.String()
}

// bridgeText 清理计划里写的交界帧描述：它来自分镜计划，同样不能伪装成阶段消息或定界块。
func bridgeText(text string) string {
	text = strings.ReplaceAll(text, "[[USER_MESSAGE]]", "")
	text = delimiterMatch.ReplaceAllString(text, "")
	return strings.Join(strings.Fields(text), " ")
}
