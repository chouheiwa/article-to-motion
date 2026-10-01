package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
)

type Status string

const (
	Succeeded Status = "succeeded"
	Skipped   Status = "skipped"
	Stale     Status = "stale"
	Failed    Status = "failed"
)

type SceneResult struct {
	SceneID  string  `json:"scene_id"`
	Status   Status  `json:"status"`
	Attempts int     `json:"attempts"`
	Seconds  float64 `json:"seconds"`
	Reason   string  `json:"reason"`
}

type Report struct {
	SchemaVersion int            `json:"schema_version"`
	Seconds       float64        `json:"seconds"`
	Interrupted   bool           `json:"interrupted"`
	Code          int            `json:"exit_code"`
	CountValues   map[Status]int `json:"counts"`
	Scenes        []SceneResult  `json:"scenes"`
}

func (r Report) ExitCode() int {
	if r.Interrupted {
		return 130
	}
	if r.CountValues[Failed] > 0 {
		return 1
	}
	return 0
}

func (r Report) WriteJSON(path string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return fsutil.AtomicWrite(path, body, 0o644)
}

func (r Report) Render() string {
	return fmt.Sprintf("镜头汇总（%d 个）\n  成功 %d  跳过 %d  过期 %d  失败 %d\n\n耗时 %.1f 秒", len(r.Scenes), r.CountValues[Succeeded], r.CountValues[Skipped], r.CountValues[Stale], r.CountValues[Failed], r.Seconds)
}

// SceneDirs 列出 root 下所有含 scene.json 的子目录，按目录名升序返回——
// 这个顺序就是镜头在时间轴上的先后顺序。
//
// 单抽出来的理由与 StartOffsets 相同：am dialogue beats 不能走 Plan
// （Plan 里的 scene.Load 会因为过期的 cast.beats 越界而拒绝加载，而那正是
// 该命令要修的东西），但它必须与 Plan 用同一套顺序，否则镜头起点的累加
// 就有了第二条路径。顺序规则放这里，两边共用；各自要读什么字段各自决定。
func SceneDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("无法读取镜头目录：%w", err)
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "scene.json")); os.IsNotExist(err) {
			continue
		}
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool { return filepath.Base(dirs[i]) < filepath.Base(dirs[j]) })
	return dirs, nil
}

func Plan(root string) ([]scene.Scene, error) {
	dirs, err := SceneDirs(root)
	if err != nil {
		return nil, err
	}
	result := make([]scene.Scene, 0, len(dirs))
	for _, dir := range dirs {
		s, err := scene.Load(dir)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}

// StartOffsets 给出每个镜头在项目全局时间轴上的起点：镜头首尾相接，
// 第 i 个镜头的起点是它前面所有镜头 duration_seconds 之和。返回的切片与
// 传入的 scenes 一一对应，顺序即 Plan 给出的顺序。
//
// 单抽成一个函数是因为这条累加规则有两个方向的使用者：am dialogue beats
// 用它把 dialogue.json 的全局台词切成镜头本地节拍，am validate cast 用它把
// 镜头本地节拍换算回全局时间做覆盖比对。两处各写一遍 cursor += duration
// 就是两个基准，正反向一旦漂开，校验通过与否取决于哪一边算错，而不取决
// 于产物对不对。
func StartOffsets(scenes []scene.Scene) []float64 {
	offsets := make([]float64, len(scenes))
	cursor := 0.0
	for i, s := range scenes {
		offsets[i] = cursor
		cursor += s.DurationSeconds
	}
	return offsets
}

// CoverageProblems 检查一组镜头是否构成一条完整、不重不漏的时间轴。
//
// 这条规则本来只写在 PROMPT 里，靠编排 agent 自己复核：
// 「所有镜头 scene.json 中 duration_seconds 之和应等于最后一条字幕结束时间
// 减第一条字幕开始时间，误差不超过 0.1 秒」。规则是纯可判定的，却没有任何
// 机器检查——漏掉一镜或某镜时长写错，要等全部渲染完拼接时才发现，
// 而那时每一镜都已经烧掉一次完整的 AI CLI 调用。
//
// srtSpan 传 0 表示不比对字幕跨度，只检查镜头集合自身（编号唯一、时长有效）。
func CoverageProblems(scenes []scene.Scene, srtSpan, tolerance float64) []string {
	var problems []string
	if len(scenes) == 0 {
		return append(problems, "没有找到任何镜头")
	}
	seen := make(map[string]string, len(scenes))
	total := 0.0
	for _, s := range scenes {
		if previous, duplicate := seen[s.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("镜头编号重复：%s 同时出现在 %s 和 %s",
				s.ID, filepath.Base(previous), filepath.Base(s.Directory)))
			continue
		}
		seen[s.ID] = s.Directory
		total += s.DurationSeconds
	}
	if srtSpan > 0 && math.Abs(total-srtSpan) > tolerance {
		problems = append(problems, fmt.Sprintf(
			"镜头总时长与字幕跨度不符：镜头合计 %.3f 秒，字幕跨度 %.3f 秒，相差 %.3f 秒（容差 %.3f）",
			total, srtSpan, math.Abs(total-srtSpan), tolerance))
	}
	return problems
}

