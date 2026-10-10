// 本文件是 am scene cues：由字幕或短语锚点推导口播镜头的帧区间，
// 写入各镜头的 duration_seconds 与 timing 块。
package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/scene"
	"github.com/chouheiwa/articale-to-motion/internal/schedule"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
	"github.com/spf13/cobra"
)

func newSceneCuesCommand(stdout io.Writer) *cobra.Command {
	var opts sceneCuesOptions
	cmd := &cobra.Command{
		Use:   "cues SCENES",
		Args:  exactArgs(1, "SCENES"),
		Short: "由字幕或短语锚点推导镜头帧区间，写入时长与 timing 块",
		Long: `按分镜计划里每个镜头的起始锚点，把全片切成首尾相接的整数帧区间，
写入 SCENES 下每个镜头 scene.json 的 duration_seconds（帧数 ÷ 帧率，保留 6 位小数）
与 timing 块。镜头时长从此由本命令决定，不要手算、不要手改。

分镜计划（--plan，JSON）：
  {
    "schema": 1,
    "scenes": [
      {"id": "scene-001", "dir": "scenes/scene-001"},
      {"id": "scene-002", "dir": "scenes/scene-002", "first_subtitle": 5},
      {"id": "scene-003", "dir": "scenes/scene-003", "first_phrase": "p-07", "cut": "mid_gap"}
    ]
  }

  id              必须与镜头目录里 scene.json 的 id 一致
  dir             镜头目录，相对 --project-root；顺序必须与 SCENES 下镜头目录的
                  字典序一一对应，不得遗漏或多出
  first_subtitle  本镜头第一条字幕在 --srt 里的序号（按出现顺序，从 1 开始）
  first_phrase    本镜头第一条短语的 phrase_id，需要 --phrases
  cut             切点规则，缺省 next_start：
                    next_start  切在锚点开始处，前面的无文字空白并入前一个镜头
                    prev_end    切在锚点前一条字幕/短语结束处，空白并入本镜头
                    mid_gap     切在空白正中（向下取整）
                  前一条与锚点重叠、没有空白时，三种规则都切在锚点开始处。

first_subtitle 与 first_phrase 每个镜头恰好填一个；第一个镜头固定从第 0 帧开始，
可以不填。最后一个镜头延伸到 --total-frames。锚点必须严格递增，每个镜头至少一帧，
合计正好等于 --total-frames。秒换帧统一按「秒 × 帧率四舍五入」。

帧率缺省取镜头视觉规范的画幅（全部镜头必须一致）；给了 --fps 时必须与画幅相同。
给了 --phrases 时，把短语时间轴原样复制进每个镜头目录（` + scene.PhraseCopyFile + `），
并写进 timing.phrases，渲染时据此生成局部短语表。

timing 块：{"fps", "start_frame", "end_frame_exclusive", "plan", "phrases"}，
帧号是全片全局帧，plan 是分镜计划文件的 SHA-256。scene.json 其余字段与顺序不变。
任何一处不合格都不写文件；内容没变的文件不重写。

--check 只重算并核对，不写文件：时长、timing 块或短语副本与重算结果不符
（例如有人手改了 duration_seconds）时逐条列出并退出 1。
镜头带 song 或 beat 块时拒绝：歌曲、卡点的时长由 am song cues / am beat cues 决定。`,
		Example: `  am scene cues --plan production/scene-plan.json --srt transcription-production.srt --total-frames 5400 scenes/
  am scene cues --plan production/scene-plan.json --srt transcription-production.srt \
    --phrases production/phrase-timeline.json --total-frames 5400 scenes/
  am scene cues --check --plan production/scene-plan.json --srt transcription-production.srt --total-frames 5400 scenes/`,
		RunE: func(_ *cobra.Command, args []string) error {
			opts.scenesRoot = args[0]
			return runSceneCues(opts, stdout)
		},
	}
	cmd.Flags().StringVar(&opts.plan, "plan", "", "分镜计划 JSON（必填）")
	cmd.Flags().StringVar(&opts.srt, "srt", "", "全片字幕 SRT（必填）")
	cmd.Flags().StringVar(&opts.phrases, "phrases", "", "全片短语时间轴 phrase-timeline.json（可选）")
	cmd.Flags().IntVar(&opts.totalFrames, "total-frames", 0, "全片总帧数（必填）")
	cmd.Flags().IntVar(&opts.fps, "fps", 0, "帧率，缺省取镜头画幅")
	cmd.Flags().StringVar(&opts.projectRoot, "project-root", ".", "项目根目录，计划里的 dir 相对它解析")
	cmd.Flags().BoolVar(&opts.check, "check", false, "只核对，不写文件")
	return cmd
}

