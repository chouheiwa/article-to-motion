// 本文件是无配音卡点模式的命令：导入音乐、分析并确认节拍网格、按小节写镜头 cue。
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/project"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/spf13/cobra"
)

func newBeatCommand(stdout io.Writer) *cobra.Command {
	root := "."
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "beat",
		Short: "无配音卡点：导入音乐、确认节拍网格、按小节切镜头",
		Long: `卡点模式以项目根的 beat.yaml 启用，与口播、歌曲讲解、多角色对话互斥。

流程：
  am beat init                  写出 beat.yaml
  am beat doctor                检查 beat-this 环境，预先下载模型权重
  am beat import MUSIC          导入音乐（记录摘要与实测时长）
  am beat analyze               分析节拍，写出草稿网格与试听轨 grid-click.wav
  am beat regrid --meter 4 --anchor 1   重拍整体错位或拍号判错时重排重拍
  am beat confirm               试听确认后，把草稿导入为正式网格
  （按小节写 ` + beat.PlanFile + `）
  am beat cues scenes/          按小节换算整数帧，写入各镜头时长与 beat 块
  am validate beat              校验网格、计划与全部镜头

镜头时长只能用小节数表达：每个切点都是某个重拍的帧号，由同一个公式算出。
产物都在 ` + beat.Store + ` 下。`,
	}
	cmd.PersistentFlags().StringVar(&root, "project-root", ".", "项目根目录")
	cmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Minute, "分析的最长等待时间")
	withTimeout := func(parent context.Context) (context.Context, context.CancelFunc, error) {
		if timeout <= 0 {
			return nil, nil, fmt.Errorf("--timeout 必须为正")
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		return ctx, cancel, nil
	}

	cmd.AddCommand(&cobra.Command{
		Use: "init", Short: "为项目写出 beat.yaml，不覆盖已有内容", Args: cobra.NoArgs,
		Long:    "在项目根写出 " + beat.ConfigFile + "，启用卡点模式。已有内容不同的 beat.yaml 时整体失败、不覆盖。项目里有 song.yaml 或 cast.yaml 时拒绝：卡点与歌曲、多角色对话的时间轴来源互斥。不安装 Python 或模型。",
		Example: "  am beat init --project-root .",
		RunE: func(*cobra.Command, []string) error {
			if _, err := project.InitializeWithFiles(root, map[string][]byte{beat.ConfigFile: []byte(beat.DefaultConfig)}); err != nil {
				return err
			}
			if _, err := beat.LoadConfig(root); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "已启用卡点模式。按 %s 配好 Python 环境后运行 am beat doctor\n", beat.ConfigFile)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "doctor", Short: "检查 beat-this 环境并预先下载模型权重", Args: cobra.NoArgs,
		Long:    "用 beat.yaml 的 python 加载固定版本 beat-this " + beat.AnalyzerVersion + " 的模型。首次运行会联网下载权重并缓存；之后的分析不再联网。",
		Example: "  python3 -m venv ~/.venvs/beat && ~/.venvs/beat/bin/pip install beat-this==" + beat.AnalyzerVersion + "\n  am beat doctor",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel, err := withTimeout(c.Context())
			if err != nil {
				return err
			}
			defer cancel()
			return beat.Doctor(ctx, root, currentEnvironment(), stdout)
		},
	})

	var replace bool
	importCmd := &cobra.Command{
		Use: "import MUSIC", Short: "导入音乐，记录摘要与实测时长", Args: exactArgs(1, "MUSIC"),
		Long:    "把音乐复制进 " + beat.Store + "，完整解码一遍并记录 SHA-256 与时长。已导入另一首时默认拒绝：换歌会让网格与镜头全部失效，确认后加 --replace。授权来源记进素材账本，本命令不判断授权。",
		Example: "  am beat import ~/Music/track.mp3\n  am beat import new-cut.wav --replace",
		RunE: func(_ *cobra.Command, args []string) error {
			media, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			track, err := beat.Import(root, args[0], media, replace)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "已导入 %s（%.3f 秒）。下一步：am beat analyze\n", track.Source, track.Duration)
			return nil
		},
	}
	importCmd.Flags().BoolVar(&replace, "replace", false, "替换已导入的另一首音乐")
	cmd.AddCommand(importCmd)

	cmd.AddCommand(&cobra.Command{
		Use: "analyze", Short: "分析节拍，写出草稿网格与试听轨", Args: cobra.NoArgs,
		Long:    "用 beat-this 分析普通拍与重拍，写出 " + beat.DraftFile + " 与 " + beat.ClickFile + "。试听轨里每拍一声，重拍更高更响。草稿不会生效：必须听完试听轨，再用 am beat confirm 导入。",
		Example: "  am beat analyze",
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, cancel, err := withTimeout(c.Context())
			if err != nil {
				return err
			}
			defer cancel()
			media, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			grid, err := beat.Analyze(ctx, root, currentEnvironment(), media, stdout)
			if err != nil {
				return err
			}
			reportDraft(stdout, grid)
			return nil
		},
	})

	var meter, anchor int
	var from string
	regrid := &cobra.Command{
		Use: "regrid", Short: "按固定拍号重排重拍，普通拍不变", Args: cobra.NoArgs,
		Long: `模型最常见的错误是重拍整体错位（把第 3 拍当成小节开头）或拍号判错，普通拍本身通常是对的。
本命令从第 --anchor 拍（从 0 数）起每 --meter 拍标一个重拍，写回草稿并重新生成试听轨。

  试听时重拍落在第 2 拍上（4/4）   am beat regrid --meter 4 --anchor 1
  模型判成 4 拍、实际是 3 拍        am beat regrid --meter 3 --anchor 0`,
		Example: "  am beat regrid --meter 4 --anchor 1",
		RunE: func(c *cobra.Command, _ []string) error {
			draft, err := readGridFile(root, from)
			if err != nil {
				return err
			}
			grid, err := draft.Regrid(meter, anchor)
			if err != nil {
				return err
			}
			media, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			if err := beat.WriteDraftAndClick(c.Context(), root, grid, media); err != nil {
				return err
			}
			reportDraft(stdout, grid)
			return nil
		},
	}
	regrid.Flags().IntVar(&meter, "meter", 4, "每小节拍数")
	regrid.Flags().IntVar(&anchor, "anchor", 0, "第一个重拍是第几拍（从 0 数）")
	regrid.Flags().StringVar(&from, "grid", "", "要重排的网格文件，缺省为 "+beat.DraftFile)
	cmd.AddCommand(regrid)

	var clickFrom string
	click := &cobra.Command{
		Use: "click", Short: "按网格文件重新生成试听轨", Args: cobra.NoArgs,
		Long:    "手改过网格 JSON 后，用它重新生成 " + beat.ClickFile + " 再听一遍。",
		Example: "  am beat click\n  am beat click --grid my-grid.json",
		RunE: func(c *cobra.Command, _ []string) error {
			grid, err := readGridFile(root, clickFrom)
			if err != nil {
				return err
			}
			media, err := mediaprobe.New(currentEnvironment())
			if err != nil {
				return err
			}
			if err := beat.WriteClick(c.Context(), root, grid, media); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "试听轨：%s\n", filepath.Join(root, beat.ClickFile))
			return nil
		},
	}
	click.Flags().StringVar(&clickFrom, "grid", "", "网格文件，缺省为 "+beat.DraftFile)
	cmd.AddCommand(click)

	var confirmFrom string
	confirm := &cobra.Command{
		Use: "confirm", Short: "把试听确认过的网格导入为正式网格", Args: cobra.NoArgs,
		Long:    "只在听完试听轨、确认每个重拍咔嗒都落在小节开头之后执行。正式网格写入 " + beat.GridFile + "；之后再改网格，所有镜头 cue 失效，需重新 am beat cues 并重渲染。",
		Example: "  am beat confirm\n  am beat confirm --grid my-grid.json",
		RunE: func(_ *cobra.Command, _ []string) error {
			draft, err := readGridFile(root, confirmFrom)
			if err != nil {
				return err
			}
			grid, err := beat.Confirm(root, draft)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "节拍网格已确认：%s\n", grid.Summarize())
			printBars(stdout, grid)
			fmt.Fprintf(stdout, "下一步：按小节写 %s，再 am beat cues scenes/\n", beat.PlanFile)
			return nil
		},
	}
	confirm.Flags().StringVar(&confirmFrom, "grid", "", "网格文件，缺省为 "+beat.DraftFile)
	cmd.AddCommand(confirm)

	cmd.AddCommand(&cobra.Command{
		Use: "bars", Short: "列出正式网格的每个小节", Args: cobra.NoArgs,
		Long:    "写分镜计划前用它查每个小节的起止时间与拍数。第一个重拍之前的弱起并进第一个镜头，最后一小节延伸到音乐结束。",
		Example: "  am beat bars",
		RunE: func(*cobra.Command, []string) error {
			grid, err := beat.LoadGrid(root)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, grid.Summarize())
			printBars(stdout, grid)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use: "cues SCENES", Short: "按小节换算整数帧，写入各镜头时长与 beat 块", Args: exactArgs(1, "SCENES"),
		Long: `读取正式网格与 ` + beat.PlanFile + `，按镜头目录字典序与计划逐一对应，
把每个镜头的小节换算成整数帧：写入 scene.json 的 duration_seconds 与 beat 块，
以及镜头目录里的 ` + beat.CueDataFile + `（镜头局部的每一拍：帧号、小节、拍序）。

镜头 id 与顺序必须和计划完全一致；任何一处不合格都不写文件。改过网格或计划后
重跑本命令，已渲染的镜头要 --force 重渲染。`,
		Example: "  am beat cues scenes/",
		RunE: func(_ *cobra.Command, args []string) error {
			count, err := applyBeatCues(root, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "已写入 %d 个镜头的时长与节拍\n", count)
			return nil
		},
	})
	return cmd
}