type Runner func(context.Context, scene.Scene) error

type outcomeError struct {
	status Status
	reason string
}

func (e *outcomeError) Error() string { return e.reason }

func Outcome(status Status, reason string) error {
	return &outcomeError{status: status, reason: reason}
}

// fatalError 标记「重试也不会变的失败」。
type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

// Fatal 把错误标记为不可重试，RunAll 遇到它立刻停止本镜头的重试循环。
//
// 存在的理由：产物规格校验失败是确定性的——同样的提示词和同样的渲染器，
// 重跑只会得到同样不合规的产物。而每次重试都是一次完整的 AI CLI 调用，
// 代价以分钟和 token 计。下发给用户的 PROMPT 里也是这么承诺的：
// 「产物校验失败不会重试——那通常意味着提示词或时长声明有问题」。
func Fatal(err error) error {
	if err == nil {
		return nil
	}
	return &fatalError{err: err}
}

// retryBackoff 是两次重试之间的等待时间，与下发 PROMPT 里写的「退避 10 秒与 30 秒」一致。
// 重试次数超过表长时沿用最后一项。
var retryBackoff = []time.Duration{10 * time.Second, 30 * time.Second}

// waitFunc 是可注入的等待实现，供测试替换成不真等待的版本。
//
// 用 Timer + select 而不是起一个 goroutine 跑 time.Sleep：后者在 ctx 取消后
// 仍会挂着直到睡满，最长 30 秒——中断时留一串还在计时的 goroutine 没有意义。
var waitFunc = func(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// waitBeforeRetry 在第 attempt 次尝试失败后退避，ctx 取消时立即返回。
func waitBeforeRetry(ctx context.Context, attempt int) {
	if len(retryBackoff) == 0 {
		return
	}
	index := min(max(attempt-1, 0), len(retryBackoff)-1)
	waitFunc(ctx, retryBackoff[index])
}

// archiveAttempt 把本次尝试的产物与日志移进 attempts/attempt-NN，返回该目录。
func archiveAttempt(s scene.Scene) string {
	attemptsRoot := filepath.Join(s.Directory, "attempts")
	_ = os.MkdirAll(attemptsRoot, 0o755)
	entries, _ := os.ReadDir(attemptsRoot)
	number := len(entries) + 1
	destination := filepath.Join(attemptsRoot, fmt.Sprintf("attempt-%02d", number))
	_ = os.MkdirAll(destination, 0o755)
	for _, path := range []string{s.OutputPath(), s.StreamLog(), s.StderrLog(), s.UserLog()} {
		if _, err := os.Lstat(path); err == nil {
			_ = os.Rename(path, filepath.Join(destination, filepath.Base(path)))
		}
	}
	return destination
}

// retryNote 描述上一次失败，供下一次渲染的提示词引用。
// 日志位置写成相对镜头目录的路径：渲染器被锁在镜头目录内工作。
func retryNote(s scene.Scene, reason, archived string) string {
	relative, err := filepath.Rel(s.Directory, archived)
	if err != nil {
		relative = archived
	}
	return fmt.Sprintf("- 失败原因：%s\n- 上一次的输出流与错误日志已归档到 %s/，先读它们的末尾定位问题。", reason, filepath.ToSlash(relative))
}

// DispatchOrder 返回派发顺序：时长长的镜头先派发，时长相同时保持时间轴顺序。
//
// 镜头之间没有依赖，总耗时由最后完成的那个 worker 决定。按时间轴顺序派发时，
// 排在末尾的长镜头会在其他 worker 都空闲后才开始，独自拖长整批耗时；先派发
// 长镜头（LPT）让短镜头去填后面的空隙。渲染耗时与帧数大致成正比，时长是
// 手头最好的估计。报告仍按时间轴顺序输出，与派发顺序无关。
func DispatchOrder(scenes []scene.Scene) []scene.Scene {
	ordered := append([]scene.Scene(nil), scenes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].DurationSeconds > ordered[j].DurationSeconds })
	return ordered
}

