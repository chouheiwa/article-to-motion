package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	assets "github.com/chouheiwa/articale-to-motion"
	"github.com/chouheiwa/articale-to-motion/internal/archive"
	"github.com/chouheiwa/articale-to-motion/internal/cast"
	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/hyperframes"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/project"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/chouheiwa/articale-to-motion/internal/song"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
	"github.com/chouheiwa/articale-to-motion/internal/validate"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Version 是 `am --version` 输出的版本号。
//
// 由发布流程在构建时按 tag 注入（-ldflags "-X …/internal/cli.Version=1.2.0"），
// 本地构建保持 dev。它曾是写死的 "1.0.0" 常量，发 v1.1.0 时没人改，下载到的
// 二进制一直自称 1.0.0——版本号必须只有 tag 这一个来源。
var Version = "dev"

// hyperframesVersion 是本包内的简写，真相在 internal/hyperframes.PinnedVersion
// ——那里同时是下发提示词里锁定指令的取值来源，避免两处字面量各自漂移。
const hyperframesVersion = hyperframes.PinnedVersion

// narrationSolo、narrationCast 是 am init --narration 的两个合法取值。
//
// solo 是默认的单口播模式，不写任何 cast 相关文件；cast 是多角色对话模式，
// 会额外写出 cast.yaml 与 cast/，见 writeCastScaffold。
const (
	narrationSolo = "solo"
	narrationCast = "cast"
)

// castInitYAML 构造 am init --narration cast 要写出的 cast.yaml 内容。
//
// 走 cast.Roster{} + yaml.Marshal，而不是手写字符串拼 YAML：与 am cast add
// 的 registerCastPack 共用同一条构造路径，Roster/Defaults 以后加字段时两条
// 命令会一起跟上，不会因为这里是手写模板而悄悄漂移出一份过时的默认值。
// defaults 取值与 registerCastPack 新建 cast.yaml 时完全一致（见 cast.go 的
// castDefaultGroundY、castDefaultGapMs）；Packs 留空（零值 nil），
// yaml.Marshal 序列化为 `packs: []`。
func castInitYAML() ([]byte, error) {
	roster := cast.Roster{
		Schema:   cast.SchemaVersion,
		Defaults: cast.Defaults{GroundY: castDefaultGroundY, GapMs: castDefaultGapMs},
	}
	body, err := yaml.Marshal(roster)
	if err != nil {
		return nil, fmt.Errorf("序列化 %s 失败：%w", cast.RosterFile, err)
	}
	return body, nil
}