func readGridFile(root, path string) (beat.Grid, error) {
	if path == "" {
		path = filepath.Join(root, beat.DraftFile)
	}
	return beat.ReadDraft(path)
}

func reportDraft(out io.Writer, grid beat.Grid) {
	fmt.Fprintf(out, "草稿网格：%s\n", grid.Summarize())
	for _, problem := range grid.Problems() {
		fmt.Fprintf(out, "问题：%s\n", problem)
	}
	fmt.Fprintf(out, "请试听 %s：每拍一声，重拍更高。重拍错位用 am beat regrid，确认无误后 am beat confirm\n", beat.ClickFile)
}

func printBars(out io.Writer, grid beat.Grid) {
	for _, bar := range grid.Bars() {
		fmt.Fprintf(out, "  第 %3d 小节  %8.3f – %8.3f 秒  %d 拍\n", bar.Number, bar.Start, bar.End, bar.Beats)
	}
}

// beatLayout 对齐分镜计划与镜头目录，返回每个镜头的帧区间。
func beatLayout(root string, scenes []scene.Scene) (beat.Grid, []beat.Segment, int, error) {
	grid, err := beat.LoadGrid(root)
	if err != nil {
		return beat.Grid{}, nil, 0, err
	}
	plan, err := beat.LoadPlan(root)
	if err != nil {
		return beat.Grid{}, nil, 0, err
	}
	canvas, err := scene.CanvasOf(root, "frame.md")
	if err != nil {
		return beat.Grid{}, nil, 0, err
	}
	segments, err := beat.Layout(grid, plan, canvas.FPS)
	if err != nil {
		return beat.Grid{}, nil, 0, err
	}
	if len(segments) != len(scenes) {
		return beat.Grid{}, nil, 0, fmt.Errorf("%s 有 %d 个镜头，镜头目录有 %d 个", beat.PlanFile, len(segments), len(scenes))
	}
	var mismatched []string
	for i, s := range scenes {
		if s.ID != segments[i].ID {
			mismatched = append(mismatched, fmt.Sprintf("第 %d 个：计划 %s，目录 %s", i+1, segments[i].ID, s.ID))
		}
		if s.Song != nil || s.Cast != nil {
			return beat.Grid{}, nil, 0, fmt.Errorf("卡点镜头 %s 不能带 song 或 cast 块", s.ID)
		}
	}
	if len(mismatched) > 0 {
		return beat.Grid{}, nil, 0, fmt.Errorf("镜头顺序与 %s 不一致（目录按字典序）：%s", beat.PlanFile, strings.Join(mismatched, "；"))
	}
	return grid, segments, canvas.FPS, nil
}