type sceneCuesOptions struct {
	plan, srt, phrases, projectRoot, scenesRoot string
	totalFrames, fps                            int
	check                                       bool
}

// sceneCue 是一个镜头重算出的结果。
type sceneCue struct {
	scene   scene.Scene
	segment scene.TimingSegment
	timing  scene.Timing
}

func runSceneCues(opts sceneCuesOptions, stdout io.Writer) error {
	if opts.plan == "" || opts.srt == "" {
		return fmt.Errorf("--plan 与 --srt 必填")
	}
	if opts.totalFrames <= 0 {
		return fmt.Errorf("--total-frames 必须是正整数")
	}
	if opts.fps < 0 {
		return fmt.Errorf("--fps 必须是正整数")
	}
	planBody, err := os.ReadFile(opts.plan)
	if err != nil {
		return fmt.Errorf("无法读取分镜计划：%w", err)
	}
	plan, err := scene.ParseTimingPlan(planBody)
	if err != nil {
		return err
	}
	planSum := sha256.Sum256(planBody)
	planSHA := hex.EncodeToString(planSum[:])

	scenes, err := matchPlanScenes(opts, plan)
	if err != nil {
		return err
	}
	fps, err := scenesFPS(scenes, opts.fps)
	if err != nil {
		return err
	}
	cues, err := srt.ReadCues(opts.srt)
	if err != nil {
		return err
	}
	var phraseBody []byte
	var phrases *scene.PhraseTimeline
	if opts.phrases != "" {
		if phraseBody, err = os.ReadFile(opts.phrases); err != nil {
			return fmt.Errorf("无法读取短语时间轴：%w", err)
		}
		timeline, err := scene.ParsePhraseTimeline(phraseBody, fps)
		if err != nil {
			return fmt.Errorf("%s：%w", opts.phrases, err)
		}
		phrases = &timeline
	}
	segments, err := scene.LayoutTiming(plan, cues, phrases, fps, opts.totalFrames)
	if err != nil {
		return err
	}
	results := make([]sceneCue, len(scenes))
	for i, s := range scenes {
		if s.Song != nil || s.Beat != nil {
			return fmt.Errorf("镜头 %s 带 song 或 beat 块：歌曲与卡点镜头的时长不由 am scene cues 决定", s.ID)
		}
		timing := scene.Timing{FPS: fps, StartFrame: segments[i].StartFrame, EndFrameExclusive: segments[i].EndFrameExclusive, Plan: planSHA}
		if phrases != nil {
			timing.Phrases = scene.PhraseCopyFile
		}
		results[i] = sceneCue{scene: s, segment: segments[i], timing: timing}
	}
	if opts.check {
		if problems := checkSceneCues(results, phraseBody); len(problems) > 0 {
			return fmt.Errorf("镜头时间合同与重算结果不符，请重新运行 am scene cues（不要手改时长）：\n  - %s", strings.Join(problems, "\n  - "))
		}
		printSceneCues(stdout, results)
		fmt.Fprintf(stdout, "核对通过：%d 个镜头，合计 %d 帧\n", len(results), opts.totalFrames)
		return nil
	}
	var writes []pendingWrite
	for _, r := range results {
		sceneFile := filepath.Join(r.scene.Directory, "scene.json")
		raw, err := os.ReadFile(sceneFile)
		if err != nil {
			return err
		}
		timingJSON, err := json.Marshal(r.timing)
		if err != nil {
			return err
		}
		body, err := rewriteJSONObject(raw, []jsonField{
			{"duration_seconds", json.RawMessage(scene.DurationLiteral(r.segment.Frames(), fps))},
			{"timing", timingJSON},
		})
		if err != nil {
			return fmt.Errorf("%s：%w", sceneFile, err)
		}
		writes = append(writes, pendingWrite{sceneFile, body})
		if phrases != nil {
			writes = append(writes, pendingWrite{filepath.Join(r.scene.Directory, scene.PhraseCopyFile), phraseBody})
		}
	}
	if err := commitWrites(writes); err != nil {
		return err
	}
	printSceneCues(stdout, results)
	fmt.Fprintf(stdout, "已写入 %d 个镜头的时长与 timing 块，合计 %d 帧\n", len(results), opts.totalFrames)
	return nil
}

