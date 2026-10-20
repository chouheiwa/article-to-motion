package scene

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/chouheiwa/articale-to-motion/internal/hyperframes"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// 渲染工具各自把技能装在不同目录，提示词里不得写死任何一条路径。
// 这里在 Go 侧解析出本机真实位置，解析不到时由 BuildPrompt 退回按技能名引用。
const (
	// SkillsDirEnv 是逃生阀：显式指定技能目录，优先级高于一切自动探测。
	SkillsDirEnv = "HYPERFRAMES_SKILLS_DIR"

	skillManifestFile   = "SKILL.md"
	rulesIndexFile      = "rules-index.md"
	blueprintsIndexFile = "blueprints-index.md"

	// portableSkillsDir 是不绑定具体工具的通用约定，作为兜底候选。
	portableSkillsDir = ".agents/skills"

	// maxWalkUpLevels 限制向上查找项目级技能目录的层数，避免病态路径下空转。
	maxWalkUpLevels = 40
)

// AnimationSkillName 是承载全部动效知识的技能目录名。
const AnimationSkillName = "hyperframes-animation"

// HyperFramesCoreSkillName 是组合 HTML 的授权契约技能目录名。
const HyperFramesCoreSkillName = "hyperframes-core"

// HyperFramesCLISkillName 是 HyperFrames CLI 用法技能目录名。
const HyperFramesCLISkillName = "hyperframes-cli"

// HyperFramesCreativeSkillName 是构图、版式与设计遵从的创意方向技能目录名。
const HyperFramesCreativeSkillName = "hyperframes-creative"

// AlgorithmicArtSkillName 是生成式艺术技能目录名。
const AlgorithmicArtSkillName = "algorithmic-art"

// TextToLottieSkillName 是 Lottie 图层制作技能目录名。
const TextToLottieSkillName = "text-to-lottie"

// CharacterRigSkillName 是角色驱动技能目录名。
const CharacterRigSkillName = "character-rig"

// SceneKitSkillName 是按帧驱动工具库（缓动、安全区、字幕）技能目录名。
const SceneKitSkillName = "scene-kit"

// sceneKitLibraryFile 是 scene-kit 技能里的库文件名。它同时出现在
// RequiredFiles 与提示词的复制契约里，所以取一个常量。
const sceneKitLibraryFile = "scene-kit.js"

// CastRequiredHeading 是 castSection 在"本镜头有角色出场"时输出的段标题
// （不含前导换行与冒号），三处共同的契约锚点：castSection 产出它、
// characterRigPrompt 的角色驱动门控靠"下方是否出现这个标题"决定要不要让
// 渲染 agent 加载 character-rig 技能、character-rig/SKILL.md 里的复制义务
// 说明也引用同一个标题告诉读者去哪找这条硬要求。前两处共享这一个 Go 常量，
// 保证不会各自改动后措辞漂移；SKILL.md 是纯文本，没法引用 Go 常量，改动
// 后不会编译失败，只能靠 assets_test.go 的断言把它钉在这个字面量上
// （断言位置见 TestCharacterRigSkillMentionsCastRequiredHeading）。
const CastRequiredHeading = "角色（强制）"

// SkillDescriptor 描述一个可自动发现的技能。
// SkillSource 说明技能是怎么进到用户项目里的。
//
// 这不是元数据而是契约：两类技能的缺失补救方式、升级方式和守护测试都不同。
// 过去只有动效技能一个例外，靠在测试里按名字特判绕过；再加第二个上游技能时
// 那种写法就会崩，所以把来源直接写进描述符。
type SkillSource uint8

const (
	// SourceEmbedded 随 am 二进制下发，由 project.Initialize 写进项目。
	// 这类技能必须存在于 assets/shared/.agents/skills/ 里。
	SourceEmbedded SkillSource = iota
	// SourceUpstream 由 am init 按固定版本从上游安装进项目。
	// 这类技能不得进嵌入树——那会留下一份永远追不上上游的陈旧副本。
	SourceUpstream
)

