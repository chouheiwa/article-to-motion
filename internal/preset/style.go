package preset

import (
	"fmt"
	"regexp"
)

// Style 是与画幅正交的视觉风格。
//
// 画幅决定画布尺寸与安全区，风格决定配色、字体、版式骨架、动效语法和禁用项。
// 两者在 am init 时各选一次，由 go generate 组合成 frame.md：canvas 与
// safe_area 两块来自画幅，其余 token 来自风格模板。
type Style struct {
	// ID 同时是 frame.md 的 style_id，也是 assets/styles/ 与模板目录的目录名。
	ID string
	// Name 同时是 frame.md 的 style_name 与风格说明书的文件名前缀。
	Name string
	// Label 是 am init 选择框里的一行说明。
	Label string
	// SheetBackground 是示例 contact sheet 的底色，取该风格的结构线或底色。
	SheetBackground string
	// Fonts 是该风格从字体池额外带进项目的字体；共用的 Noto Sans SC 不在此列。
	Fonts []Font
}

// ArchetypeIDs 是每套风格都必须按此顺序声明的四种镜头骨架。
//
// 四者是叙事职责而不是视觉样式：钩子与强结论、对照、流程、系统总览。各风格
// 用自己的界面隐喻给它们起名、画示例（例如游戏 HUD 风把 capability_deck 画成
// 角色属性面板），但 id 固定，编排提示词与校验才不必随风格改动。
var ArchetypeIDs = []string{"proposition", "comparison", "process", "capability_deck"}

// GuideDoc 返回风格说明书在项目根下的相对路径。
func (s Style) GuideDoc() string {
	return "docs/" + s.Name + "-视频风格说明书.md"
}

// 顺序即 am init 选择框里的展示顺序，第一项是默认值。
var builtinStyles = []Style{
	{
		ID:              "clear-system-blueprint-v1",
		Name:            "清晰系统蓝图",
		Label:           "清晰系统蓝图：知识、技术与产品机制讲解（默认）",
		SheetBackground: "#D5DEEB",
	},
	{
		ID:              "pixel-arcade-v1",
		Name:            "像素街机",
		Label:           "像素街机：8/16-bit 复古游戏、怀旧、入门科普",
		SheetBackground: "#1B1B2F",
		Fonts:           []Font{FontFusionPixel400, FontPressStart2P400, FontSilkscreen400},
	},
	{
		ID:              "scifi-hud-v1",
		Name:            "科幻HUD",
		Label:           "科幻 HUD：航天、硬件、系统架构、任务式讲解",
		SheetBackground: "#0A1622",
		Fonts:           []Font{FontOrbitron},
	},
	{
		ID:              "cyberpunk-glitch-v1",
		Name:            "赛博故障",
		Label:           "赛博故障：安全攻防、黑客叙事、反差观点",
		SheetBackground: "#120B1E",
		Fonts:           []Font{FontSmileySans400, FontChakraPetch700},
	},
	{
		ID:              "candy-casual-v1",
		Name:            "糖果休闲",
		Label:           "糖果休闲手游：轻松科普、生活技巧、成长激励",
		SheetBackground: "#FFE3EE",
		Fonts:           []Font{FontZCOOLKuaiLe400, FontFredoka600, FontFredoka700},
	},
	{
		ID:              "rebel-graphic-v1",
		Name:            "二次元锐利拼贴",
		Label:           "二次元锐利拼贴：强观点、排行、角色化表达",
		SheetBackground: "#111111",
		Fonts:           []Font{FontZCOOLQingKe400, FontBungee400},
	},
	{
		ID:              "fantasy-quest-v1",
		Name:            "奇幻羊皮卷",
		Label:           "奇幻羊皮卷：历史、故事、学习路线、世界观",
		SheetBackground: "#3B2A1A",
		Fonts:           []Font{FontNotoSerifSC400, FontNotoSerifSC700, FontCinzel700},
	},
	{
		ID:              "esports-broadcast-v1",
		Name:            "电竞赛事转播",
		Label:           "电竞赛事转播：对比评测、排行榜、数据战报",
		SheetBackground: "#0B0F1A",
		Fonts:           []Font{FontBebasNeue400, FontOxanium600},
	},
	{
		ID:              "visual-novel-v1",
		Name:            "视觉小说对话",
		Label:           "视觉小说对话：故事、对话体、情感与人物",
		SheetBackground: "#2A2238",
		Fonts:           []Font{FontLXGWWenKai400},
	},
	{
		ID:              "neon-data-dark-v1",
		Name:            "暗夜数据霓虹",
		Label:           "暗夜数据霓虹：AI、前沿技术、数据与指标",
		SheetBackground: "#070A12",
		Fonts:           []Font{FontSpaceGrotesk500, FontSpaceGrotesk700},
	},
	{
		ID:              "editorial-magazine-v1",
		Name:            "杂志编辑排版",
		Label:           "杂志编辑排版：观点、人文、商业评论",
		SheetBackground: "#E8E2D6",
		Fonts:           []Font{FontNotoSerifSC400, FontNotoSerifSC700},
	},
	{
		ID:              "whiteboard-doodle-v1",
		Name:            "白板手绘",
		Label:           "白板手绘：教程、概念拆解、课堂讲解",
		SheetBackground: "#E9ECEF",
		Fonts:           []Font{FontXiaolai400, FontCaveat600},
	},
}