// matchPlanScenes 把计划里的镜头与 SCENES 下的镜头目录逐一对应。
//
// 要求一一对应而不只是「计划里的目录都存在」：漏掉的镜头不会被写时长，
// 合计等于总帧数也就不再意味着全片被覆盖。
func matchPlanScenes(opts sceneCuesOptions, plan scene.TimingPlan) ([]scene.Scene, error) {
	scenes, err := schedule.Plan(opts.scenesRoot)
	if err != nil {
		return nil, err
	}
	if len(scenes) != len(plan.Scenes) {
		return nil, fmt.Errorf("分镜计划有 %d 个镜头，%s 下有 %d 个镜头目录", len(plan.Scenes), opts.scenesRoot, len(scenes))
	}
	var problems []string
	for i, entry := range plan.Scenes {
		dir := entry.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(opts.projectRoot, filepath.FromSlash(dir))
		}
		want, err := canonicalDir(dir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("第 %d 个镜头 %s 的目录 %s 不存在", i+1, entry.ID, entry.Dir))
			continue
		}
		have, err := canonicalDir(scenes[i].Directory)
		if err != nil {
			return nil, err
		}
		if want != have {
			problems = append(problems, fmt.Sprintf("第 %d 个：计划 %s，目录字典序 %s", i+1, entry.Dir, scenes[i].Directory))
			continue
		}
		if scenes[i].ID != entry.ID {
			problems = append(problems, fmt.Sprintf("第 %d 个：计划 id %s，%s/scene.json 的 id %s", i+1, entry.ID, entry.Dir, scenes[i].ID))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("分镜计划与镜头目录不一致（目录按字典序）：\n  - %s", strings.Join(problems, "\n  - "))
	}
	return scenes, nil
}

func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if stat, err := os.Stat(resolved); err != nil || !stat.IsDir() {
		return "", fmt.Errorf("%s 不是目录", dir)
	}
	return resolved, nil
}

// scenesFPS 取全部镜头的画幅帧率，必须一致；flag 非 0 时还必须与之相同。
func scenesFPS(scenes []scene.Scene, flag int) (int, error) {
	fps := flag
	for _, s := range scenes {
		canvas, err := scene.CanvasOf(s.Directory, s.StyleGuide)
		if err != nil {
			return 0, fmt.Errorf("镜头 %s：%w", s.ID, err)
		}
		if fps == 0 {
			fps = canvas.FPS
		}
		if canvas.FPS != fps {
			if flag != 0 {
				return 0, fmt.Errorf("--fps %d 与镜头 %s 的画幅 %dfps 不符", flag, s.ID, canvas.FPS)
			}
			return 0, fmt.Errorf("镜头帧率不一致：镜头 %s 是 %dfps，前面的镜头是 %dfps", s.ID, canvas.FPS, fps)
		}
	}
	return fps, nil
}