type SkillDescriptor struct {
	// Name 是技能目录名。
	Name string
	// Source 说明技能来自嵌入树还是上游安装。
	Source SkillSource
	// RequiredFiles 是技能被视为「完整安装」所必须存在的文件列表。
	// 至少包含 SKILL.md；动效技能额外要求 rules-index.md。
	RequiredFiles []string
	// PromptSection 返回该技能注入镜头提示词的片段。
	// skillsDir 为空时应退回按技能名引用，不写死本机路径。
	PromptSection func(skillsDir string) string
}

// RegisteredSkills 是所有自动发现的技能注册表。
// 新增技能只需在此追加一条，ResolveAllSkills 和 BuildPrompt 自动覆盖。
const SongExplainerSkillName = "song-explainer"

var RegisteredSkills = []SkillDescriptor{
	{Name: SongExplainerSkillName, Source: SourceEmbedded, RequiredFiles: []string{skillManifestFile}, PromptSection: songExplainerPrompt},
	{
		Name:          AnimationSkillName,
		Source:        SourceUpstream,
		RequiredFiles: []string{skillManifestFile, rulesIndexFile},
		PromptSection: animationPrompt,
	},
	{
		Name:          HyperFramesCoreSkillName,
		Source:        SourceUpstream,
		RequiredFiles: []string{skillManifestFile},
		PromptSection: hyperFramesCorePrompt,
	},
	{
		Name:          HyperFramesCLISkillName,
		Source:        SourceUpstream,
		RequiredFiles: []string{skillManifestFile},
		PromptSection: hyperFramesCLIPrompt,
	},
	{
		Name:          HyperFramesCreativeSkillName,
		Source:        SourceUpstream,
		RequiredFiles: []string{skillManifestFile},
		PromptSection: hyperFramesCreativePrompt,
	},
	{
		Name:          AlgorithmicArtSkillName,
		Source:        SourceEmbedded,
		RequiredFiles: []string{skillManifestFile},
		PromptSection: algorithmicArtPrompt,
	},
	{
		Name:          TextToLottieSkillName,
		Source:        SourceEmbedded,
		RequiredFiles: []string{skillManifestFile},
		PromptSection: textToLottiePrompt,
	},
	{
		Name:   CharacterRigSkillName,
		Source: SourceEmbedded,
		// 驱动库本身也是必需文件：只有 SKILL.md 而没有 cast.js 的技能目录，
		// 提示词里那条「把驱动库复制进镜头目录」的契约就无从执行。
		RequiredFiles: []string{skillManifestFile, castDriverFile},
		PromptSection: characterRigPrompt,
	},
	{
		Name:   SceneKitSkillName,
		Source: SourceEmbedded,
		// 库文件本身也是必需文件：只有 SKILL.md 的技能目录，提示词里那条
		// 「把库复制进镜头目录」的契约就无从执行。
		RequiredFiles: []string{skillManifestFile, sceneKitLibraryFile},
		PromptSection: sceneKitPrompt,
	},
}

// castDriverFile 是 character-rig 技能里的驱动库文件名。
// 它同时出现在 RequiredFiles 与 castSection 的复制契约里，所以取一个常量。
const castDriverFile = "cast.js"

// skillLocations 是一个渲染工具的技能目录：家目录级与项目级的相对路径可能不同。
type skillLocations struct {
	home    string
	project string
}

var rendererSkillDirs = map[string]skillLocations{
	"claude":    {home: ".claude/skills", project: ".claude/skills"},
	"codex":     {home: ".codex/skills", project: ".codex/skills"},
	"qoder":     {home: ".qoder/skills", project: ".qoder/skills"},
	"codebuddy": {home: ".codebuddy/skills", project: ".codebuddy/skills"},
	"opencode":  {home: ".config/opencode/skills", project: ".opencode/skills"},
}

