package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/reads"
	"github.com/spf13/cobra"
)

func newValidateReadsCommand(stdout io.Writer) *cobra.Command {
	var inventoryPath, phrasesPath, reportPath, root string
	opts := reads.Defaults

	cmd := &cobra.Command{
		Use: "reads", Args: noArgs, Short: "检查每个可读元素是否给了观众足够的阅读时间",
		Long: `读取 visible-event-inventory.json，逐个检查画面上的可读元素。

作者知道画面写了什么，观众只看一遍、第一次看。元素一个接一个飞快出现、
没读完就切走，是生成式动画最常见的失败，而渲染、产物校验都发现不了它。

判为问题（退出码 1）：
  - 可读区间（readable_start_frame 到 readable_end_frame_exclusive）短于
    max(--min-seconds, 字数 / --chars-per-second)
  - 帧号顺序不成立：enter ≤ readable_start < readable_end ≤ exit_end
  - event_id 缺失或重复
  - 给了短语时间轴时：元素没绑定 phrase_id、绑定的短语不存在、元素早于短语
    开始帧出现（画面剧透口播）、元素的可读顺序与口播短语顺序相反

只进 hints：可读起点相距不到 --crowd-seconds 的多个元素。观众一次只能读一个，
但卡片组、标题加副标题一起出现也是合法版式，机器分不清。

字数按阅读单位计：一个汉字一个单位，一个英文词或一个数一个单位，标点不算。
role 为 decorative 的元素不检查阅读时间。

默认阈值取自 Netflix 简体中文字幕规范（成人节目每秒不超过 9 个字，每条至少
5/6 秒）。字幕位置固定、观众已经盯着；画面元素还要先被找到，所以这是下限。

文件结构：
  visible-event-inventory.json
    {"schema":1,"fps":30,"events":[{"event_id","scene_id","selector","text","role",
      "phrase_id","enter_frame","readable_start_frame",
      "readable_end_frame_exclusive","exit_end_frame"}]}
  phrase-timeline.json
    {"fps":30,"phrases":[{"phrase_id","semantic_order","start_frame",
      "end_frame_exclusive", ...}]}
帧号都是全片全局帧。`,
		Example: `  # 默认读 production/ 下的两个文件；短语时间轴不存在时跳过短语相关检查
  am validate reads

  am validate reads --inventory production/visible-event-inventory.json \
    --report-json production/reads-check.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			for name, value := range map[string]float64{"--chars-per-second": opts.CharsPerSecond, "--min-seconds": opts.MinSeconds, "--crowd-seconds": opts.CrowdSeconds} {
				if !(value > 0) || math.IsInf(value, 0) {
					return fmt.Errorf("%s 必须是有限正数，收到 %v", name, value)
				}
			}
			if inventoryPath == "" {
				inventoryPath = filepath.Join(root, "production", "visible-event-inventory.json")
			}
			inventory, err := reads.LoadInventory(inventoryPath)
			if err != nil {
				return err
			}
			var timeline *reads.Timeline
			explicit := phrasesPath != ""
			if !explicit {
				phrasesPath = filepath.Join(root, "production", "phrase-timeline.json")
			}
			if _, statErr := os.Stat(phrasesPath); statErr == nil || explicit {
				loaded, err := reads.LoadTimeline(phrasesPath)
				if err != nil {
					return err
				}
				timeline = &loaded
			} else {
				fmt.Fprintf(stdout, "提示：没有 %s，跳过短语绑定与顺序检查\n", phrasesPath)
			}
			report := reads.Check(inventory, timeline, opts)
			if reportPath != "" {
				if err := report.WriteJSON(reportPath); err != nil {
					return err
				}
			}
			for _, hint := range report.Hints {
				fmt.Fprintf(stdout, "提示：%s\n", hint)
			}
			if !report.OK {
				return fmt.Errorf("阅读时间检查未通过：%s\n  - %s", inventoryPath, strings.Join(report.Problems, "\n  - "))
			}
			fmt.Fprintf(stdout, "阅读时间检查通过：%d 个可读元素\n", len(report.Events))
			return nil
		}}
	cmd.Flags().StringVar(&root, "project-root", ".", "项目根目录，用于定位 production/ 下的默认文件")
	cmd.Flags().StringVar(&inventoryPath, "inventory", "", "可读元素清单，缺省为 <project-root>/production/visible-event-inventory.json")
	cmd.Flags().StringVar(&phrasesPath, "phrases", "", "短语时间轴，缺省为 <project-root>/production/phrase-timeline.json（不存在则跳过）")
	cmd.Flags().StringVar(&reportPath, "report-json", "", "把逐元素结果写成 JSON 报告到该路径")
	cmd.Flags().Float64Var(&opts.CharsPerSecond, "chars-per-second", reads.Defaults.CharsPerSecond, "阅读速度上限（单位/秒）")
	cmd.Flags().Float64Var(&opts.MinSeconds, "min-seconds", reads.Defaults.MinSeconds, "任何可读元素的最短可读时长（秒）")
	cmd.Flags().Float64Var(&opts.CrowdSeconds, "crowd-seconds", reads.Defaults.CrowdSeconds, "可读起点相距不到这么久视为同时出现（秒）")
	return cmd
}
