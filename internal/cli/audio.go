// 本文件收拢配音相关的命令。
package cli

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
	"github.com/chouheiwa/articale-to-motion/internal/voicecheck"
	"github.com/spf13/cobra"
)

func newAudioCmd(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "audio", Short: "配音相关检查", Long: `配音相关的机器检查。

  check   把字幕时间轴与配音里实际发声的区间逐条比对，拦下整体偏移、
          落在静音里的字幕与没有字幕覆盖的发声

配音本身由编排工具按 TTS_PROVIDER 生成，强制对齐按 ALIGN_PROVIDER 执行；
这里只做与工具无关、结果确定的校验。`}
	cmd.AddCommand(newAudioCheckCommand(stdout))
	return cmd
}

func newAudioCheckCommand(stdout io.Writer) *cobra.Command {
	var audioPath, srtPath, reportPath string
	opts := voicecheck.Defaults

	cmd := &cobra.Command{
		Use: "check", Args: noArgs, Short: "把字幕时间轴与配音里实际发声的区间逐条比对",
		Long: `用配音的能量包络独立校验字幕时间轴。

字幕时间来自 TTS 自带的时间戳、ASR 识别或强制对齐，三者都可能整体偏移或局部
错位——实测 TTS 自带的 SRT 会比实际发声早约 0.4 秒。字幕、镜头切点和画面节拍
全挂在这条时间轴上，它错了，下游没有任何环节会报错。

这条命令不识别文字，只判定「这一刻有没有声音」：电平低于全片最响处
--below-peak-db 分贝即视为静音，短于 --min-gap 的静音并入发声。它与字幕来源
完全无关，所以能交叉校验任何一种来源。

判为问题（退出码 1）：
  - 字幕开始早于其中首个发声超过 --tolerance 秒
  - 字幕开始晚于发声超过 --tolerance 秒，且那段发声不在前一条字幕里
  - 字幕整段落在静音里
  - 有声区间持续 --min-uncovered 秒以上没有任何字幕覆盖

只进 hints（不影响退出码）：
  - 字幕在声音结束后继续停留超过 --tolerance 秒。终稿字幕按可读语义单元延长
    停留是允许的；但对 TTS 或对齐直接产出的时间戳，这说明它偏晚
  - 全部字幕起点偏差的中位数超过容差的一半：多条同向偏移说明时间戳来源有系统
    误差，应整体校正而不是逐条修

逐条的发声区间与偏差写在 --report-json 里，判断结果请读它。`,
		Example: `  # 校验 TTS 产出的句级时间戳
  am audio check --audio production/audio/voice.wav --srt production/audio/voice.srt

  # 校验终稿字幕并留存报告
  am audio check --audio production/audio/voice.wav --srt transcription-production.srt \
    --report-json production/audio/voice-check.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if audioPath == "" || srtPath == "" {
				return fmt.Errorf("必须同时给出 --audio 与 --srt")
			}
			for name, value := range map[string]float64{"--tolerance": opts.Tolerance, "--below-peak-db": opts.BelowPeakDB, "--min-gap": opts.MinGap, "--min-uncovered": opts.MinUncovered} {
				if !(value > 0) || math.IsInf(value, 0) {
					return fmt.Errorf("%s 必须是有限正数，收到 %v", name, value)
				}
			}
			cues, err := srt.ReadCues(srtPath)
			if err != nil {
				return err
			}
			toolchain, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			levels, err := toolchain.Loudness(audioPath, opts.Window)
			if err != nil {
				return err
			}
			speech, threshold := voicecheck.Speech(levels, opts)
			report := voicecheck.Check(cues, speech, threshold, opts)
			if reportPath != "" {
				if err := report.WriteJSON(reportPath); err != nil {
					return err
				}
			}
			fmt.Fprintf(stdout, "字幕 %d 条，检测到发声 %d 段（静音阈值 %.1f dBFS）\n", len(cues), len(speech), report.ThresholdDB)
			if report.MedianStartDelta != nil {
				fmt.Fprintf(stdout, "字幕起点相对发声的中位偏差：%+.3f 秒\n", *report.MedianStartDelta)
			}
			for _, hint := range report.Hints {
				fmt.Fprintf(stdout, "提示：%s\n", hint)
			}
			if !report.OK {
				return fmt.Errorf("字幕时间轴与配音不符：%s\n  - %s", srtPath, strings.Join(report.Problems, "\n  - "))
			}
			fmt.Fprintln(stdout, "字幕时间轴与配音一致")
			return nil
		}}
	cmd.Flags().StringVar(&audioPath, "audio", "", "配音文件，必填")
	cmd.Flags().StringVar(&srtPath, "srt", "", "待校验的字幕文件，必填")
	cmd.Flags().StringVar(&reportPath, "report-json", "", "把逐条比对结果写成 JSON 报告到该路径")
	cmd.Flags().Float64Var(&opts.Tolerance, "tolerance", voicecheck.Defaults.Tolerance, "字幕边界与发声边界允许的偏差（秒）")
	cmd.Flags().Float64Var(&opts.BelowPeakDB, "below-peak-db", voicecheck.Defaults.BelowPeakDB, "低于全片最响处多少分贝视为静音")
	cmd.Flags().Float64Var(&opts.MinGap, "min-gap", voicecheck.Defaults.MinGap, "视为停顿的最短静音（秒），更短的并入发声")
	cmd.Flags().Float64Var(&opts.MinUncovered, "min-uncovered", voicecheck.Defaults.MinUncovered, "报告无字幕覆盖的最短发声（秒）")
	return cmd
}