// hasSkill 校验候选目录里确实装了指定技能，并且必需文件全部存在。
func hasSkill(dir, skillName string, requiredFiles []string) bool {
	if dir == "" {
		return false
	}
	skill := filepath.Join(dir, skillName)
	for _, name := range requiredFiles {
		info, err := os.Stat(filepath.Join(skill, name))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// projectCandidates 从镜头目录逐级向上，收集每一层的项目级技能目录候选。
// 项目级安装会覆盖全局安装，因此必须先于家目录被检查。
func projectCandidates(sceneDir, relative string) []string {
	current, err := filepath.Abs(sceneDir)
	if err != nil {
		return nil
	}
	var out []string
	for level := 0; level < maxWalkUpLevels; level++ {
		out = append(out, filepath.Join(current, filepath.FromSlash(relative)))
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return out
}

// SkillsEnvironment 把 .env 覆盖层里的 SkillsDirEnv 并入查找用的环境。
// 优先级与 config.Load 一致：真实环境变量 > .env；不修改传入的任何一个 map。
func SkillsEnvironment(baseEnv, overlay map[string]string) map[string]string {
	if baseEnv[SkillsDirEnv] != "" || overlay[SkillsDirEnv] == "" {
		return baseEnv
	}
	merged := make(map[string]string, len(baseEnv)+1)
	for key, value := range baseEnv {
		merged[key] = value
	}
	merged[SkillsDirEnv] = overlay[SkillsDirEnv]
	return merged
}

// ResolveSkill 在渲染器的技能目录树中查找指定技能。
//
// 查找顺序：SkillsDirEnv → 渲染器专属目录（项目级自镜头目录逐级向上，再家目录级）
// → 通用 .agents/skills（同样先项目级再家目录级）。
// 未找到时返回空字符串且不报错，由调用方降级为按技能名引用；
// 只有 SkillsDirEnv 被显式设置却指向无效目录时才报错——配置写错了必须响。
func ResolveSkill(renderer, sceneDir string, environ map[string]string, desc SkillDescriptor) (string, error) {
	if explicit := environ[SkillsDirEnv]; explicit != "" {
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("%s 不是合法路径：%s", SkillsDirEnv, explicit)
		}
		if !hasSkill(absolute, desc.Name, desc.RequiredFiles) {
			return "", fmt.Errorf("%s 指向的目录缺少 %s/%s：%s", SkillsDirEnv, desc.Name, desc.RequiredFiles[0], absolute)
		}
		return absolute, nil
	}

	home := environ["HOME"]
	homeCandidate := func(relative string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(home, filepath.FromSlash(relative))
	}

	// 先把渲染器专属目录找完再考虑通用目录，否则项目根位于 $HOME 之下时，
	// 向上遍历会在 $HOME 那一层先命中 .agents/skills，抢在专属目录前面。
	var ordered []string
	if locations, known := rendererSkillDirs[renderer]; known {
		ordered = append(ordered, projectCandidates(sceneDir, locations.project)...)
		ordered = append(ordered, homeCandidate(locations.home))
	}
	ordered = append(ordered, projectCandidates(sceneDir, portableSkillsDir)...)
	ordered = append(ordered, homeCandidate(portableSkillsDir))

	for _, candidate := range ordered {
		if hasSkill(candidate, desc.Name, desc.RequiredFiles) {
			return candidate, nil
		}
	}
	return "", nil
}

// ResolveAllSkills 解析所有注册技能，返回 name → 绝对路径的映射。
// 未找到的技能不出现 in map 中（不是报错）。
func ResolveAllSkills(renderer, sceneDir string, environ map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	for _, desc := range RegisteredSkills {
		dir, err := ResolveSkill(renderer, sceneDir, environ, desc)
		if err != nil {
			return nil, err
		}
		if dir != "" {
			result[desc.Name] = dir
		}
	}
	return result, nil
}

// skillPromptSections 为所有已注册技能生成提示词片段。
// 已解析的技能注入本机路径；未解析的退回按技能名引用。
func skillPromptSections(resolved map[string]string) string {
	var sections string
	for _, desc := range RegisteredSkills {
		if desc.Name == SongExplainerSkillName {
			continue
		}
		dir := resolved[desc.Name]
		sections += desc.PromptSection(dir)
	}
	return sections
}

// animationPrompt 是动效技能的提示词片段。
//
// 这里刻意不设数量配额（几条 rule、几个分类、几个属性）。配额是可以逐项打勾的
// 指标，渲染器会为了达标加一个与文案无关的 rotation 或 clip-path；公开案例里
// 把「好看」压成蓝图名与指标的版本，指标全绿、画面反而拥挤平庸。动效数量由
// 文案决定，这里只要求每个动效说得出理由。
//
// 环境动效不设项目级风格配额；只要不影响阅读和确定性，就由镜头根据文案决定。
// 语义元素仍应在到位后稳定停留，避免用装饰动效掩盖信息层级。
func animationPrompt(skillsDir string) string {
	reference := fmt.Sprintf(
		"- 加载 %s 技能，实现前必须读取该技能目录下的 %s（原子动效 rule 索引）。\n"+
			"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		AnimationSkillName, rulesIndexFile)
	if skillsDir != "" {
		skill := filepath.Join(skillsDir, AnimationSkillName)
		reference = fmt.Sprintf(
			"- 加载 %s 技能。本机该技能目录为：\n    %s\n"+
				"  实现前必须读取其中的 %s（原子动效 rule 索引）；若该路径不存在，\n"+
				"  改用你自身的技能加载机制载入 %s 后读取同名文件。\n",
			AnimationSkillName, skill, rulesIndexFile, AnimationSkillName)
	}
	return "动效要求（强制）：\n" + reference +
		"- 导演的是状态变化，不只是入场：每个动画先想清楚它最终停在什么状态、那个状态是否说明了这句台词的论点；" +
		"入场再漂亮，也补不了落定状态说不清的问题。\n" +
		"- 跟随关系要成立：指针落在它要按的控件上，标签跟着它标注的对象走。\n" +
		"- 按文案选 rule，不设数量下限：每个动效都要说得出它在表达哪句话、哪层关系或哪个转折，" +
		"说不出理由的不加。宁可少而准，不要为了显得丰富堆叠属性。\n" +
		"- 镜头包含 3 个及以上阶段时，先读同目录 " + blueprintsIndexFile + " 选一个模板再落地。\n" +
		"- 环境动效可选，整镜最多 1 条，振幅小到不干扰阅读；不得持续漂浮或晃动语义元素。\n" +
		"- 语义元素到位后应当稳定停留供阅读，这段停留允许画面静止；" +
		"镜头结尾停在可读的终态，不得停在入场中间态。\n" +
		"- 但整个画面不得完全冻结超过 3 秒（含结尾）：长停留期间用那条环境动效或极缓的镜头推移维持画面活性，" +
		"语义元素本身保持不动。\n" +
		"- 装饰性动效只能落在装饰层（显式标记 decorative）或语义元素的入场 / 退场窗口内，" +
		"不得侵占任何元素的可读稳定区间，也不得改变已约定的短语帧。\n"
}

// algorithmicArtPrompt 是生成式艺术技能的提示词片段。
// hyperFramesCorePrompt 指向组合 HTML 的授权契约。
//
// 上游 SKILL.md 的原话是 "Read before writing composition HTML"——它定义
// data-* 时间属性、class="clip"、track 结构和确定性渲染规则，写组合前不读
// 就只能靠猜，而猜错的表现往往是渲染成功但时间轴不对。
//
// 同时要收窄范围：该技能还覆盖建子项目和 STORYBOARD.md / SCRIPT.md 计划格式，
// 那是整片工作流的东西。分镜由 am 决定、已经写进 scene.json，渲染器照着那套
// 走会产出计划文件并试图自己排布多镜头，直接违反单镜头契约。
func hyperFramesCorePrompt(skillsDir string) string {
	reference := fmt.Sprintf("- 写组合 HTML 前必须读 %s 技能。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		HyperFramesCoreSkillName)
	if skillsDir != "" {
		reference = fmt.Sprintf("- 写组合 HTML 前必须读 %s 技能。本机该技能目录为：\n    %s\n",
			HyperFramesCoreSkillName, filepath.Join(skillsDir, HyperFramesCoreSkillName))
	}
	return "组合规范（强制）：\n" + reference +
		"  重点是 data-* 时间属性、class=\"clip\"、track 结构与确定性渲染规则。\n" +
		"- 本镜头只产出一个 composition。不建子项目，不写 STORYBOARD.md / SCRIPT.md 计划文件——" +
		"分镜由上层决定并已写进 scene.json，你只负责这一镜。\n"
}

// hyperFramesCLIPrompt 指向 CLI 用法，并把可用命令收窄到本地渲染与检查。
//
// 上游该技能覆盖 cloud / cloudrun / lambda / publish 等远端渲染路径，以及
// skills / upgrade 这类会改动已装技能的命令。前者会把镜头送去云端渲染，绕开
// 本机的确定性前提；后者会顶掉 am 固定的技能版本——两类都是执行契约明令禁止的。
func hyperFramesCLIPrompt(skillsDir string) string {
	reference := fmt.Sprintf("- CLI 的命令与排错方式见 %s 技能。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		HyperFramesCLISkillName)
	if skillsDir != "" {
		reference = fmt.Sprintf("- CLI 的命令与排错方式见 %s 技能。本机该技能目录为：\n    %s\n",
			HyperFramesCLISkillName, filepath.Join(skillsDir, HyperFramesCLISkillName))
	}
	return "渲染命令（强制）：\n" + reference +
		"- 只用本地渲染与检查相关的命令（render、check、snapshot）。preview 需要浏览器交互，本环境是无头的，不要用。\n" +
		"- snapshot 一律带 --describe false：它默认会把画面发给外部视觉服务，本镜头的自查由你自己看图完成。\n" +
		"- 禁止 cloud、cloudrun、lambda、publish 等远端渲染路径：本镜头必须在本机渲染。\n" +
		"- 禁止 skills、upgrade 以及任何会改动已装技能的命令：版本已固定，改动会影响其他镜头。\n"
}

// hyperFramesCreativePrompt 把渲染器指向上游的构图与设计判断参考。
//
// 上游 SKILL.md 原话把 house-style.md 与 video-composition.md 称为避免
// 「generic, web-page-looking output」的首要读物——那正是本项目产出最常见的
// 毛病。design-adherence.md 则是写完之后对照设计规范自查的清单，供画面自查用。
//
// 同时要收窄范围：该技能还覆盖旁白与整片节拍规划。旁白和分镜由上层决定，
// 渲染器只负责当前镜头，不替整片做计划。视觉方向则由当前镜头自主决定。
func hyperFramesCreativePrompt(skillsDir string) string {
	reference := fmt.Sprintf("- 设计本镜头版式前读 %s 技能的 references/house-style.md 与 references/video-composition.md，"+
		"画面自查时对照 references/design-adherence.md。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		HyperFramesCreativeSkillName)
	if skillsDir != "" {
		reference = fmt.Sprintf("- 设计本镜头版式前读 %s 技能的 references/house-style.md 与 references/video-composition.md，"+
			"画面自查时对照 references/design-adherence.md。本机该技能目录为：\n    %s\n",
			HyperFramesCreativeSkillName, filepath.Join(skillsDir, HyperFramesCreativeSkillName))
	}
	return "构图与版式参考：\n" + reference +
		"- 这些文件只是可选的构图参考；只取你认为有助于当前镜头的内容，不把它们当作项目级视觉规范。" +
		"配色、字体、版式与动效由当前镜头自主决定；安全区仍遵守画布的技术约束。" +
		"旁白、分镜与整片节拍已由上层决定，不做整片规划。\n"
}

func algorithmicArtPrompt(skillsDir string) string {
	if skillsDir != "" {
		skill := filepath.Join(skillsDir, AlgorithmicArtSkillName)
		return fmt.Sprintf("生成式艺术参考：\n"+
			"- 如需使用算法艺术/生成式视觉效果，加载 %s 技能。本机该技能目录为：\n    %s\n"+
			"  读取其中的 SKILL.md（工作流与不变量）和 references/ 目录（技术目录、配色规则、动画配方）。\n"+
			"- 该技能提供 50+ 算法技术（10 个分类）、OKLab 配色插值、完美循环动画模板。\n"+
			"- 使用时遵守反默认规则：禁止单独使用 Perlin 流场/粒子轨迹/圆填充/递归树/Voronoi 作为唯一想法，\n"+
			"  必须组合两个正交技术或使用目录中更冷门的算法。\n",
			AlgorithmicArtSkillName, skill)
	}
	return fmt.Sprintf("生成式艺术参考：\n"+
		"- 如需使用算法艺术/生成式视觉效果，加载 %s 技能。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n"+
		"- 该技能提供 50+ 算法技术、OKLab 配色、完美循环动画。使用时遵守反默认规则。\n",
		AlgorithmicArtSkillName)
}

// textToLottiePrompt 是 Lottie 图层技能的提示词片段。
//
// Lottie 是可选项而非默认项：烘进 JSON 的东西后期改不动，且文字与数据类动画
// 交给 Lottie 会与动效 rule 体系给出互相冲突的指令。因此这里的措辞必须把
// 「默认不用」和「不计入动效预算」两条写死，否则渲染器会拿一个 Lottie 顶替
// 整镜头的动效工作量。
func textToLottiePrompt(skillsDir string) string {
	reference := fmt.Sprintf(
		"- 需要时加载 %s 技能。\n"+
			"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		TextToLottieSkillName)
	if skillsDir != "" {
		skill := filepath.Join(skillsDir, TextToLottieSkillName)
		reference = fmt.Sprintf(
			"- 需要时加载 %s 技能。本机该技能目录为：\n    %s\n"+
				"  读取其中的 %s 判断该不该用，再按其路由表只读匹配的 references/ 文件；\n"+
				"  若该路径不存在，改用你自身的技能加载机制载入 %s。\n",
			TextToLottieSkillName, skill, skillManifestFile, TextToLottieSkillName)
	}
	return "Lottie 图层（可选）：\n" + reference +
		"- 默认不用 Lottie。只有矢量图形、时间线自成一体、且用 CSS/GSAP 表达明显更笨重时才考虑，" +
		"典型是 logo 演绎、图标变形、loader、状态反馈、SVG 描边绘制、矢量质感特效。\n" +
		"- 文字排版、逐字动画、数字滚动、图表、转场与运镜一律不得交给 Lottie，" +
		"这些归 HTML 与动效 rule 体系。任何时候都不得产出铺满画幅、承担镜头全部内容的 Lottie。\n" +
		"- Lottie 图层不计入上述动效预算的任何一条 rule，持续性环境动效也不得用 Lottie 循环顶替；" +
		"HTML 侧必须独立满足全部动效要求。\n" +
		"- Lottie 由渲染框架逐帧 seek：播放器实例 autoplay 与 loop 均为 false 并注册到框架约定的全局数组，" +
		"JSON 内不得使用表达式、时钟或未播种随机，粒子与物理一类系统必须烘成关键帧。\n"
}

// characterRigPrompt 是角色驱动技能的提示词片段。
//
// 这里刻意不写驱动库的文件名，也不写任何角色契约的细节：那些属于
// castSection，只在 scene.Cast != nil 时才出现。没有角色的镜头必须拿不到
// 任何角色相关的强制要求——老镜头（单口播）的提示词不能因为新增了这个技能
// 就多出一段它永远用不上的契约，internal/scene 的
// TestBuildPromptOmitsCastSectionWhenNil 守着这一点。
//
// 本段只做一件事：把技能目录的本机绝对路径交出去。castSection 里那条
// 「把驱动库复制进镜头目录」的契约需要一个源目录，而它就在这里。
func characterRigPrompt(skillsDir string) string {
	if skillsDir != "" {
		return fmt.Sprintf("角色驱动（按需）：\n"+
			"- 本镜头如有角色出场（下方出现「%s」段），必须加载 %s 技能。本机该技能目录为：\n    %s\n"+
			"  实现前必须读其中的 %s：它定义角色包目录结构、驱动库的挂载与转身/说话接口，以及四条硬规则。\n"+
			"- 没有角色的镜头忽略本段，不要自行引入角色。\n",
			CastRequiredHeading, CharacterRigSkillName, filepath.Join(skillsDir, CharacterRigSkillName), skillManifestFile)
	}
	return fmt.Sprintf("角色驱动（按需）：\n"+
		"- 本镜头如有角色出场（下方出现「%s」段），必须加载 %s 技能。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n"+
		"- 没有角色的镜头忽略本段，不要自行引入角色。\n",
		CastRequiredHeading, CharacterRigSkillName)
}

// sceneKitPrompt 是按帧驱动工具库的提示词片段，解析到就总是注入。
//
// 这个库存在的理由是让各镜头不再各写各的：字幕位置、字号、断行每镜不同，
// frame.md 的字幕规范没有任何共用代码去执行。所以措辞必须把「烧录字幕一律
// 用 SceneKit.captions」「版式用 safeRect 而不是手抄像素」写成要求而不是建议，
// 否则渲染器照旧手写一套，库就白装了。
//
// 复制义务与 character-rig 同理：渲染只服务镜头目录内的文件，引用镜头目录外的
// 路径会静默 404，字幕不出现、渲染照样退出码 0。
func sceneKitPrompt(skillsDir string) string {
	reference := fmt.Sprintf("- 加载 %s 技能，用法以其中的 %s 为准。\n"+
		"  （本机未能定位该技能目录，请使用你自身的技能加载机制载入。）\n",
		SceneKitSkillName, skillManifestFile)
	if skillsDir != "" {
		reference = fmt.Sprintf("- 加载 %s 技能，用法以其中的 %s 为准。本机该技能目录为：\n    %s\n",
			SceneKitSkillName, skillManifestFile, filepath.Join(skillsDir, SceneKitSkillName))
	}
	return "镜头工具库（强制）：\n" + reference +
		"- 用到它时把技能目录里的 " + sceneKitLibraryFile + " 复制进本镜头目录，用相对路径引入；不得引用镜头目录之外的文件。\n" +
		"- 先加载镜头目录里的 " + SceneDataFile + "（由上层在渲染前写好，不要自己写或改），再加载 " + sceneKitLibraryFile + "。\n" +
		"- 画面内烧录的字幕一律用 SceneKit.captions，不得手写字幕代码；位置、字号、断行与样式由本镜头决定，但必须保持可读。\n" +
		"- 版式按安全区取值用 SceneKit.safeRect，不要把安全区像素手抄进合成；短语时间窗用 SceneKit.phrase。\n"
}

func songExplainerPrompt(dir string) string {
	if dir == "" {
		return "读取 song-explainer 技能，仅执行单镜头歌词驱动动画规则。\n"
	}
	return fmt.Sprintf("读取 %s，仅执行单镜头歌词驱动动画规则。\n", filepath.Join(dir, SongExplainerSkillName, skillManifestFile))
}

// RendererAccess 列出渲染器在安全模式下必须被放行的最小权限。
//
// 命令与提示词、镜头锁定文件要求的完全一致：CLI 必须带固定版本前缀，
// ls / ffprobe 用来查素材与产物。技能树在项目根的 .agents/skills，位于镜头
// 目录之外，不开放读取的话渲染器读不到动效 rule 索引，只能凭空写动画。
func RendererAccess(resolved map[string]string) tools.RendererAccess {
	seen := map[string]bool{}
	var dirs []string
	for _, dir := range resolved {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return tools.RendererAccess{
		Commands: []string{"npx --yes hyperframes@" + hyperframes.PinnedVersion, "ls", "ffprobe"},
		ReadDirs: dirs,
	}
}
