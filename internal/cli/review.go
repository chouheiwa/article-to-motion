// 本文件收拢整片复查与合成源码扫描两条命令。
package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/determinism"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/review"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/spf13/cobra"
)

func newReviewCommand(stdout io.Writer, projectRoot *string) *cobra.Command {
	var scenesRoot, outDir, reportPath string
	var fps, perSheet, thumbWidth int
	var syncTolerance, every float64
	var silent bool

	cmd := &cobra.Command{
		Use: "review VIDEO", Args: exactArgs(1, "VIDEO"), Short: "整片复查：音画、帧数、镜头交界与联系表",
		Long: `对混音后的成片做整片复查，产出一份机器可读报告和若干张联系表。

镜头级检查（渲染后的产物校验、am scene frames）只看单个镜头；拼接与混音之后
才出现的问题它们看不到。这条命令补的正是这一段：

  音画时长   音轨与视频流各自的时长相差超过 --sync-tolerance 即判不合格；
             --silent 时改为断言没有音轨（只拼接、未混音的交付）
  总帧数     给了 --scenes 时，精确解码统计成片帧数，与各镜头帧数之和比对；
             不一致说明漏拼、重拼或拼接掉帧
  镜头交界   给了 --scenes 时，取每个交界两侧各一帧做纯色判定。切黑、切白是
             合法转场，机器分不清有意还是事故，所以只进 hints，不改变退出码
  响度       有音轨时解码成片，量积分响度、真峰与响度范围写进报告的 loudness；
             项目里有 frame.md 时与它 audio 块的目标比对。偏离只进 hints，
             不改变退出码：平台响度规范各不相同，frame.md 的数是缺省建议
  交界条带   给了 --scenes 时，每个交界出一张 seam-NN.png：上一行是切点前 6 帧，
             下一行是切点后 6 帧，用来看转场是否顺、有没有一帧闪烁或跳变

联系表按镜头排版：每行一个镜头，依次是首帧、中间帧、末帧；没给 --scenes 时
按 --every 秒的间隔取帧，每行 4 格。每张最多 --per-sheet 格，超出分页。
格子与镜头、帧号的对应关系写在报告的 sheets 里——图上不叠文字，因为 drawtext
依赖 ffmpeg 编译时带 freetype，各发行版不一致。

联系表不做判断。画面对不对要打开图逐页看：文字是否溢出或压在图形上、相邻
镜头的风格与密度是否一致、交界是否跳变突兀、结尾是否停在合适的画面。

退出码 0 表示机器检查全部通过，1 表示有不符项或检查跑不起来。判断结果读
--report-json（缺省为 <--out>/review.json），不要解析 stdout。`,
		Example: `  # 成片复查，镜头信息取自 scenes/
  am review production/audio/candidate.mp4 --scenes scenes/ --out production/review

  # 脱离项目使用：只查音画时长，按 3 秒间隔出联系表
  am review final.mp4 --fps 30 --every 3 --out /tmp/review`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rootDir := *projectRoot
			if rootDir == "" {
				rootDir = "."
			}
			if outDir == "" {
				return fmt.Errorf("必须用 --out 指定复查产物目录")
			}
			rate := fps
			if rate <= 0 {
				canvas, err := scene.CanvasOf(rootDir, "frame.md")
				if err != nil {
					return fmt.Errorf("无法从 %s 的 frame.md 确定帧率：%w；脱离项目使用时请显式传 --fps", rootDir, err)
				}
				rate = canvas.FPS
			}
			var segments []review.Segment
			if scenesRoot != "" {
				scenes, err := schedule.Plan(scenesRoot)
				if err != nil {
					return err
				}
				for _, s := range scenes {
					segment := review.Segment{ID: s.ID, Frames: int(math.Round(s.DurationSeconds * float64(rate)))}
					if s.Timing != nil {
						segment.Handoff, segment.CrossChapter = s.Timing.Enter.Device, s.Timing.Enter.CrossChapter
					}
					segments = append(segments, segment)
				}
			}
			loudness, err := reviewLoudnessTarget(rootDir, silent)
			if err != nil {
				return err
			}
			toolchain, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			report, err := review.Run(toolchain, review.Options{
				Video: args[0], FPS: rate, Segments: segments, OutDir: outDir, Silent: silent, Loudness: loudness,
				SyncTolerance: syncTolerance, Every: every, TilesPerSheet: perSheet, ThumbWidth: thumbWidth,
			})
			if err != nil {
				return err
			}
			target := reportPath
			if target == "" {
				target = filepath.Join(outDir, "review.json")
			}
			if err := report.WriteJSON(target); err != nil {
				return err
			}
			for _, sheet := range report.Sheets {
				fmt.Fprintf(stdout, "联系表：%s（%d 格）\n", sheet.Image, len(sheet.Tiles))
			}
			if len(report.Seams) > 0 {
				fmt.Fprintf(stdout, "交界条带：%d 张（%s 起）\n", len(report.Seams), report.Seams[0].Image)
			}
			for _, hint := range report.Hints {
				fmt.Fprintf(stdout, "提示：%s\n", hint)
			}
			fmt.Fprintf(stdout, "报告：%s\n", target)
			if !report.OK {
				return fmt.Errorf("整片复查未通过：%s\n  - %s", args[0], strings.Join(report.Problems, "\n  - "))
			}
			fmt.Fprintln(stdout, "机器检查通过；请逐页打开联系表做视觉复查")
			return nil
		}}
	cmd.Flags().StringVar(projectRoot, "project-root", "", "项目根目录，缺省为当前目录；用于从 frame.md 反查帧率")
	cmd.Flags().StringVar(&scenesRoot, "scenes", "", "镜头根目录；给了才做总帧数与交界检查，联系表按镜头排版")
	cmd.Flags().StringVar(&outDir, "out", "", "联系表与报告的输出目录，必填")
	cmd.Flags().StringVar(&reportPath, "report-json", "", "报告路径，缺省为 <--out>/review.json")
	cmd.Flags().IntVar(&fps, "fps", 0, "帧率覆盖值；不传则读 frame.md 的 canvas.fps")
	cmd.Flags().BoolVar(&silent, "silent", false, "断言成片没有音轨，用于只拼接未混音的交付；不做音画比对")
	cmd.Flags().Float64Var(&syncTolerance, "sync-tolerance", review.DefaultSyncTolerance, "音视频流时长允许的差值（秒）")
	cmd.Flags().Float64Var(&every, "every", review.DefaultEvery, "没给 --scenes 时联系表的取帧间隔（秒）")
	cmd.Flags().IntVar(&perSheet, "per-sheet", review.DefaultTilesPerSheet, "每张联系表的最大格数，超出分页")
	cmd.Flags().IntVar(&thumbWidth, "thumb-width", review.DefaultThumbWidth, "联系表缩略图宽度（像素）")
	return cmd
}