// checkSceneCues 逐个镜头比对重算结果，返回全部不符项。
func checkSceneCues(results []sceneCue, phraseBody []byte) []string {
	var problems []string
	for _, r := range results {
		s := r.scene
		frames := r.segment.Frames()
		if got := int(math.Round(s.DurationSeconds * float64(r.timing.FPS))); got != frames {
			problems = append(problems, fmt.Sprintf("%s：duration_seconds %g 秒是 %d 帧，应为 %d 帧（%s 秒）", s.ID, s.DurationSeconds, got, frames, scene.DurationLiteral(frames, r.timing.FPS)))
		} else if err := scene.VerifyFrameAlignment(s); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", s.ID, err))
		}
		switch {
		case s.Timing == nil:
			problems = append(problems, fmt.Sprintf("%s：缺少 timing 块", s.ID))
		case *s.Timing != r.timing:
			problems = append(problems, fmt.Sprintf("%s：timing 块是 %s，应为 %s", s.ID, describeTiming(*s.Timing), describeTiming(r.timing)))
		}
		if phraseBody != nil {
			copyPath := filepath.Join(s.Directory, scene.PhraseCopyFile)
			if have, err := os.ReadFile(copyPath); err != nil || !bytes.Equal(have, phraseBody) {
				problems = append(problems, fmt.Sprintf("%s：%s 与全片短语时间轴不一致", s.ID, scene.PhraseCopyFile))
			}
		}
	}
	return problems
}

func describeTiming(t scene.Timing) string {
	plan := t.Plan
	if len(plan) > 12 {
		plan = plan[:12]
	}
	return fmt.Sprintf("%dfps [%d, %d) plan=%s phrases=%q", t.FPS, t.StartFrame, t.EndFrameExclusive, plan, t.Phrases)
}

func printSceneCues(out io.Writer, results []sceneCue) {
	fmt.Fprintf(out, "%-20s %10s %10s %8s %12s\n", "镜头", "起始帧", "结束帧", "帧数", "秒")
	for _, r := range results {
		fmt.Fprintf(out, "%-20s %10d %10d %8d %12s\n", r.scene.ID, r.segment.StartFrame, r.segment.EndFrameExclusive,
			r.segment.Frames(), scene.DurationLiteral(r.segment.Frames(), r.timing.FPS))
	}
}

type jsonField struct {
	key   string
	value json.RawMessage
}

// rewriteJSONObject 改写一个 JSON 对象里的若干字段：已有字段原地替换，
// 新字段追加在末尾，其余字段的顺序与取值原样保留，整体按两格缩进重排。
//
// 不走 map[string]json.RawMessage：那样会把字段按字母序重排，每次运行都让
// 手写的 scene.json 面目全非，diff 里看不出真正改了什么。
func rewriteJSONObject(raw []byte, updates []jsonField) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if tok, err := decoder.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("不是 JSON 对象")
	}
	var fields []jsonField
	seen := map[string]bool{}
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("不是合法 JSON：%w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("不是合法 JSON 对象")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("字段 %s 不是合法 JSON：%w", key, err)
		}
		if seen[key] {
			return nil, fmt.Errorf("字段 %s 重复", key)
		}
		seen[key] = true
		fields = append(fields, jsonField{key, value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("不是合法 JSON：%w", err)
	}
	for _, u := range updates {
		replaced := false
		for i := range fields {
			if fields[i].key == u.key {
				fields[i].value = u.value
				replaced = true
			}
		}
		if !replaced {
			fields = append(fields, u)
		}
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, f := range fields {
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		var value bytes.Buffer
		if err := json.Indent(&value, f.value, "  ", "  "); err != nil {
			return nil, fmt.Errorf("字段 %s 无法重排：%w", f.key, err)
		}
		out.WriteString("  ")
		out.Write(key)
		out.WriteString(": ")
		out.Write(value.Bytes())
		if i < len(fields)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("}\n")
	return out.Bytes(), nil
}
