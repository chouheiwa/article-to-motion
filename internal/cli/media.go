// 本文件收拢与媒体产物相关的命令：抽帧和视频规格校验。
//
// 从 cli.go 拆出来是因为那个文件已经超过项目自己定的 800 行上限。
// 这里的命令共用一组辅助函数（时间点与画幅解析），放在一起也更好找。
package cli

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/concat"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/spf13/cobra"
)

// parseTimePoints 解析 --at 的时间点列表，支持秒数、百分比和 end。
//
// 百分比和 end 都要先知道时长，所以 duration 必须是实测值而不是 scene.json 的
// 声明值：产物时长和声明时长不一致正是要靠抽帧去看的情况之一。
//
// end 取 duration - 一帧，而不是 duration：后者已经超出最后一帧，
// ffmpeg 会返回成功但什么都不输出。
func parseTimePoints(spec string, duration float64, fps int) ([]float64, error) {
	if fps <= 0 {
		fps = 30
	}
	lastFrame := duration - 1.0/float64(fps)
	if lastFrame < 0 {
		lastFrame = 0
	}
	var points []float64
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		var value float64
		switch {
		case strings.EqualFold(item, "end"):
			value = lastFrame
		case strings.HasSuffix(item, "%"):
			percent, err := strconv.ParseFloat(strings.TrimSuffix(item, "%"), 64)
			if err != nil || percent < 0 || percent > 100 {
				return nil, fmt.Errorf("--at 的百分比必须是 0..100，实际收到 %q", item)
			}
			value = duration * percent / 100
			if value > lastFrame {
				value = lastFrame
			}
		default:
			parsed, err := strconv.ParseFloat(item, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
				return nil, fmt.Errorf("--at 的时间点必须是非负秒数、百分比或 end，实际收到 %q", item)
			}
			if parsed > lastFrame {
				return nil, fmt.Errorf("--at 的时间点 %s 秒超出视频时长 %.3f 秒", item, duration)
			}
			value = parsed
		}
		points = append(points, value)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("--at 至少要给一个时间点")
	}
	return points, nil
}

// parseCanvas 解析 "1080x1440" 形式的画幅覆盖值。
func parseCanvas(value string) (int, int, error) {
	width, height, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), "x")
	if !ok {
		return 0, 0, fmt.Errorf("--canvas 必须写成 宽x高，例如 1080x1440，实际收到 %q", value)
	}
	w, errW := strconv.Atoi(strings.TrimSpace(width))
	h, errH := strconv.Atoi(strings.TrimSpace(height))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("--canvas 的宽高必须是正整数，实际收到 %q", value)
	}
	return w, h, nil
}

// maxStripFrames 是一张逐帧条带的帧数上限：30fps 下 3 秒。条带是用来看一个
// 动作或一次转场的，更长的区间应拆成几段，否则缩略图小到看不出差别。
const maxStripFrames = 90

// parseRegion 解析 --crop 的 x,y,w,h，并要求区域落在画面内。
func parseRegion(value string, canvas scene.Canvas) (*mediaprobe.Region, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("--crop 必须写成 x,y,宽,高（像素），实际收到 %q", value)
	}
	numbers := make([]int, 4)
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("--crop 的每一项都必须是非负整数，实际收到 %q", value)
		}
		numbers[i] = n
	}
	r := &mediaprobe.Region{X: numbers[0], Y: numbers[1], W: numbers[2], H: numbers[3]}
	if r.W < 2 || r.H < 2 || r.X+r.W > canvas.WidthPx || r.Y+r.H > canvas.HeightPx {
		return nil, fmt.Errorf("--crop %s 超出 %dx%d 画面或宽高小于 2", value, canvas.WidthPx, canvas.HeightPx)
	}
	return r, nil
}