func AllStyles() []Style {
	out := make([]Style, len(builtinStyles))
	copy(out, builtinStyles)
	return out
}

func DefaultStyle() Style { return builtinStyles[0] }

func StyleIDs() []string {
	out := make([]string, 0, len(builtinStyles))
	for _, s := range builtinStyles {
		out = append(out, s.ID)
	}
	return out
}

func StyleByID(id string) (Style, bool) {
	for _, s := range builtinStyles {
		if s.ID == id {
			return s, true
		}
	}
	return Style{}, false
}

// StyleByIDOrError 供 validate 从 frame.md 的 style_id 反查风格。
func StyleByIDOrError(id string) (Style, error) {
	if s, ok := StyleByID(id); ok {
		return s, nil
	}
	return Style{}, fmt.Errorf("未知风格 %q，可选：%v", id, StyleIDs())
}

// CustomStyleFlag 是 am init --style 的保留取值：不选内置风格，改由用户描述生成。
const CustomStyleFlag = "custom"

// DraftStyleID 是自定义风格草稿骨架的 style_id。
//
// 草稿骨架字段齐全、能被解析，但还没有按用户描述定稿：validate 见到它就报
// 「定风格阶段未完成」，scene.Load 见到它就拒绝渲染。定稿时由编排阶段把
// style_id 改成 custom-<名字>。
const DraftStyleID = "custom-draft"

// CustomStyle 是自定义风格草稿骨架在源树与生成器里的身份。
//
// 它刻意不进 builtinStyles：生成器、init 选择框与一串枚举内置风格的测试都
// 遍历那张表，混进去会把草稿当成一套带示例图的正式风格。说明书路径固定为
// CustomStyle.GuideDoc()，定稿后改了 style_name 也不变，validate 不必从
// 用户写的名字拼路径。
var CustomStyle = Style{
	ID:              DraftStyleID,
	Name:            "自定义",
	Label:           "自定义：按文字描述生成风格（需在制作前完成定风格阶段）",
	SheetBackground: "#F5F7FB",
}

// customStyleID 限定自定义风格 id 的命名空间：小写字母数字，连字符分段。
// 内置风格一律不以 custom- 开头，两边不会撞名。
var customStyleID = regexp.MustCompile(`^custom(-[a-z0-9]+)+$`)

// IsCustomStyleID 判断 style_id 是否属于自定义风格（含草稿）。
func IsCustomStyleID(id string) bool {
	return customStyleID.MatchString(id)
}