func applyBeatCues(root, scenesRoot string) (int, error) {
	scenes, err := schedule.Plan(scenesRoot)
	if err != nil {
		return 0, err
	}
	grid, segments, fps, err := beatLayout(root, scenes)
	if err != nil {
		return 0, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	gridSHA, err := beat.GridSHA(root)
	if err != nil {
		return 0, err
	}
	var writes []pendingWrite
	for i, s := range scenes {
		// 镜头目录向上找到的项目根必须就是本次的 --project-root，否则写出的
		// 引用在渲染时会指向另一个项目的网格。
		if found, err := beat.FindRoot(s.Directory); err != nil || found != absRoot {
			return 0, fmt.Errorf("镜头 %s 不在项目 %s 之内（向上找到的 %s 根是 %q）", s.ID, absRoot, beat.ConfigFile, found)
		}
		ref, err := beat.GridRef(s.Directory, absRoot)
		if err != nil {
			return 0, err
		}
		segment := segments[i]
		data, dataSHA, err := beat.EncodeLocal(grid.Slice(segment, fps))
		if err != nil {
			return 0, err
		}
		cue := beat.Cue{
			Scene: s.ID, FirstBar: segment.FirstBar, Bars: segment.Bars,
			FPS: fps, Grid: ref, SHA: gridSHA, StartFrame: segment.StartFrame, Frames: segment.Frames,
			Data: beat.CueDataFile, DataSHA: dataSHA,
		}
		sceneFile := filepath.Join(s.Directory, "scene.json")
		raw, err := os.ReadFile(sceneFile)
		if err != nil {
			return 0, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return 0, fmt.Errorf("%s 不是合法 JSON：%w", sceneFile, err)
		}
		if fields["beat"], err = json.Marshal(cue); err != nil {
			return 0, err
		}
		if fields["duration_seconds"], err = json.Marshal(float64(segment.Frames) / float64(fps)); err != nil {
			return 0, err
		}
		body, err := json.MarshalIndent(fields, "", "  ")
		if err != nil {
			return 0, err
		}
		writes = append(writes, pendingWrite{filepath.Join(s.Directory, beat.CueDataFile), data}, pendingWrite{sceneFile, append(body, '\n')})
	}
	if err := commitWrites(writes); err != nil {
		return 0, err
	}
	return len(scenes), nil
}

type pendingWrite struct {
	path string
	body []byte
}

// commitWrites 分两轮落盘：先把每份新内容写成同目录的临时文件，全部成功后
// 再逐个改名替换。磁盘满、权限不足这类错误都发生在第一轮，此时一个目标文件
// 都还没动；第二轮只剩同文件系统内的 rename。rename 本身仍可能中途失败，
// 那时 am validate beat 会按网格摘要把没换上的镜头报出来。
//
// 内容不变的文件不重写：run-all 靠 scene.json 的修改时间判断产物是否过期，
// 无变化的重跑不能把所有镜头都标成需要重渲染。
func commitWrites(writes []pendingWrite) error {
	type staged struct{ temp, path string }
	var ready []staged
	cleanup := func() {
		for _, s := range ready {
			_ = os.Remove(s.temp)
		}
	}
	for _, w := range writes {
		if old, err := os.ReadFile(w.path); err == nil && string(old) == string(w.body) {
			continue
		}
		temp, err := os.CreateTemp(filepath.Dir(w.path), "."+filepath.Base(w.path)+".*")
		if err != nil {
			cleanup()
			return err
		}
		_, writeErr := temp.Write(w.body)
		closeErr := temp.Close()
		if err := errors.Join(writeErr, closeErr, os.Chmod(temp.Name(), 0o644)); err != nil {
			_ = os.Remove(temp.Name())
			cleanup()
			return err
		}
		ready = append(ready, staged{temp.Name(), w.path})
	}
	for i, s := range ready {
		if err := os.Rename(s.temp, s.path); err != nil {
			for _, rest := range ready[i:] {
				_ = os.Remove(rest.temp)
			}
			return fmt.Errorf("写入 %s 失败，部分镜头可能仍是旧网格，请重跑 am beat cues：%w", s.path, err)
		}
	}
	return nil
}

// beatCoverage 校验全部镜头都带着与网格、计划一致的 beat 块，且首尾相接覆盖整首音乐。
func beatCoverage(root string, scenes []scene.Scene) error {
	_, segments, _, err := beatLayout(root, scenes)
	if err != nil {
		return err
	}
	var problems []string
	for i, s := range scenes {
		segment := segments[i]
		if s.Beat == nil {
			problems = append(problems, fmt.Sprintf("%s 缺少 beat 块", s.ID))
			continue
		}
		if s.Beat.StartFrame != segment.StartFrame || s.Beat.Frames != segment.Frames {
			problems = append(problems, fmt.Sprintf("%s 的帧区间与计划不符：beat 块 %d+%d，计划 %d+%d", s.ID, s.Beat.StartFrame, s.Beat.Frames, segment.StartFrame, segment.Frames))
			continue
		}
		if err := scene.VerifyBeatInputs(s); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", s.ID, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("卡点镜头校验未通过，改过网格或计划后请重新 am beat cues：\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func newValidateBeatCommand(stdout io.Writer) *cobra.Command {
	root := "."
	scenesRoot := ""
	cmd := &cobra.Command{
		Use: "beat", Short: "校验节拍网格、分镜计划与全部卡点镜头", Args: cobra.NoArgs,
		Long:    "检查正式网格已人工确认且属于当前音乐；分镜计划的小节合计等于网格小节数；镜头目录与计划逐一对应；每个镜头的 beat 块、时长与局部节拍文件都与网格一致，切点全部落在重拍上。失败退出 1。",
		Example: "  am validate beat --project-root .",
		RunE: func(*cobra.Command, []string) error {
			dir := scenesRoot
			if dir == "" {
				dir = filepath.Join(root, "scenes")
			}
			scenes, err := schedule.Plan(dir)
			if err != nil {
				return err
			}
			if err := beatCoverage(root, scenes); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "卡点校验通过：%d 个镜头，切点全部落在重拍上\n", len(scenes))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "project-root", ".", "项目根目录")
	cmd.Flags().StringVar(&scenesRoot, "scenes", "", "镜头目录，缺省为项目 scenes/")
	return cmd
}