// parseStrip 把 --strip a:b 换算成区间内的全部帧时间点。
func parseStrip(spec string, duration float64, fps int) ([]float64, error) {
	from, to, ok := strings.Cut(spec, ":")
	if !ok {
		return nil, fmt.Errorf("--strip 必须写成 开始:结束（秒），实际收到 %q", spec)
	}
	bounds, err := parseTimePoints(from+","+to, duration, fps)
	if err != nil {
		return nil, fmt.Errorf("--strip：%w", err)
	}
	first, last := int(math.Round(bounds[0]*float64(fps))), int(math.Round(bounds[1]*float64(fps)))
	if last < first {
		return nil, fmt.Errorf("--strip 的结束早于开始：%q", spec)
	}
	if count := last - first + 1; count > maxStripFrames {
		return nil, fmt.Errorf("--strip 覆盖 %d 帧，超过上限 %d 帧；请拆成几段", count, maxStripFrames)
	}
	points := make([]float64, 0, last-first+1)
	for frame := first; frame <= last; frame++ {
		points = append(points, float64(frame)/float64(fps))
	}
	return points, nil
}

func newSceneFramesCommand(stdout io.Writer) *cobra.Command {
	var at, outDir, reportPath, strip, crop string
	var checkBlank bool
	var columns, thumbWidth int

	cmd := &cobra.Command{
		Use: "frames DIRECTORY", Args: exactArgs(1, "DIRECTORY"), Short: "从镜头产物抽帧供视觉检查",
		Long: `把 DIRECTORY 这一镜的产物按若干时间点抽成 PNG，用于视觉验收。

这个命令不判断画面对不对——那只有能看图的一方说得清。它的职责是把「渲染完的
mp4」变成「可以逐张打开看的 PNG」，并把机器能判的情况标出来。

渲染成功不等于画面正确：文字压在图形上看不清、中文字体静默回退成方框、
动画在第 30 帧就停住、转场只做了一半——这些今天全都能拿到退出码 0。
抽帧是把这类问题从「成片时才发现」提前到「单镜头渲完就发现」的唯一手段。

--at 接受三种写法，可混用：
  秒数     0、1.5、2.833
  百分比   25%、50%——按实测时长换算，不是 scene.json 的声明时长
  end      最后一帧。取时长减一帧，直接用时长会超出末帧，
           ffmpeg 会返回成功却什么都不输出

--check-blank 对每一帧做纯色判定，命中即退出 1。判定阈值刻意保守，只拦几乎
完全均匀的画面：深色背景配浅色标题是常见设计，误判它比漏判一个真空白帧更糟。
偏暗但有内容的帧只进报告的 hints，不影响退出码。

--strip 开始:结束 把区间内的每一帧拼成一张条带图（strip-<开始>-<结束>.png），
用来看动作与转场：有没有预备和跟随、是否匀速、几个元素是否同一帧齐动、
入场是否一帧蹦出。单张最多 90 帧，与 --at 互斥。

--crop x,y,宽,高 只保留画面上这块区域的原始像素，用来在全分辨率下看小字
是否清楚、线条是否对齐；对 --at 的 PNG 与 --strip 的条带都生效。

产物默认写到 <DIRECTORY>/visual-qc/，与 PROMPT 第七阶段的 production/visual-qc/
是同一用途。逐帧统计量写在 --report-json 里，判断结果请读它。`,
		Example: `  # 默认在首、四分位和末帧各抽一张
  am scene frames scenes/scene-001

  # 只看第 0 帧封面，并拦下空白帧
  am scene frames scenes/scene-001 --at 0 --check-blank

  # 逐帧看 1.2 到 1.6 秒的入场动作
  am scene frames scenes/scene-001 --strip 1.2:1.6

  # 全分辨率看底部说明文字
  am scene frames scenes/scene-001 --at 50% --crop 88,1180,904,160

  # 检查转场区间，写到项目统一的视觉验收目录
  am scene frames scenes/scene-001 --at 0,0.5,1.2,end \
    --out production/visual-qc/scene-001 \
    --report-json production/visual-qc/scene-001.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := scene.Load(args[0])
			if err != nil {
				return err
			}
			canvas, err := scene.CanvasOf(s.Directory, s.StyleGuide)
			if err != nil {
				return err
			}
			toolchain, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			// 时间点按实测时长换算：产物时长与声明时长不一致，
			// 正是要靠抽帧去看的情况之一。
			media, err := toolchain.Probe(s.OutputPath())
			if err != nil {
				return err
			}
			var region *mediaprobe.Region
			if crop != "" {
				if region, err = parseRegion(crop, canvas); err != nil {
					return err
				}
			}
			destination := outDir
			if destination == "" {
				destination = filepath.Join(s.Directory, "visual-qc")
			}
			if strip != "" {
				if cmd.Flags().Changed("at") || checkBlank {
					return fmt.Errorf("--strip 与 --at、--check-blank 互斥")
				}
				points, err := parseStrip(strip, media.DurationSeconds, canvas.FPS)
				if err != nil {
					return err
				}
				name := "strip-" + strings.ReplaceAll(strings.ReplaceAll(strip, ":", "-"), ".", "_") + ".png"
				image := filepath.Join(destination, name)
				if err := toolchain.ContactSheetRegion(s.OutputPath(), points, region, columns, thumbWidth, image); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "逐帧条带：%s（%d 帧，%d 列，从左到右、从上到下）\n", image, len(points), columns)
				return nil
			}
			points, err := parseTimePoints(at, media.DurationSeconds, canvas.FPS)
			if err != nil {
				return err
			}
			report, err := toolchain.ExtractFramesRegion(s.OutputPath(), points, region, destination)
			if err != nil {
				return err
			}
			if reportPath != "" {
				if err := report.WriteJSON(reportPath); err != nil {
					return err
				}
			}
			for _, frame := range report.Frames {
				fmt.Fprintf(stdout, "%.3f 秒  %s  灰度均值 %.1f  标准差 %.2f\n",
					frame.AtSeconds, frame.Image, frame.Stats.Mean, frame.Stats.StdDev)
			}
			for _, hint := range report.Hints {
				fmt.Fprintf(stdout, "提示：%s\n", hint)
			}
			if checkBlank && !report.OK {
				return fmt.Errorf("抽帧发现空白帧：%s\n  - %s",
					s.Output, strings.Join(report.Problems, "\n  - "))
			}
			return nil
		}}
	cmd.Flags().StringVar(&at, "at", "0,25%,50%,75%,end", "抽帧时间点，逗号分隔；支持秒数、百分比与 end")
	cmd.Flags().StringVar(&outDir, "out", "", "PNG 输出目录，缺省为 <DIRECTORY>/visual-qc")
	cmd.Flags().BoolVar(&checkBlank, "check-blank", false, "发现空白帧时退出码 1；不传则只抽帧和记录，不改变退出码")
	cmd.Flags().StringVar(&reportPath, "report-json", "", "把逐帧统计写成 JSON 报告到该路径")
	cmd.Flags().StringVar(&strip, "strip", "", "开始:结束（秒，可用百分比与 end），把区间内每一帧拼成一张条带图")
	cmd.Flags().StringVar(&crop, "crop", "", "x,y,宽,高（像素），只保留该区域的原始像素")
	cmd.Flags().IntVar(&columns, "cols", 6, "条带每行的帧数")
	cmd.Flags().IntVar(&thumbWidth, "thumb-width", 320, "条带里每帧的宽度（像素）")
	return cmd
}

func newValidateVideoCommand(stdout io.Writer, projectRoot *string) *cobra.Command {
	var canvasOverride string
	var fps, expectFrames, sampleRate, channels int
	var codec, pixelFormat, reportPath string
	var duration, tolerance float64
	var silent, withAudio, decode, checkFrameZero bool

	cmd := &cobra.Command{
		Use: "video PATH", Args: exactArgs(1, "PATH"), Short: "校验视频产物规格",
		Long: `按机器可判定的规格校验 PATH 指向的视频，用于静音母版和成片验收。

检查项与不给的默认值：
  分辨率        取项目 frame.md 的 canvas；--canvas 1080x1920 可覆盖
  帧率          同上取 canvas.fps；--fps 可覆盖
  视频编码      默认 h264，--codec 可覆盖，--codec "" 关闭该项检查
  像素格式      默认 yuv420p，--pixel-format 同上
  时长/帧数/音轨 默认不检查，由对应参数开启

不符项会一次全部列出，不是报第一条就停——逐条修再重跑一轮代价太高。

三个开关代价明显更高，默认关闭：
  --expect-frames  精确统计帧数，完整解码一遍。容器声明的 nb_frames 可能缺失
                   或不准，要断言「总帧数等于冻结时间轴」时必须用它。
  --decode         完整解码一遍，发现截断和损坏。仅读流头的检查发现不了：
                   moov 在文件头时，截断一半的文件照样能读出正确规格。
  --check-frame-zero  检查第 0 帧是否空白帧，对应 frame.md 的
                   black_or_blank_frame_zero 禁令。抖音类平台拿第 0 帧做封面。

退出码 0 表示全部通过，1 表示有不符项或检查跑不起来，127 表示缺 ffprobe/ffmpeg。
「检查跑不起来」（文件读不了、工具缺失）和「检查跑了但不合格」在 --report-json
里是能区分的：前者不产出报告文件，后者产出 ok=false 的完整报告。

判断结果请读 --report-json 写出的文件，不要解析 stdout 的中文输出。`,
		Example: `  # 静音母版：画幅取自 frame.md，断言无音轨且能完整解码
  am validate video production/silent-master.mp4 --silent --decode

  # 成片：断言总帧数、48kHz 双声道，并检查第 0 帧封面
  am validate video final.mp4 --expect-frames 8340 --audio \
    --check-frame-zero --report-json production/final-check.json

  # 脱离项目使用，显式给画幅
  am validate video clip.mp4 --canvas 1080x1920 --fps 30`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if silent && withAudio {
				return fmt.Errorf("--silent 与 --audio 互斥：一个断言没有音轨，一个断言有")
			}
			rootDir := *projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			spec := mediaprobe.Spec{
				Codec:           codec,
				PixelFormat:     pixelFormat,
				DurationSeconds: duration,
				Tolerance:       tolerance,
				Frames:          expectFrames,
			}
			if canvasOverride != "" {
				width, height, err := parseCanvas(canvasOverride)
				if err != nil {
					return err
				}
				spec.WidthPx, spec.HeightPx = width, height
				spec.FPS = fps
			} else {
				canvas, err := scene.CanvasOf(rootDir, "frame.md")
				if err != nil {
					return fmt.Errorf("无法从 %s 的 frame.md 确定画幅：%w；"+
						"脱离项目使用时请显式传 --canvas 宽x高 与 --fps", rootDir, err)
				}
				spec.WidthPx, spec.HeightPx, spec.FPS = canvas.WidthPx, canvas.HeightPx, canvas.FPS
				if fps > 0 {
					spec.FPS = fps
				}
			}
			switch {
			case silent:
				spec.Audio = mediaprobe.AudioAbsent
			case withAudio:
				spec.Audio = mediaprobe.AudioPresent
				spec.SampleRate, spec.Channels = sampleRate, channels
			}
			toolchain, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			report, err := toolchain.Verify(args[0], mediaprobe.VerifyOptions{
				Spec:           spec,
				ExactFrames:    expectFrames > 0,
				Decode:         decode,
				CheckFrameZero: checkFrameZero,
			})
			if err != nil {
				return err
			}
			if reportPath != "" {
				if err := report.WriteJSON(reportPath); err != nil {
					return err
				}
			}
			for _, hint := range report.Hints {
				fmt.Fprintf(stdout, "提示：%s\n", hint)
			}
			if !report.OK {
				return fmt.Errorf("视频规格校验未通过：%s\n  - %s",
					args[0], strings.Join(report.Problems, "\n  - "))
			}
			fmt.Fprintf(stdout, "视频规格校验通过：%s\n", args[0])
			return nil
		}}
	cmd.Flags().StringVar(projectRoot, "project-root", "", "项目根目录，缺省为当前目录；用于从 frame.md 反查画幅")
	cmd.Flags().StringVar(&canvasOverride, "canvas", "", "画幅覆盖值，写成 宽x高（如 1080x1920）；不传则读项目 frame.md")
	cmd.Flags().IntVar(&fps, "fps", 0, "帧率覆盖值；不传则读 frame.md 的 canvas.fps")
	cmd.Flags().StringVar(&codec, "codec", "h264", `期望的视频编码；传空串关闭该项检查`)
	cmd.Flags().StringVar(&pixelFormat, "pixel-format", "yuv420p", `期望的像素格式；传空串关闭该项检查`)
	cmd.Flags().Float64Var(&duration, "duration", 0, "期望时长（秒），0 表示不检查")
	cmd.Flags().Float64Var(&tolerance, "duration-tolerance", 0.15, "时长允许偏差（秒），有限非负数")
	cmd.Flags().IntVar(&expectFrames, "expect-frames", 0, "期望总帧数，0 表示不检查；开启后会完整解码一遍精确统计")
	cmd.Flags().BoolVar(&silent, "silent", false, "断言没有音轨，用于镜头产物和静音母版")
	cmd.Flags().BoolVar(&withAudio, "audio", false, "断言有音轨，用于混音后的成片；与 --silent 互斥")
	cmd.Flags().IntVar(&sampleRate, "sample-rate", 48000, "配合 --audio 断言采样率，0 表示不检查")
	cmd.Flags().IntVar(&channels, "channels", 2, "配合 --audio 断言声道数，0 表示不检查")
	cmd.Flags().BoolVar(&decode, "decode", false, "完整解码一遍确认没有截断或损坏；代价与文件长度成正比")
	cmd.Flags().BoolVar(&checkFrameZero, "check-frame-zero", false, "检查第 0 帧是否空白帧，交付封面前必查")
	cmd.Flags().StringVar(&reportPath, "report-json", "", "把校验结果写成 JSON 报告到该路径；判断结果应读它而不是解析 stdout")
	return cmd
}

func newConcatCommand(stdout io.Writer, projectRoot *string) *cobra.Command {
	var outPath, canvasOverride, normalizedDir string
	var fps int
	var dryRun bool

	cmd := &cobra.Command{
		Use: "concat ROOT", Args: exactArgs(1, "ROOT"), Short: "把镜头产物按顺序拼成静音母版",
		Long: `按镜头目录名的字典序，把 ROOT 下所有镜头的产物拼成一条静音母版。

规格一致时全程 -c copy，不重新编码。只有编码或像素格式与目标不同的片段会先
生成规范化副本（原始文件保留），再一起拼接。

刻意不提供重定时能力。分辨率或帧率与母版不符时直接失败并要求回镜头工程重渲染，
而不是缩放或改帧率：把 720p 拉伸到 1080 是画质损失而不是格式统一，改帧率会动到
时间轴。PROMPT 第八阶段写的是同一条规则——「禁止通过 setpts 或改变帧率让镜头
追赶配音；源时间轴有误时必须回到镜头工程重渲染」。提示词里的禁令可以被忽略，
不存在的能力不能，所以这里根本没有对应的参数。

带音轨的镜头产物同样直接失败：静音母版按契约就该无音轨，混音在后面的阶段做。

目标画幅默认从项目 frame.md 反查，--canvas 可脱离项目使用。
--dry-run 只打印分析结果，不写任何文件，用于拼接前确认。

拼完会自证：断言母版时长等于各片段时长之和、无音轨、画幅与帧率符合目标。`,
		Example: `  # 拼成静音母版
  am concat scenes/ --out production/silent-master.mp4

  # 先看看有没有规格不一致的镜头，不写文件
  am concat scenes/ --out production/silent-master.mp4 --dry-run

  # 脱离项目使用，显式给画幅
  am concat scenes/ --out master.mp4 --canvas 1080x1920 --fps 30`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outPath == "" {
				return fmt.Errorf("必须用 --out 指定母版输出路径")
			}
			rootDir := *projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			target := concat.Target{Codec: concat.DefaultCodec, PixelFormat: concat.DefaultPixelFormat}
			if canvasOverride != "" {
				width, height, err := parseCanvas(canvasOverride)
				if err != nil {
					return err
				}
				target.WidthPx, target.HeightPx, target.FPS = width, height, fps
			} else {
				canvas, err := scene.CanvasOf(rootDir, "frame.md")
				if err != nil {
					return fmt.Errorf("无法从 %s 的 frame.md 确定画幅：%w；"+
						"脱离项目使用时请显式传 --canvas 宽x高 与 --fps", rootDir, err)
				}
				target.WidthPx, target.HeightPx, target.FPS = canvas.WidthPx, canvas.HeightPx, canvas.FPS
				if fps > 0 {
					target.FPS = fps
				}
			}
			scenes, err := schedule.Plan(args[0])
			if err != nil {
				return err
			}
			if err := songOutputsCurrent(scenes, ""); err != nil {
				return err
			}
			inputs := make([]concat.Input, 0, len(scenes))
			for _, s := range scenes {
				inputs = append(inputs, concat.Input{ID: s.ID, Path: s.OutputPath()})
			}
			toolchain, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			plan, err := concat.Analyze(toolchain, inputs, target)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "片段 %d 个，合计 %.3f 秒；需要规范化 %d 个\n",
				len(inputs), plan.TotalSeconds, len(plan.Normalize))
			for _, item := range plan.Normalize {
				fmt.Fprintf(stdout, "  规范化：%s\n", item.ID)
			}
			if len(plan.Blocking) > 0 {
				return fmt.Errorf("拼接前必须先修复以下问题：\n  - %s",
					strings.Join(plan.Blocking, "\n  - "))
			}
			if dryRun {
				fmt.Fprintln(stdout, "dry-run：未写入任何文件")
				return nil
			}

			runner, err := concat.NewRunner(currentEnvironment(), toolchain)
			if err != nil {
				return err
			}
			needsNormalize := make(map[string]bool, len(plan.Normalize))
			for _, item := range plan.Normalize {
				needsNormalize[item.ID] = true
			}
			destinations := normalizedDir
			if destinations == "" {
				destinations = filepath.Join(filepath.Dir(outPath), "normalized")
			}
			paths := make([]string, 0, len(inputs))
			for _, input := range inputs {
				if !needsNormalize[input.ID] {
					paths = append(paths, input.Path)
					continue
				}
				dest := filepath.Join(destinations, input.ID+".mp4")
				if err := runner.Normalize(cmd.Context(), input.Path, dest, target); err != nil {
					return err
				}
				paths = append(paths, dest)
			}
			if err := runner.Concat(cmd.Context(), paths, outPath); err != nil {
				return err
			}
			// 自证：拼完的母版必须对得上各片段时长之和，且仍然无音轨。
			report, err := toolchain.Verify(outPath, mediaprobe.VerifyOptions{Spec: mediaprobe.Spec{
				WidthPx:         target.WidthPx,
				HeightPx:        target.HeightPx,
				FPS:             target.FPS,
				DurationSeconds: plan.TotalSeconds,
				Tolerance:       0.1,
				Audio:           mediaprobe.AudioAbsent,
			}})
			if err != nil {
				return err
			}
			if !report.OK {
				return fmt.Errorf("母版已写出但不符合预期，不要直接用于后续阶段：%s\n  - %s",
					outPath, strings.Join(report.Problems, "\n  - "))
			}
			fmt.Fprintf(stdout, "静音母版已生成：%s（%.3f 秒）\n", outPath, report.Media.DurationSeconds)
			return nil
		}}
	cmd.Flags().StringVar(&outPath, "out", "", "母版输出路径，必填")
	cmd.Flags().StringVar(projectRoot, "project-root", "", "项目根目录，缺省为当前目录；用于从 frame.md 反查画幅")
	cmd.Flags().StringVar(&canvasOverride, "canvas", "", "画幅覆盖值，写成 宽x高；不传则读项目 frame.md")
	cmd.Flags().IntVar(&fps, "fps", 0, "帧率覆盖值；不传则读 frame.md 的 canvas.fps")
	cmd.Flags().StringVar(&normalizedDir, "normalized-dir", "", "规范化副本目录，缺省为 <--out 所在目录>/normalized")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只分析并打印结果，不写任何文件")
	return cmd
}
