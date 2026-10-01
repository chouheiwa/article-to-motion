package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/scene"
)

// recordSleeps 把重试退避换成记录，测试既不真的等待，又能断言等了多久。
// 返回的读取函数带锁：退避发生在 worker goroutine 上。
func recordSleeps(t *testing.T) func() []time.Duration {
	t.Helper()
	original := waitFunc
	var mu sync.Mutex
	var slept []time.Duration
	waitFunc = func(_ context.Context, d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
	}
	t.Cleanup(func() { waitFunc = original })
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), slept...)
	}
}

func testScene(t *testing.T, root, id string) scene.Scene {
	t.Helper()
	dir := filepath.Join(root, id)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte("test"), 0o644)
	os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"`+id+`","duration_seconds":1,"output":"`+id+`.mp4","transcript":"transcript.srt","text":"hello"}`), 0o644)
	s, err := scene.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlanSortsScenesAndIgnoresHelpers(t *testing.T) {
	root := t.TempDir()
	testScene(t, root, "scene-002")
	testScene(t, root, "scene-001")
	os.Mkdir(filepath.Join(root, "attempts"), 0o755)
	got, err := Plan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "scene-001" || got[1].ID != "scene-002" {
		t.Fatalf("unexpected plan: %+v", got)
	}
}

func TestRunAllHonorsConcurrencyAndRetries(t *testing.T) {
	recordSleeps(t)
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001"), testScene(t, root, "scene-002"), testScene(t, root, "scene-003")}
	var active, maximum int32
	attempts := map[string]int{}
	var mu sync.Mutex
	runner := func(ctx context.Context, s scene.Scene) error {
		current := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			old := atomic.LoadInt32(&maximum)
			if current <= old || atomic.CompareAndSwapInt32(&maximum, old, current) {
				break
			}
		}
		mu.Lock()
		attempts[s.ID]++
		attempt := attempts[s.ID]
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		if s.ID == "scene-002" && attempt == 1 {
			return errors.New("renderer exit code 3")
		}
		return nil
	}
	report := RunAll(context.Background(), scenes, 2, 1, runner)
	if maximum > 2 || report.ExitCode() != 0 || report.CountValues[Succeeded] != 3 || attempts["scene-002"] != 2 {
		t.Fatalf("bad report=%+v max=%d attempts=%v", report, maximum, attempts)
	}
}

func TestRunAllReturnsPartialReportOnCancellation(t *testing.T) {
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001"), testScene(t, root, "scene-002")}
	ctx, cancel := context.WithCancel(context.Background())
	runner := func(ctx context.Context, s scene.Scene) error {
		if s.ID == "scene-001" {
			cancel()
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	report := RunAll(ctx, scenes, 1, 0, runner)
	if !report.Interrupted || len(report.Scenes) == 0 {
		t.Fatalf("expected partial interrupted report: %+v", report)
	}
}

func TestRunAllRecordsSkipWithoutRetry(t *testing.T) {
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	report := RunAll(context.Background(), scenes, 1, 5, func(context.Context, scene.Scene) error {
		return Outcome(Skipped, "已有合格产物")
	})
	if report.CountValues[Skipped] != 1 || report.Scenes[0].Attempts != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

// TestFatalErrorsAreNotRetried 守着下发 PROMPT 里的承诺：
// 「产物校验失败不会重试」。每次重试都是一次完整的 AI CLI 调用，
// 而规格不符是确定性的，重跑只会得到同样的产物。
func TestFatalErrorsAreNotRetried(t *testing.T) {
	recordSleeps(t)
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	calls := 0
	report := RunAll(context.Background(), scenes, 1, 5, func(context.Context, scene.Scene) error {
		calls++
		return Fatal(errors.New("产物时长不符：期望 2.833 秒，实测 5.000 秒"))
	})
	if calls != 1 {
		t.Errorf("不可重试的失败被重试了 %d 次", calls)
	}
	if report.CountValues[Failed] != 1 {
		t.Errorf("状态应当是 failed：%+v", report)
	}
	if report.Scenes[0].Attempts != 1 {
		t.Errorf("尝试次数应当如实记为 1，实际 %d", report.Scenes[0].Attempts)
	}
	if !strings.Contains(report.Scenes[0].Reason, "产物时长不符") {
		t.Errorf("原始错误信息丢失：%q", report.Scenes[0].Reason)
	}
}

// TestOrdinaryErrorsStillRetry 是上一条的反面：渲染器非零退出仍要重试，
// 否则一次网络抖动就会判定镜头失败。
func TestOrdinaryErrorsStillRetry(t *testing.T) {
	recordSleeps(t)
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	calls := 0
	RunAll(context.Background(), scenes, 1, 2, func(context.Context, scene.Scene) error {
		calls++
		return errors.New("渲染器退出码 3")
	})
	if calls != 3 {
		t.Errorf("普通失败应当重试到 1+2 次，实际 %d 次", calls)
	}
}

// TestRetriesBackOffBetweenAttempts 守着 PROMPT 里写的「退避 10 秒与 30 秒」。
// 失败后立刻重启 AI CLI 通常只会撞上同一个瞬时故障。
func TestRetriesBackOffBetweenAttempts(t *testing.T) {
	slept := recordSleeps(t)
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	RunAll(context.Background(), scenes, 1, 2, func(context.Context, scene.Scene) error {
		return errors.New("渲染器退出码 3")
	})
	got := slept()
	want := []time.Duration{10 * time.Second, 30 * time.Second}
	if len(got) != len(want) {
		t.Fatalf("退避次数 = %d，期望 %d：%v", len(got), len(want), got)
	}
	for i, d := range want {
		if got[i] != d {
			t.Errorf("第 %d 次退避 = %v，期望 %v", i+1, got[i], d)
		}
	}
}

// TestBackoffDoesNotDelayCancellation：中断时不能还坐等 30 秒退避走完。
// 用真实的 waitFunc，只把退避时长调长，确保测的是取消真的能打断等待。
func TestBackoffDoesNotDelayCancellation(t *testing.T) {
	original := retryBackoff
	retryBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { retryBackoff = original })

	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Report, 1)
	go func() {
		done <- RunAll(ctx, scenes, 1, 3, func(context.Context, scene.Scene) error {
			return errors.New("渲染器退出码 3")
		})
	}()
	// 让第一次失败进入退避，再取消。
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case report := <-done:
		if !report.Interrupted {
			t.Error("取消后报告应当标记 interrupted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消没有打断退避等待：RunAll 卡住了")
	}
}

func TestReportWritesAtomicJSONAndRenders(t *testing.T) {
	report := Report{SchemaVersion: 1, Seconds: 1.5, CountValues: map[Status]int{Succeeded: 1, Skipped: 0, Stale: 0, Failed: 0}, Scenes: []SceneResult{{SceneID: "scene-001", Status: Succeeded, Attempts: 1}}}
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	if err := report.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), `"schema_version": 1`) || !strings.Contains(report.Render(), "成功 1") {
		t.Fatalf("body=%s render=%s err=%v", body, report.Render(), err)
	}
	failed := report
	failed.CountValues[Failed] = 1
	if failed.ExitCode() != 1 {
		t.Fatal("failed report must exit one")
	}
}

// TestCoverageProblemsCatchesSplitMistakes 守着 PROMPT 里那条纯可判定的规则：
// 「所有镜头 duration_seconds 之和应等于字幕跨度，误差不超过 0.1 秒」。
// 过去只靠编排 agent 自己复核，漏一镜要等全部渲染完拼接时才发现。
func TestCoverageProblemsCatchesSplitMistakes(t *testing.T) {
	root := t.TempDir()
	makeScene := func(id string, duration float64) scene.Scene {
		dir := filepath.Join(root, id)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte("test"), 0o644)
		os.WriteFile(filepath.Join(dir, "scene.json"), fmt.Appendf(nil,
			`{"id":"%s","duration_seconds":%v,"output":"%s.mp4","transcript":"transcript.srt","text":"hello"}`,
			id, duration, id), 0o644)
		s, err := scene.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	full := []scene.Scene{makeScene("scene-001", 2.5), makeScene("scene-002", 5.0)}
	if problems := CoverageProblems(full, 7.5, 0.1); len(problems) != 0 {
		t.Errorf("合计 7.5 秒对上字幕跨度 7.5 秒，不该报错：%v", problems)
	}
	// 误差在容差内。
	if problems := CoverageProblems(full, 7.55, 0.1); len(problems) != 0 {
		t.Errorf("0.05 秒偏差在容差内：%v", problems)
	}
	// 漏了一镜：合计明显短于字幕跨度。
	problems := CoverageProblems(full, 12.0, 0.1)
	if len(problems) != 1 {
		t.Fatalf("应当报出总时长不符：%v", problems)
	}
	if !strings.Contains(problems[0], "7.500") || !strings.Contains(problems[0], "12.000") {
		t.Errorf("错误信息应当同时给出两个实测值：%s", problems[0])
	}
}

func TestCoverageProblemsRejectsEmptyAndDuplicateScenes(t *testing.T) {
	if problems := CoverageProblems(nil, 0, 0.1); len(problems) != 1 {
		t.Errorf("空镜头集合应当报错：%v", problems)
	}
	root := t.TempDir()
	a := testScene(t, filepath.Join(root, "a"), "scene-001")
	b := testScene(t, filepath.Join(root, "b"), "scene-001")
	problems := CoverageProblems([]scene.Scene{a, b}, 0, 0.1)
	if len(problems) != 1 || !strings.Contains(problems[0], "编号重复") {
		t.Errorf("重复编号应当被发现：%v", problems)
	}
}

// TestCoverageProblemsSkipsSpanCheckWhenNoSRT：没给字幕时只查集合自身，
// 不能凭空拿 0 当成期望跨度去比。
func TestCoverageProblemsSkipsSpanCheckWhenNoSRT(t *testing.T) {
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	if problems := CoverageProblems(scenes, 0, 0.1); len(problems) != 0 {
		t.Errorf("没给字幕跨度时不该做跨度比对：%v", problems)
	}
}

// TestStartOffsetsAccumulatesDurations 钉住"镜头全局起点"这条累加规则只有
// 一处实现：正向（am dialogue beats 把全局台词切成镜头本地节拍）与反向
// （am validate cast 把镜头本地节拍换算回全局时间）必须同源。
func TestStartOffsetsAccumulatesDurations(t *testing.T) {
	scenes := []scene.Scene{
		{ID: "scene-001", DurationSeconds: 2.5},
		{ID: "scene-002", DurationSeconds: 1.25},
		{ID: "scene-003", DurationSeconds: 3},
	}
	offsets := StartOffsets(scenes)
	want := []float64{0, 2.5, 3.75}
	if len(offsets) != len(want) {
		t.Fatalf("起点数量应与镜头数一致：得到 %v", offsets)
	}
	for i := range want {
		if offsets[i] != want[i] {
			t.Errorf("第 %d 个镜头的全局起点应为 %v，得到 %v", i, want[i], offsets[i])
		}
	}
}

func TestStartOffsetsEmpty(t *testing.T) {
	if got := StartOffsets(nil); len(got) != 0 {
		t.Fatalf("没有镜头时应返回空切片，得到 %v", got)
	}
}

// TestSceneDirsOrdersByDirectoryName 钉住"镜头先后顺序"这条规则只有一处
// 实现：Plan 与 am dialogue beats 的轻量读取都从这里取顺序，不各自 ReadDir
// 再各自排一遍。含 scene.json 才算镜头，其余目录与散落文件都要跳过。
func TestSceneDirsOrdersByDirectoryName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"scene-010", "scene-002", "scene-001"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scene.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "not-a-scene"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := SceneDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, dir := range dirs {
		names = append(names, filepath.Base(dir))
	}
	want := []string{"scene-001", "scene-002", "scene-010"}
	if len(names) != len(want) {
		t.Fatalf("应只认含 scene.json 的目录，得到 %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("镜头顺序应按目录名升序：want %v got %v", want, names)
		}
	}
}

// TestRetryCarriesPreviousFailure：重试必须带上上一次的失败原因与归档日志位置，
// 同一提示词原样重跑大概率重蹈覆辙。首次尝试不带任何说明。
func TestRetryCarriesPreviousFailure(t *testing.T) {
	recordSleeps(t)
	root := t.TempDir()
	scenes := []scene.Scene{testScene(t, root, "scene-001")}
	var notes []string
	RunAll(context.Background(), scenes, 1, 2, func(_ context.Context, s scene.Scene) error {
		notes = append(notes, s.RetryNote)
		return fmt.Errorf("渲染器退出码 %d", len(notes))
	})
	if len(notes) != 3 {
		t.Fatalf("应尝试 3 次，实际 %d 次", len(notes))
	}
	if notes[0] != "" {
		t.Errorf("首次尝试不应带重试说明：%q", notes[0])
	}
	for i, want := range []struct{ reason, archive string }{
		{"渲染器退出码 1", "attempts/attempt-01/"},
		{"渲染器退出码 2", "attempts/attempt-02/"},
	} {
		note := notes[i+1]
		if !strings.Contains(note, want.reason) || !strings.Contains(note, want.archive) {
			t.Errorf("第 %d 次重试说明应含 %q 与 %q，实际：%q", i+1, want.reason, want.archive, note)
		}
		if strings.Contains(note, root) {
			t.Errorf("归档路径应相对镜头目录，实际：%q", note)
		}
	}
}

func withDuration(s scene.Scene, seconds float64) scene.Scene {
	s.DurationSeconds = seconds
	return s
}

// TestRunAllDispatchesLongestFirstButReportsInTimelineOrder：单 worker 时派发顺序
// 就是执行顺序，能直接观测；报告必须仍按时间轴顺序，拼接与审阅都依赖它。
func TestRunAllDispatchesLongestFirstButReportsInTimelineOrder(t *testing.T) {
	root := t.TempDir()
	scenes := []scene.Scene{
		withDuration(testScene(t, root, "scene-001"), 2),
		withDuration(testScene(t, root, "scene-002"), 9),
		withDuration(testScene(t, root, "scene-003"), 2),
		withDuration(testScene(t, root, "scene-004"), 5),
	}
	var mu sync.Mutex
	var started []string
	report := RunAll(context.Background(), scenes, 1, 0, func(_ context.Context, s scene.Scene) error {
		mu.Lock()
		started = append(started, s.ID)
		mu.Unlock()
		return nil
	})
	if got := strings.Join(started, ","); got != "scene-002,scene-004,scene-001,scene-003" {
		t.Fatalf("应按时长降序派发、同长保持原序，实际 %s", got)
	}
	var reported []string
	for _, result := range report.Scenes {
		reported = append(reported, result.SceneID)
	}
	if got := strings.Join(reported, ","); got != "scene-001,scene-002,scene-003,scene-004" {
		t.Fatalf("报告应按时间轴顺序，实际 %s", got)
	}
	if scenes[0].ID != "scene-001" {
		t.Fatal("DispatchOrder 不得改动调用方的切片")
	}
}

func TestRenderWorkers(t *testing.T) {
	cases := []struct{ cpus, jobs, want int }{
		{10, 1, 0}, // 单镜头：沿用上游 auto
		{10, 3, 2}, // (10-2)/3
		{16, 3, 4}, // (16-2)/3
		{4, 3, 1},  // 核不够分时至少 1
		{2, 16, 1},
	}
	for _, tc := range cases {
		if got := RenderWorkers(tc.cpus, tc.jobs); got != tc.want {
			t.Errorf("RenderWorkers(%d, %d) = %d，期望 %d", tc.cpus, tc.jobs, got, tc.want)
		}
	}
}