// writeCastScaffold 在 target 下写出 cast.yaml 并建出空的 cast/ 目录。
//
// packs 刻意留空：am init 刚建出的项目本来就还没有引入任何角色包，班底为空
// 由 internal/cast.LoadRoster（允许空 packs）和后续的项目级发布前校验负责
// 报告，不在这里塞占位角色，也不在这里判空报错——那会让「init 就是要建一个
// 还没有角色的项目」这个正常状态被误判成错误。
//
// 与 project.Initialize 的幂等语义保持一致：cast.yaml 已存在且内容相同就
// 跳过，内容不同就报错，不做静默覆盖或合并。
func writeCastScaffold(target string) error {
	castDir := filepath.Join(target, "cast")
	if err := os.MkdirAll(castDir, 0o755); err != nil {
		return fmt.Errorf("创建 %s 目录失败：%w", cast.RosterFile, err)
	}
	rosterPath := filepath.Join(target, cast.RosterFile)
	body, err := castInitYAML()
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(rosterPath); err == nil {
		if !bytes.Equal(existing, body) {
			return fmt.Errorf("目标文件已存在且内容不同：%s", rosterPath)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查 %s 失败：%w", rosterPath, err)
	}
	if err := fsutil.AtomicWrite(rosterPath, body, 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", rosterPath, err)
	}
	return nil
}

func currentEnvironment() map[string]string {
	return envutil.EnvMap()
}

func addExecutableLocation(env map[string]string) {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告：无法获取可执行文件路径：%v\n", err)
		return
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告：无法解析可执行文件路径：%v\n", err)
		return
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	env["AM_EXECUTABLE"] = executable
	directory := filepath.Dir(executable)
	if current := env["PATH"]; current != "" {
		env["PATH"] = directory + string(os.PathListSeparator) + current
	} else {
		env["PATH"] = directory
	}
}

func Execute(args []string, stdout, stderr io.Writer) int {
	return ExecuteContext(context.Background(), args, stdout, stderr)
}

func ExecuteContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	root.SetContext(ctx)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		var coded *exitError
		if errors.As(err, &coded) {
			return coded.code
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return 130
		}
		if strings.Contains(err.Error(), "缺少必需工具") {
			return 127
		}
		return 1
	}
	return 0
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	unsafe := false
	root := &cobra.Command{
		Use:   "am",
		Short: "ArticleToMotion 竖屏 MG 视频制作工具",
		Long: `ArticleToMotion 竖屏 MG 视频制作工具。

把定稿 SRT 拆成并发渲染的动画镜头，或从文章/口播稿走完 TTS、字幕、封面、
声音与发布交付的全流程。渲染由外部 AI CLI 完成，am 负责契约、调度与校验。

两种入口：
  am run                          项目已有定稿 transcription.srt，读项目根 PROMPT.md
  am run PROMPT-PRODUCTION.md     从文章或口播稿开始完整制作
  am scene run / run-all          只跑镜头渲染，可脱离 am run 单独使用

配置解析优先级（高到低）：环境变量 > 项目 .env > article-to-motion.conf > 内置默认。
用 am config get 读取解析结果，不要自己解析配置文件。
项目 .env 不允许出现以 API_KEY / TOKEN / SECRET / PASSWORD 结尾的键，出现则拒绝启动。

外部 AI CLI 默认运行在限制于项目工作区的安全模式。安全模式下子进程只继承白名单
环境变量加上 .env 覆盖层；缺变量用 AM_PASSTHROUGH_ENV=NAME1,NAME2 显式列出，
不要改用 --unsafe 绕过隔离。

退出码：
  0    成功
  1    一般失败（参数、校验、配置、IO）
  127  缺少必需的外部命令
  130  被中断（SIGINT / 上下文取消）
  其他 透传子进程退出码：am run 用编排工具的退出码，am scene run-all 用镜头汇总退出码

所有错误写 stderr，正常输出写 stdout；失败时不打印 usage，只打印一行错误。`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().BoolVar(&unsafe, "unsafe", false,
		"关闭 AI CLI 的权限隔离（危险）。只对会启动 AI CLI 的 run / scene run / scene run-all 生效，"+
			"对 init、config、validate、archive 无作用；会传递给嵌套镜头任务")

	var skipHyperframes bool
	var canvasID string
	var styleID string
	var styleBrief string
	var styleBriefFile string
	var narration string
	var delivery string
	initCmd := &cobra.Command{
		Use:   "init [DIR]",
		Short: "初始化一个可复现的视频项目",
		Long: `在 DIR（省略则为当前目录）写入视频项目骨架。

画幅在初始化时一次性选定并写入 frame.md，之后不再更改；改画幅意味着重建项目。
可选画幅（均 30fps）：
` + canvasOptions() + `
非交互环境（CI、管道）必须显式传 --canvas，不会静默取默认值；不传且无法交互时报错退出。

风格同样在初始化时一次性选定，与画幅正交：画幅决定画布与安全区，风格决定
配色、字体、版式骨架、动效语法与禁用项。用 --style 指定，不传时在终端里交互
选择；非交互环境不传则取默认风格 ` + preset.DefaultStyle().ID + `。改风格同样意味着重建项目。
可选风格：
` + styleOptions() + `

--style ` + preset.CustomStyleFlag + ` 不套用内置风格，改由 --style-brief 或 --style-brief-file 给出文字
描述。init 只写入描述（` + styleBriefPath + `）与一份草稿 frame.md；am run 在制作开始前
执行定风格阶段，把描述落成 frame.md、说明书与四张示例图。定稿并通过
am validate style 之前，镜头不会开始渲染。

写入内容：
  PROMPT.md、PROMPT-PRODUCTION.md、article-to-motion.conf、.env.example
  frame.md、docs/ 风格说明书、assets/fonts/ 中文字体、assets/style-guide/ 示例
  templates/project-rules.md（am run 据此派生 AGENTS.md / CLAUDE.md 等）
  .agents/skills/ 内置技能树（text-to-lottie、algorithmic-art），随二进制下发

不覆盖内容不同的已有文件：目标已存在且字节不同就整体失败并回滚，不做合并。
字节相同则视为已存在并跳过，因此对同一画幅重复执行是幂等的，可安全用于补齐缺失文件。

默认联网安装固定版本 ` + hyperframesVersion + ` 的 HyperFrames 官方技能，
装进项目的 .agents/skills/，与内置技能树同一个目录。离线或 CI 用
--skip-hyperframes 跳过；该开关不影响内置技能树，它随二进制下发。

技能装进项目而不是装进 HOME，是因为项目之间互不覆盖只有这样才成立：上游
安装器只认 homedir，一台机器上只有一份技能，两个项目固定不同版本时谁后
初始化谁说了算。装进项目还让项目自包含——整个目录拷到另一台机器就能渲染。
安装全程不写用户 HOME：上游安装器在项目外的临时目录里运行，产物再搬进项目。

技能不走上游安装器：它只克隆仓库默认分支，没有指定 tag 的口子，同一个版本号
隔几天装出来的技能可以不同。am 自己按 tag 浅克隆并校验 commit 与技能数量，
两次初始化的技能树逐字节一致。安装后在 .agents/skills/` + hyperframes.ManifestFile + `
里记下版本、上游 commit 与每个技能的内容摘要。安装技能需要 git。

上游技能与内置技能重名时保留内置版本并告警，不会覆盖本仓库 fork 过的技能。

叙事模式由 --narration 一次性选定，默认 solo（单口播）。传 cast 会额外在
项目根写 cast.yaml 并建出空的 cast/ 目录，进入多角色对话叙事模式；此时
production 阶段的 TTS 与时间线装配规程改为遵守随项目下发的
PROMPT-CAST-ADDENDUM.md，具体规则见该文件。cast.yaml 刚建出时 packs 为空
列表，这是预期状态：项目在第一次 am cast add / am cast new 之前本来就还
没有可用角色。`,
		Example: `  # 交互选择画幅
  am init my-video

  # 非交互环境必须显式指定画幅
  am init my-video --canvas vertical-9x16

  # 指定风格
  am init my-video --canvas vertical-9x16 --style ` + preset.DefaultStyle().ID + `

  # 不套用内置风格，按文字描述定制
  am init my-video --canvas vertical-9x16 --style ` + preset.CustomStyleFlag + ` --style-brief "暖色胶片质感，手写标注，低饱和"

  # 离线或 CI：跳过联网安装 HyperFrames 技能
  am init my-video --canvas vertical-3x4 --skip-hyperframes

  # 在当前目录补齐缺失的骨架文件（幂等）
  am init --canvas vertical-3x4

  # 多角色对话叙事：额外写出 cast.yaml 与 cast/
  am init my-story --canvas vertical-3x4 --narration cast`,
		Args: maxArgs(1, "DIR"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if narration != narrationSolo && narration != narrationCast {
				return fmt.Errorf("无效的 --narration: %s（可选：%s %s）", narration, narrationSolo, narrationCast)
			}
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			target, _ = filepath.Abs(target)
			if delivery != "speech" && delivery != "song" {
				return fmt.Errorf("--delivery 必须为 speech 或 song")
			}
			if (delivery == "song" && (narration == narrationCast || cast.HasRoster(target))) || (narration == narrationCast && song.Enabled(target)) {
				return fmt.Errorf("首版不支持 song + cast")
			}
			chosen, err := resolveCanvas(canvasID, os.Stdin, stdout)
			if err != nil {
				return err
			}
			style, err := resolveStyle(styleID, os.Stdin, stdout)
			if err != nil {
				return err
			}
			brief, err := resolveStyleBrief(style, styleBrief, styleBriefFile, os.Stdin, stdout)
			if err != nil {
				return err
			}
			shared, err := assets.Shared()
			if err != nil {
				return err
			}
			presetFiles, err := assets.Preset(chosen.ID)
			if err != nil {
				return err
			}
			styleFiles, err := assets.Style(style.ID, chosen.ID)
			if err != nil {
				return err
			}
			extra, err := assets.StyleFonts(style)
			if err != nil {
				return err
			}
			if brief != "" {
				extra[styleBriefPath] = styleBriefDocument(brief)
			}
			if delivery == "song" {
				extra["song.yaml"] = []byte(song.DefaultConfig)
			}
			result, err := project.InitializeWithFiles(target, extra, shared, presetFiles, styleFiles)
			if err != nil {
				return err
			}
			if narration == narrationCast {
				if err := writeCastScaffold(target); err != nil {
					return fmt.Errorf("项目文件已写入，但写出 %s 失败：%w", cast.RosterFile, err)
				}
			}
			fmt.Fprintf(stdout, "项目已初始化：%s（画幅 %s，风格 %s，新增 %d，未变 %d）\n", target, chosen.Label, style.Name, result.Created, result.Unchanged)
			if style.ID == preset.CustomStyle.ID {
				fmt.Fprintf(stdout, "自定义风格尚未定稿：am run 会先执行定风格阶段，按 %s 生成风格；定稿前 am validate style 会报未完成，镜头也不会开始渲染\n", styleBriefPath)
			}
			if skipHyperframes {
				fmt.Fprintln(stdout, "警告：已跳过 HyperFrames 技能安装")
				return nil
			}
			builtin, err := assets.BuiltinSkills()
			if err != nil {
				return err
			}
			installed, err := hyperframes.Install(cmd.Context(), hyperframes.Options{
				ProjectDir: target,
				Version:    hyperframesVersion,
				Env:        currentEnvironment(),
				Protected:  builtin,
				Output:     stderr,
			})
			if err != nil {
				return fmt.Errorf("项目文件已写入，但 HyperFrames 技能安装失败：%w", err)
			}
			fmt.Fprintf(stdout, "HyperFrames %s 技能已装入 %s（%d 个）\n",
				hyperframesVersion, hyperframes.SkillsSubdir, len(installed.Installed))
			for _, name := range installed.Protected {
				fmt.Fprintf(stderr, "警告：上游技能 %s 与内置技能重名，已保留内置版本\n", name)
			}
			return nil
		},
	}
	initCmd.Flags().BoolVar(&skipHyperframes, "skip-hyperframes", false, "跳过联网安装 HyperFrames 官方技能；不影响随二进制下发的内置技能树")
	initCmd.Flags().StringVar(&canvasID, "canvas", "", "画幅预设："+strings.Join(preset.IDs(), " | ")+"；不传则在终端里交互选择")
	initCmd.Flags().StringVar(&styleID, "style", "", "视觉风格："+strings.Join(append(preset.StyleIDs(), preset.CustomStyleFlag), " | ")+"；不传则在终端里交互选择，非交互环境取默认风格")
	initCmd.Flags().StringVar(&styleBrief, "style-brief", "", "自定义风格的文字描述，只能与 --style "+preset.CustomStyleFlag+" 一起使用")
	initCmd.Flags().StringVar(&styleBriefFile, "style-brief-file", "", "从文件读取自定义风格的描述，与 --style-brief 二选一")
	initCmd.Flags().StringVar(&narration, "narration", narrationSolo,
		"叙事模式："+narrationSolo+"（单口播，默认） | "+narrationCast+"（多角色对话，额外写出 cast.yaml 与 cast/）")
	initCmd.Flags().StringVar(&delivery, "delivery", "speech", "讲解形式：speech（默认）或 song（单人整曲）")
	root.AddCommand(initCmd)

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "读取解析后的配置",
		Long: `读取 am 解析后的配置值。

不要自己去读 article-to-motion.conf 或 .env——解析涉及四层优先级
（环境变量 > 项目 .env > article-to-motion.conf > 内置默认），只有本命令的
输出等于 am 实际会用的值。`,
	}
	configCmd.AddCommand(&cobra.Command{
		Use:   "get ORCHESTRATOR|RENDERER|TTS_PROVIDER|ALIGN_PROVIDER",
		Args:  exactArgs(1, "KEY"),
		Short: "打印单个配置键",
		Long: `把单个配置键的解析结果打印到 stdout，只有值本身加一个换行，便于直接捕获。

可用键：
  ORCHESTRATOR   编排工具：codex | claude | qoder | codebuddy | opencode
  RENDERER       渲染工具：同上取值范围
  TTS_PROVIDER   语音合成：` + strings.ReplaceAll(config.TTSProviderIDs(), " ", " | ") + `
  ALIGN_PROVIDER 已知文本强制对齐：` + strings.ReplaceAll(config.AlignerIDs(), " ", " | ") + `
                 auto 在 Apple Silicon 上解析为 mlx，其余平台为 funasr；
                 本命令打印的是解析后的具体工具，不会是 auto

在当前工作目录解析配置，因此要在项目根运行。键名未知或解析出的值非法时退出码 1。`,
		Example: `  # 捕获解析后的渲染工具
  renderer="$(am config get RENDERER)"

  # 环境变量优先级最高，可临时覆盖
  RENDERER=codex am config get RENDERER`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(".", nil)
			if err != nil {
				return err
			}
			values := map[string]string{"ORCHESTRATOR": cfg.Orchestrator, "RENDERER": cfg.Renderer, "TTS_PROVIDER": cfg.TTSProvider, "ALIGN_PROVIDER": cfg.AlignProvider}
			value, ok := values[args[0]]
			if !ok {
				return fmt.Errorf("未知配置键：%s", args[0])
			}
			fmt.Fprintln(stdout, value)
			return nil
		},
	})
	root.AddCommand(configCmd)

	sceneCmd := &cobra.Command{
		Use:   "scene",
		Short: "镜头相关操作",
		Long: fmt.Sprintf(`渲染镜头。可以在 am run 的编排流程里被调用，也可以完全独立使用。

一个镜头是一个目录，至少包含：
  scene.json   执行契约，字段见下
  prompt.md    本镜头的创意方向（可选，缺失时使用内置的通用创意方向）
  transcript   完整字幕，供渲染工具理解上下文

scene.json 只接受 %d 个必填字段 %s，
以及 %d 个可选字段 %s（%s 是多角色叙事的角色配置，见 README「多角色叙事」一节）。
出现任何未知字段直接失败。这份字段清单与 internal/scene.Load 的校验逻辑同源（见
internal/scene/scene.go 的 requiredSceneFields / optionalSceneFields），改字段
集时两处不会漂移。
duration_seconds 保留毫秒精度写成小数秒（如 2.833），不得取整。
text 不得含 [[USER_MESSAGE]] 或 <scene-text> 定界标记，含则失败。

渲染工具由 scene.json 的 renderer 决定，未声明则取配置的 RENDERER；
解析不到直接报错，不存在静默回退。

渲染输出中只有以 [[USER_MESSAGE]] 开头的行会转给用户，其余进日志文件：
  render-<镜头编号>.stream.jsonl / .stderr.log / .user.log`,
			len(scene.RequiredFields()), strings.Join(scene.RequiredFields(), "、"),
			len(scene.OptionalFields()), strings.Join(scene.OptionalFields(), "、"), "cast"),
	}
	var tolerance float64
	runScene := &cobra.Command{
		Use: "run DIRECTORY", Args: exactArgs(1, "DIRECTORY"), Short: "执行单个镜头",
		Long: `渲染 DIRECTORY 这一个镜头，产出 scene.json 里 output 指定的 MP4。

无条件重新渲染：不做已有产物检查，也没有 --force。需要跳过已完成镜头时用 run-all。

渲染完成后校验产物时长是否落在 duration_seconds ± --duration-tolerance 内，
超出则失败。容差必须是有限非负数。

启动前解析技能目录并把绝对路径写进渲染提示词。动效技能未找到时打印警告并降级为
按技能名引用，不中断渲染——警告写 stderr，成片动效质量会下降，值得处理。`,
		Example: `  am scene run scenes/scene-001

  # 放宽产物时长容差到 0.3 秒
  am scene run scenes/scene-001 --duration-tolerance 0.3`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTolerance(tolerance); err != nil {
				return err
			}
			cfg, err := config.Load(".", nil)
			if err != nil {
				return err
			}
			s, err := scene.Load(args[0])
			if err != nil {
				return err
			}
			return scene.Run(cmd.Context(), s, cfg, envutil.IsUnsafe(unsafe), currentEnvironment(), stdout, tolerance)
		},
	}
	runScene.Flags().Float64Var(&tolerance, "duration-tolerance", 0.15, "产物时长与 duration_seconds 的允许偏差（秒），有限非负数")
	sceneCmd.AddCommand(runScene)

	var jobs, retries int
	var force bool
	var reportPath, srtPath, songTimelinePath string
	var strictCoverage bool
	var coverageTolerance float64
	runAll := &cobra.Command{
		Use: "run-all ROOT", Args: exactArgs(1, "ROOT"), Short: "并行执行多个镜头",
		Long: `并发渲染 ROOT 下的所有镜头。镜头之间无创作依赖，跨镜头视觉一致性由 frame.md 保证。

每个镜头开跑前先判定状态，判定结果直接决定要不要重渲染：
  已有合格产物   产物存在且通过时长校验 → 跳过
  Stale         scene.json 或 prompt.md 的修改时间晚于产物 → 不自动重渲染，
                标记为需要显式 --force，避免悄悄覆盖人工确认过的成片
  其余           渲染

--force 跳过全部上述判定，无条件重渲染每个镜头。

--jobs 取值 1..16，缺省读配置 SCENE_JOBS（其自身默认为 3）。--retries 取值 0..5。
越界直接失败，不做截断。

重试只针对渲染器非零退出，退避 10 秒与 30 秒。产物规格校验失败不重试：
同样的提示词和渲染器重跑只会得到同样不合规的产物，而每次重试都是一次完整的
AI CLI 调用。这类失败要先改提示词或时长声明再重跑。

渲染开始前先做覆盖校验：镜头编号是否唯一，以及给了 --srt 时，镜头总时长是否
等于字幕跨度（容差 --coverage-tolerance，默认 0.1 秒）。默认只警告，
--strict-coverage 让它在渲染前直接失败——漏掉一镜要等全部渲染完拼接时才发现，
那时每一镜都已经烧掉一次完整的 AI CLI 调用。

被中断（SIGINT）时停止派发新任务、终止在飞进程组，并写出部分报告。

退出码在有镜头失败时非 0，由汇总报告决定；此时 --report-json 写出的文件仍然完整，
应当读它来判断是哪些镜头失败，而不是解析 stdout 的人类可读表格。`,
		Example: `  am scene run-all scenes/

  # 并发 3、失败重试 2 次，并写出机器可读报告
  am scene run-all scenes/ --jobs 3 --retries 2 --report-json production/run-report.json

  # 渲染前先核对镜头拆分是否完整覆盖字幕，不通过就不开工
  am scene run-all scenes/ --srt transcription.srt --strict-coverage

  # 输入改过、镜头被判为 Stale 时，显式重渲染
  am scene run-all scenes/ --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if songTimelinePath != "" && srtPath != "" {
				return fmt.Errorf("--song-timeline 与 --srt 互斥")
			}
			if err := validateTolerance(tolerance); err != nil {
				return err
			}
			cfg, err := config.Load(".", nil)
			if err != nil {
				return err
			}
			if jobs == 0 {
				jobs = cfg.SceneJobs
			}
			if jobs < 1 || jobs > 16 || retries < 0 || retries > 5 {
				return fmt.Errorf("--jobs 必须为 1..16，--retries 必须为 0..5")
			}
			scenes, err := schedule.Plan(args[0])
			if err != nil {
				return err
			}
			// 覆盖校验在渲染之前跑：漏掉一镜或某镜时长写错，等全部渲染完
			// 拼接时才发现的话，每一镜都已经烧掉一次完整的 AI CLI 调用。
			span := 0.0
			if songTimelinePath != "" {
				path, err := filepath.Abs(songTimelinePath)
				if err != nil {
					return err
				}
				songRoot := filepath.Dir(filepath.Dir(filepath.Dir(path)))
				if path != filepath.Join(songRoot, song.Store, "timeline.json") {
					return fmt.Errorf("--song-timeline 必须指向 production/song/timeline.json")
				}
				t, err := song.LoadTimeline(songRoot)
				if err != nil {
					return err
				}
				if err = songCoverage(songRoot, scenes, t, true); err != nil {
					return err
				}
			} else if song.Enabled(".") {
				return fmt.Errorf("歌曲项目必须提供 --song-timeline production/song/timeline.json")
			}
			if srtPath != "" {
				parsed, err := srt.ReadSpan(srtPath)
				if err != nil {
					return err
				}
				span = parsed.Seconds()
			}
			if problems := schedule.CoverageProblems(scenes, span, coverageTolerance); len(problems) > 0 {
				message := fmt.Sprintf("镜头覆盖校验未通过：\n  - %s", strings.Join(problems, "\n  - "))
				if strictCoverage {
					return errors.New(message)
				}
				fmt.Fprintf(stderr, "警告：%s\n用 --strict-coverage 让这类问题在渲染前直接失败。\n", message)
			}
			// 非整数帧时长会让渲染器多出一帧，拼接时才暴露；渲染前一次列全。
			var misaligned []string
			for _, s := range scenes {
				if err := scene.VerifyFrameAlignment(s); err != nil {
					misaligned = append(misaligned, err.Error())
				}
			}
			if len(misaligned) > 0 {
				return fmt.Errorf("镜头时长未对齐整数帧：\n  - %s", strings.Join(misaligned, "\n  - "))
			}
			for _, s := range scenes {
				if s.Song != nil && songTimelinePath == "" {
					return fmt.Errorf("歌曲镜头必须提供 --song-timeline production/song/timeline.json")
				}
				if err := scene.VerifySongInputs(s); err != nil {
					return err
				}
			}
			initial := make(map[string]error)
			if !force {
				env := currentEnvironment()
				for _, item := range scenes {
					if _, statErr := os.Stat(item.OutputPath()); statErr != nil {
						continue
					}
					if verifyErr := scene.VerifyOutput(item, env, tolerance); verifyErr != nil {
						continue
					}
					outputInfo, statErr := os.Stat(item.OutputPath())
					if statErr != nil {
						continue
					}
					status := schedule.Skipped
					reason := "已有合格产物"
					for _, input := range []string{filepath.Join(item.Directory, "scene.json"), filepath.Join(item.Directory, "prompt.md")} {
						if info, statErr := os.Stat(input); statErr == nil && info.ModTime().After(outputInfo.ModTime()) {
							status, reason = schedule.Stale, "输入比产物新，需要显式 --force 重渲染"
							break
						}
					}
					initial[item.ID] = schedule.Outcome(status, reason)
				}
			}
			// 并发镜头共用同一个输出目标，必须串行化写入：scene.Run 会往这里写
			// 阶段消息和警告，多个 worker 同时 Fprintln 到一个非并发安全的 writer
			// 是实打实的数据竞争，写 os.Stdout 时则表现为几镜的进度输出互相交错。
			progress := &syncWriter{writer: stdout}
			// 已跳过的镜头不占渲染并发，worker 按真正要渲染的镜头数分。
			workers := schedule.RenderWorkers(runtime.NumCPU(), min(jobs, len(scenes)-len(initial)))
			report := schedule.RunAll(cmd.Context(), scenes, jobs, retries, func(ctx context.Context, s scene.Scene) error {
				if outcome, ok := initial[s.ID]; ok {
					return outcome
				}
				s = s.WithRenderWorkers(workers)
				err := scene.Run(ctx, s, cfg, envutil.IsUnsafe(unsafe), currentEnvironment(), progress, tolerance)
				// 产物规格不符是确定性的：同样的提示词和渲染器重跑只会得到同样的产物。
				// 不在这里截断的话，每个规格不符的镜头都要白烧 retries 次 AI CLI 调用。
				var verification *scene.VerificationError
				if errors.As(err, &verification) {
					return schedule.Fatal(err)
				}
				return err
			})
			fmt.Fprintln(stdout, report.Render())
			if reportPath != "" {
				if err := report.WriteJSON(reportPath); err != nil {
					return err
				}
			}
			if report.ExitCode() != 0 {
				return &exitError{code: report.ExitCode(), message: "镜头执行未全部成功"}
			}
			return nil
		},
	}
	runAll.Flags().IntVar(&jobs, "jobs", 0, "并发上限，取值 1..16；缺省读配置 SCENE_JOBS，其自身默认为 3")
	runAll.Flags().IntVar(&retries, "retries", 2, "单个镜头失败后的最大重试次数，取值 0..5")
	runAll.Flags().Float64Var(&tolerance, "duration-tolerance", 0.15, "产物时长与 duration_seconds 的允许偏差（秒），有限非负数")
	runAll.Flags().BoolVar(&force, "force", false, "跳过「已有合格产物」与 Stale 判定，无条件重渲染每个镜头")
	runAll.Flags().StringVar(&reportPath, "report-json", "", "把逐镜头结果写成 JSON 报告到该路径；镜头失败时应读它而不是解析 stdout")
	runAll.Flags().StringVar(&songTimelinePath, "song-timeline", "", "歌曲覆盖基准，与 --srt 互斥；包含前奏至尾奏")
	runAll.Flags().StringVar(&srtPath, "srt", "", "定稿字幕路径；给了就核对镜头总时长是否等于字幕跨度")
	runAll.Flags().Float64Var(&coverageTolerance, "coverage-tolerance", 0.1, "镜头总时长与字幕跨度的允许偏差（秒）")
	runAll.Flags().BoolVar(&strictCoverage, "strict-coverage", false, "覆盖校验不通过时直接失败，而不是只打印警告")
	sceneCmd.AddCommand(runAll)
	sceneCmd.AddCommand(newSceneFramesCommand(stdout))
	sceneCmd.AddCommand(newSceneLintCommand(stdout))
	root.AddCommand(sceneCmd)

	var workdir string
	runCmd := &cobra.Command{Use: "run [PROMPT]", Short: "按配置启动编排工具",
		Long: `把 prompt 文件交给配置的编排工具执行，由它驱动整条制作流程。

PROMPT 省略时读项目根的 PROMPT.md——该入口假定项目里已有定稿 transcription.srt。
传入 PROMPT-PRODUCTION.md 则从文章、口播稿或参考字幕开始完整制作。
两者都是随 am init 下发的内置 prompt，可按项目改写。找不到该文件时失败。

启动前会做三件事：
  1. 按 ORCHESTRATOR 解析出该工具会自动读取的项目级指令文件名
     （codex/qoder/opencode 读 AGENTS.md，claude 读 CLAUDE.md，codebuddy 读 CODEBUDDY.md），
     再从 templates/project-rules.md 派生并写到项目根。模板缺失时只打印警告继续，
     本次不下发项目规则；目标文件已存在且内容不同时失败，不覆盖。
  2. 把当前 am 可执行文件所在目录放到子进程 PATH 首位，并注入绝对路径为 AM_EXECUTABLE，
     因此 prompt 里的 am scene ... 会用回启动本次流程的同一个 CLI，无需把二进制拷进项目。
  3. 安全模式下按白名单构造子进程环境；额外变量用 AM_PASSTHROUGH_ENV 列出。

启动后先打印一段以 --- 结尾的头部（编排工具、渲染工具、Prompt 路径、项目规则路径），
其后全部是编排工具自身的输出。编排工具失败时透传它的退出码。`,
		Example: `  # 项目已有定稿 transcription.srt
  am run

  # 从文章或口播稿开始完整制作
  am run PROMPT-PRODUCTION.md

  # 只有确认项目与输入可信时才关闭权限隔离
  am --unsafe run

  # 安全模式下额外透传两个环境变量
  AM_PASSTHROUGH_ENV=HTTPS_PROXY,NO_PROXY am run`,
		Args: maxArgs(1, "PROMPT"), RunE: func(cmd *cobra.Command, args []string) error {
			rootDir, _ := os.Getwd()
			cfg, err := config.Load(rootDir, nil)
			if err != nil {
				return err
			}
			if workdir == "" {
				workdir = rootDir
			}
			workdir, _ = filepath.Abs(workdir)
			promptPath := filepath.Join(rootDir, "PROMPT.md")
			if song.Enabled(rootDir) {
				if _, err := song.LoadConfig(rootDir); err != nil {
					return err
				}
				promptPath = filepath.Join(rootDir, "PROMPT-SONG.md")
			}
			if len(args) == 1 {
				promptPath, _ = filepath.Abs(args[0])
			}
			prompt, err := os.ReadFile(promptPath)
			if err != nil {
				return fmt.Errorf("找不到 prompt 文件：%s", promptPath)
			}
			if song.Enabled(rootDir) {
				contract, err := assets.Files.ReadFile("assets/shared/PROMPT-SONG.md")
				if err != nil {
					return err
				}
				prompt = append(prompt, append([]byte("\n\n歌曲模式必要契约（优先于口播规程）：\n"), contract...)...)
			}
			rulesName, err := tools.ProjectRulesFilename(cfg.Orchestrator)
			if err != nil {
				return err
			}
			rulesPath, err := project.WriteRules(rootDir, rulesName)
			if err != nil {
				return err
			}
			isUnsafe := envutil.IsUnsafe(unsafe)
			argv, stdin, err := tools.OrchestratorInvocation(cfg.Orchestrator, workdir, string(prompt), isUnsafe)
			if err != nil {
				return err
			}
			binary, err := exec.LookPath(argv[0])
			if err != nil {
				return fmt.Errorf("缺少必需工具：%s", argv[0])
			}
			process := exec.Command(binary, argv[1:]...)
			process.Dir, process.Stdout, process.Stderr = workdir, stdout, stderr
			childEnvironment := cfg.ChildEnvironment(currentEnvironment(), isUnsafe, envutil.ParsePassthrough(os.Getenv("AM_PASSTHROUGH_ENV")))
			addExecutableLocation(childEnvironment)
			process.Env = envList(childEnvironment)
			if stdin != "" {
				process.Stdin = strings.NewReader(stdin)
			}
			if rulesPath == "" {
				fmt.Fprintf(stderr, "警告：项目缺少 %s，本次不下发项目规则；可运行 am init 补齐\n", project.RulesTemplate)
			}
			fmt.Fprintf(stdout, "编排工具: %s\n渲染工具: %s\nPrompt: %s\n项目规则: %s\n---\n", cfg.Orchestrator, cfg.Renderer, promptPath, rulesDisplay(rulesPath))
			if err := runProcessGroup(cmd.Context(), process); err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					return &exitError{code: exit.ExitCode(), message: "编排工具执行失败"}
				}
				return err
			}
			return nil
		}}
	runCmd.Flags().StringVar(&workdir, "workdir", "", "编排工具的工作目录，缺省为当前项目根")
	root.AddCommand(runCmd)

	var mainRef, archiveRoot, archiveName string
	var yes, dryRun bool
	archiveCmd := &cobra.Command{Use: "archive", Short: "归档当前项目并复位到 main",
		Long: `把本片的单片文件连同 SHA-256 清单移出仓库，并把工作区复位到冻结的 main 提交。

破坏性操作，且不属于成片验收的一部分。未经用户明确确认不要执行：
先跑 --dry-run 看计划，把结果原样汇报给用户，由用户决定是否继续。

会把相对 main 的改动分成三类，只有第一类会被归档：
  候选文件   main 提交里不存在的新增文件，以及全部未跟踪文件 → 移入归档目录
  共享修改   main 提交里已存在却被改动的文件 → 视为阻断
  ignored   被 .gitignore 忽略但存在于工作区的文件 → 视为阻断

存在共享修改或 ignored 文件时拒绝执行并报告。不要为了让它通过而删文件、移文件、
改 .gitignore 或重置工作区——那会掩盖问题。也不要删除项目分支或改写 Git 历史。

--archive-name 缺省为 project-<年月日-时分秒>。成功后工作区 detach 到 --main-ref
指向的提交。除非传 --yes，否则会在 stdin 上交互确认；非交互环境下没有 --yes 会取消。`,
		Example: `  # 先看计划，不做任何改动
  am archive --dry-run

  # 用户确认后执行
  am archive

  # 非交互环境必须显式跳过确认
  am archive --yes --archive-root ~/video-archive --archive-name my-video-2026-08`,
		Args: noArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if archiveName == "" {
				archiveName = "project-" + time.Now().Format("20060102-150405")
			}
			plan, err := archive.BuildPlan(".", mainRef, archiveRoot, archiveName)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "归档目标：%s\n候选文件：%d\n共享修改：%d\nignored：%d\n", plan.Destination, len(plan.Candidates), len(plan.Shared), len(plan.Ignored))
			if dryRun {
				return nil
			}
			if !yes {
				fmt.Fprint(stdout, "确认归档并复位到 main？[y/N] ")
				answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.ToLower(strings.TrimSpace(answer)) != "y" {
					return fmt.Errorf("用户取消归档")
				}
			}
			return archive.Execute(plan)
		}}
	archiveCmd.Flags().StringVar(&mainRef, "main-ref", "main", "冻结的 main ref，归档后工作区 detach 到它指向的提交")
	archiveCmd.Flags().StringVar(&archiveRoot, "archive-root", "", "归档父目录，须在仓库之外")
	archiveCmd.Flags().StringVar(&archiveName, "archive-name", "", "归档目录名，缺省为 project-<年月日-时分秒>")
	archiveCmd.Flags().BoolVar(&yes, "yes", false, "跳过交互确认；非交互环境执行归档时必须传")
	archiveCmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印计划，不移动任何文件、不改动工作区")
	root.AddCommand(archiveCmd)

	validateCmd := &cobra.Command{Use: "validate", Short: "校验发布配置与风格规范",
		Long: `只读校验，不修改任何项目文件（唯一例外是 style --regenerate-examples）。
校验通过时打印一行结论并退出 0，失败时把具体不符项写到 stderr 并退出 1。`,
	}
	var projectRoot string
	var regenerateExamples bool
	publishCmd := &cobra.Command{Use: "publish PATH", Args: exactArgs(1, "PATH"), Short: "校验 publish.md",
		Long: `校验 PATH 指向的发布配置。

publish.md 必须以安全 YAML frontmatter 提供机器字段，并以固定 Markdown 章节
提供人工可复制内容。校验会核对字段完整性，以及它引用的产物是否与项目实际一致，
因此需要 --project-root 定位项目（缺省为当前目录）。

通过时打印 publish 校验通过：<路径>（<状态>）。`,
		Example: `  am validate publish publish.md --project-root .`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rootDir := projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			data, err := validate.Publish(args[0], rootDir)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "publish 校验通过：%s（%v）\n", args[0], data["publish_status"])
			return nil
		}}
	publishCmd.Flags().StringVar(&projectRoot, "project-root", "", "项目根目录，缺省为当前目录")
	validateCmd.AddCommand(publishCmd)
	styleCmd := &cobra.Command{Use: "style", Args: noArgs, Short: "校验风格规范",
		Long: `校验项目的视觉规范：画布规格、配色、字体层级、安全区，以及字体文件是否齐备。

字体检查是重点：渲染机是一台干净的无头 Chrome，不装任何系统字体。字体栈里没有
@font-face 的字体族会静默回退——本地看着正常、成片排版却是错的。HyperFrames 会
自动内联一批字体，但其中没有任何简体中文字体，所以中文字形必须由项目自带文件提供。

--regenerate-examples 会重新渲染 assets/style-guide/examples/ 下的示例 PNG，
这是本命令唯一会写文件的路径，出图在校验之前执行。内置风格以内置 SVG 为模板并
按项目配色重着色；自定义风格（am init --style custom）直接渲染项目里的四张
SVG，草稿阶段也可以出图。它需要两个外部命令，缺任意一个都会失败：
  rsvg-convert   来自 librsvg，负责 SVG 转 PNG
  magick         来自 ImageMagick，负责把多张 PNG 拼成概览图

SVG 转 PNG 这一步不能用 ImageMagick 代替：多数 ImageMagick 构建自带内置 XML SVG
渲染器，会抢在 rsvg 委托之前接管 .svg，产出黑底无字的图却返回退出码 0。`,
		Example: `  am validate style --project-root .

  # 重新生成示例 PNG，需要 rsvg-convert 与 magick 都可用
  am validate style --project-root . --regenerate-examples`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rootDir := projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			// 先出图再校验：自定义风格的示例 PNG 要靠这一步生成，先校验的话
			// 会因为 PNG 还不存在而永远走不到出图。
			if regenerateExamples {
				if err := validate.RegenerateExamples(rootDir, stdout); err != nil {
					return err
				}
			}
			if err := validate.Style(rootDir); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "风格规范校验通过")
			return nil
		}}
	styleCmd.Flags().StringVar(&projectRoot, "project-root", "", "项目根目录，缺省为当前目录")
	styleCmd.Flags().BoolVar(&regenerateExamples, "regenerate-examples", false, "先重新生成风格示例 PNG 再校验，需要 rsvg-convert（librsvg）与 magick（ImageMagick）两个命令")
	validateCmd.AddCommand(styleCmd)
	validateCmd.AddCommand(newValidateVideoCommand(stdout, &projectRoot))
	validateCmd.AddCommand(newValidateCastCommand(stdout))
	validateCmd.AddCommand(newValidateSongCommand(stdout))
	validateCmd.AddCommand(newValidateReadsCommand(stdout))
	root.AddCommand(validateCmd)
	root.AddCommand(newStyleCommand(stdout))
	root.AddCommand(newConcatCommand(stdout, &projectRoot))
	root.AddCommand(newReviewCommand(stdout, &projectRoot))
	root.AddCommand(newAudioCmd(stdout))
	root.AddCommand(newSongCmd(stdout))
	root.AddCommand(newCastCmd(stdout))
	root.AddCommand(newDialogueCmd(stdout))
	return root
}

func rulesDisplay(path string) string {
	if path == "" {
		return "（缺模板，未下发）"
	}
	return path
}

// exactArgs 是 cobra.ExactArgs 的中文版：默认信息是 "accepts 1 arg(s), received 0"，
// 既与全中文界面不一致，也没说清缺的是哪个参数。调用方要能只读这一行就知道补什么。
func exactArgs(count int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == count {
			return nil
		}
		return fmt.Errorf("%s 需要 %d 个位置参数 %s，实际收到 %d 个；用法：%s",
			cmd.CommandPath(), count, name, len(args), cmd.UseLine())
	}
}

// maxArgs 同上，用于可选位置参数。
func maxArgs(count int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= count {
			return nil
		}
		return fmt.Errorf("%s 最多接受 %d 个位置参数 %s，实际收到 %d 个；用法：%s",
			cmd.CommandPath(), count, name, len(args), cmd.UseLine())
	}
}

// noArgs 同上，用于不接受位置参数的命令。
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s 不接受位置参数，实际收到 %d 个：%s；用法：%s",
		cmd.CommandPath(), len(args), strings.Join(args, " "), cmd.UseLine())
}

func validateTolerance(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("--duration-tolerance 必须是有限且非负的数字")
	}
	return nil
}

func envList(values map[string]string) []string {
	return envutil.EnvList(values)
}

type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message + "（退出码 " + strconv.Itoa(e.code) + "）" }

// syncWriter 把多个 goroutine 的写入串行化。
//
// am scene run-all 并发渲染时，每个镜头的 scene.Run 都往同一个 writer 写阶段
// 消息。fmt.Fprintln 对单个字符串参数只做一次 Write，所以加锁就足以保证整行
// 不被切开；没有锁的话，写 bytes.Buffer 这类非并发安全的 writer 是数据竞争，
// 写 os.Stdout 则表现为几镜的进度输出互相交错。
type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer.Write(p)
}