// RenderWorkers 给出每个镜头渲染可用的帧捕获 worker 数。
//
// HyperFrames 的 --workers auto 为单次渲染独占整机设计（0.8.14 源码）：worker
// 数取「核数 - 2」「一半系统内存 / 1.5GB」「帧数 / 30」与上限
// max(6, min(16, 核数/8)) 中的最小值。jobs 个镜头同时渲染时这些估算互不知情，
// 每个都按独占 CPU 和一半内存算，合起来会超订。按 (核数 - 2) / jobs 均分，留两个核
// 给编码与系统；jobs 为 1 时返回 0，表示沿用上游的 auto。
func RenderWorkers(cpus, jobs int) int {
	if jobs <= 1 {
		return 0
	}
	return max(1, (cpus-2)/jobs)
}

func RunAll(ctx context.Context, scenes []scene.Scene, jobs, retries int, runner Runner) Report {
	started := time.Now()
	if jobs < 1 {
		jobs = 1
	}
	tasks := make(chan scene.Scene)
	results := make(chan SceneResult)
	var workers sync.WaitGroup
	worker := func() {
		defer workers.Done()
		for s := range tasks {
			begin := time.Now()
			result := SceneResult{SceneID: s.ID, Status: Failed}
			func() {
				defer func() {
					if r := recover(); r != nil {
						result.Reason = fmt.Sprintf("渲染器 panic: %v", r)
					}
				}()
				current := s
				for attempt := 1; attempt <= retries+1; attempt++ {
					result.Attempts = attempt
					err := runner(ctx, current)
					if err == nil {
						result.Status, result.Reason = Succeeded, ""
						break
					}
					var outcome *outcomeError
					if errors.As(err, &outcome) {
						result.Status, result.Reason = outcome.status, outcome.reason
						result.Attempts = 0
						break
					}
					result.Reason = err.Error()
					// 确定性失败：重跑只会得到同样的结果，而每次重试都是一次
					// 完整的 AI CLI 调用。状态和实际尝试次数照常保留。
					var fatal *fatalError
					if errors.As(err, &fatal) {
						break
					}
					if ctx.Err() != nil {
						break
					}
					if attempt <= retries {
						archived := archiveAttempt(s)
						current = s.WithRetryNote(retryNote(s, result.Reason, archived))
						waitBeforeRetry(ctx, attempt)
					}
				}
			}()
			result.Seconds = time.Since(begin).Seconds()
			results <- result
		}
	}
	for i := 0; i < jobs; i++ {
		workers.Add(1)
		go worker()
	}
	go func() {
		defer close(tasks)
		for _, s := range DispatchOrder(scenes) {
			select {
			case tasks <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	byID := make(map[string]SceneResult)
	for result := range results {
		byID[result.SceneID] = result
	}
	ordered := make([]SceneResult, 0, len(byID))
	counts := map[Status]int{Succeeded: 0, Skipped: 0, Stale: 0, Failed: 0}
	for _, s := range scenes {
		if result, ok := byID[s.ID]; ok {
			ordered = append(ordered, result)
			counts[result.Status]++
		}
	}
	report := Report{SchemaVersion: 1, Seconds: time.Since(started).Seconds(), Interrupted: ctx.Err() != nil, CountValues: counts, Scenes: ordered}
	report.Code = report.ExitCode()
	return report
}