// reviewLoudnessTarget 从项目根的 frame.md 读成片响度目标。
// 没有 frame.md（脱离项目使用）时不比对；有但读不出目标时报错，
// 不静默跳过——那会让人以为响度已经查过了。
func reviewLoudnessTarget(rootDir string, silent bool) (*review.LoudnessTarget, error) {
	if silent {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(rootDir, "frame.md")); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	targets, err := scene.AudioTargetsOf(rootDir, "frame.md")
	if err != nil {
		return nil, fmt.Errorf("无法从 %s 的 frame.md 读出响度目标：%w", rootDir, err)
	}
	return &review.LoudnessTarget{IntegratedLUFS: targets.IntegratedLUFS, Tolerance: targets.Tolerance, TruePeakMax: targets.TruePeakMax}, nil
}

func newSceneLintCommand(stdout io.Writer) *cobra.Command {
	var reportPath string
	cmd := &cobra.Command{
		Use: "lint DIRECTORY", Args: exactArgs(1, "DIRECTORY"), Short: "扫描镜头合成源码的确定性问题",
		Long: `静态扫描 DIRECTORY 里渲染实际加载的合成源码，找出让渲染结果不可复现的写法。

这与渲染后的产物校验跑的是同一套规则：scene run / run-all 在渲染器退出后
会自动执行，命中即判本镜头失败且不重试。单独提供这条命令，是为了在改完
合成代码后、重新渲染前先确认一遍。

扫描范围：从 index.html（没有时取顶层 .html，仍没有时向下两层找 index.html）
出发，沿 src、href、data-composition-src、import 与 CSS url() 的本地引用展开。
没被引用的草稿文件不参与判定。第三方库（vendor/、lib/、node_modules/ 等目录下，
带 .min / .bundle / .umd 后缀，或以许可证横幅开头的 JS）只记进报告的 skipped，
不做判定：库内部的 Math.random、requestAnimationFrame 不在渲染路径上。

规则：
  clock            Date.now()、performance.now()、无参 new Date()
  random           Math.random()
  raf              requestAnimationFrame
  interval         setInterval
  infinite-repeat  repeat: -1
  network          远程 fetch、XMLHttpRequest、WebSocket、EventSource
  remote-font      在线字体（Google Fonts）
  unpinned-cdn     jsdelivr / unpkg / esm.sh / cdnjs 上没有完整版本号的库
  remote-asset     其他远程资源

注释、字符串与正则字面量里提到这些 API 不算违规。渲染路径确实不经过的交互代码可在同一行注释写
` + determinism.AllowMarker + `: 理由
豁免，豁免项仍写进报告的 allowed。`,
		Example: `  am scene lint scenes/scene-003
  am scene lint scenes/scene-003 --report-json scenes/scene-003/lint.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := determinism.Scan(args[0])
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
			for _, skipped := range report.Skipped {
				fmt.Fprintf(stdout, "按第三方库跳过：%s\n", skipped)
			}
			for _, allowed := range report.Allowed {
				fmt.Fprintf(stdout, "已豁免：%s\n", allowed)
			}
			if !report.OK {
				return fmt.Errorf("合成源码存在确定性问题：%s\n  - %s", args[0], strings.Join(report.Problems(), "\n  - "))
			}
			fmt.Fprintf(stdout, "合成源码扫描通过：%d 个文件\n", len(report.Files))
			return nil
		}}
	cmd.Flags().StringVar(&reportPath, "report-json", "", "把扫描结果写成 JSON 报告到该路径")
	return cmd
}
