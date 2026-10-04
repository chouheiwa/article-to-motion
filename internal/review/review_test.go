package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

type fakeProber struct {
	media    mediaprobe.Media
	frames   int
	blankAt  map[float64]bool
	sheets   [][]float64
	columns  []int
	countErr error
	loudness mediaprobe.ProgramLoudness
	measured int
}

func (f *fakeProber) Probe(string) (mediaprobe.Media, error) { return f.media, nil }
func (f *fakeProber) CountFrames(string) (int, error)        { return f.frames, f.countErr }
func (f *fakeProber) FrameStats(_ string, at float64) (mediaprobe.FrameStats, error) {
	if f.blankAt[at] {
		return mediaprobe.FrameStats{AtSeconds: at, Mean: 0, StdDev: 0}, nil
	}
	return mediaprobe.FrameStats{AtSeconds: at, Mean: 100, StdDev: 40}, nil
}
func (f *fakeProber) MeasureProgramLoudness(string) (mediaprobe.ProgramLoudness, error) {
	f.measured++
	return f.loudness, nil
}
func (f *fakeProber) ContactSheet(_ string, points []float64, columns, _ int, _ string) error {
	f.sheets = append(f.sheets, points)
	f.columns = append(f.columns, columns)
	return nil
}

func media(video, audio float64, withAudio bool) mediaprobe.Media {
	m := mediaprobe.Media{DurationSeconds: max(video, audio), Video: &mediaprobe.VideoStream{DurationSeconds: video, FPSNum: 30, FPSDen: 1}}
	if withAudio {
		m.Audio = &mediaprobe.AudioStream{DurationSeconds: audio}
	}
	return m
}

func segments() []Segment { return []Segment{{"s1", 30}, {"s2", 60}, {"s3", 30}} }

func TestHealthyFilmPasses(t *testing.T) {
	p := &fakeProber{media: media(4, 4.02, true), frames: 120}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, Segments: segments(), OutDir: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Problems) != 0 {
		t.Fatalf("健康成片不应有问题：%v", report.Problems)
	}
	if len(report.Cuts) != 2 || report.Cuts[0].Frame != 30 || report.Cuts[1].Frame != 90 {
		t.Fatalf("交界位置不符：%+v", report.Cuts)
	}
	if len(report.Sheets) != 1 || len(report.Sheets[0].Tiles) != 9 || report.Sheets[0].Columns != 3 {
		t.Fatalf("联系表规划不符：%+v", report.Sheets)
	}
	tiles := report.Sheets[0].Tiles
	// s2 占第 30..89 帧：首帧 30、中间 60、末帧 89。
	if tiles[3].Frame != 30 || tiles[4].Frame != 60 || tiles[5].Frame != 89 || tiles[5].Position != "end" {
		t.Fatalf("s2 的取帧不符：%+v", tiles[3:6])
	}
	// 两个交界各一张条带：切点前 6 帧一行、切点后 6 帧一行。
	if len(report.Seams) != 2 || len(report.Seams[0].Tiles) != 12 || report.Seams[0].Tiles[5].Frame != 29 ||
		report.Seams[0].Tiles[6].Frame != 30 || report.Seams[0].Tiles[6].SceneID != "s2" || report.Seams[0].Tiles[5].Position != "before" {
		t.Fatalf("交界条带不符：%+v", report.Seams)
	}
}

func TestMachineCheckableProblems(t *testing.T) {
	cases := []struct {
		name   string
		prober *fakeProber
		want   string
	}{
		{"没有音轨", &fakeProber{media: media(4, 0, false), frames: 120}, "没有音轨"},
		{"音轨短了半秒", &fakeProber{media: media(4, 3.5, true), frames: 120}, "音画时长不一致"},
		{"拼接掉一帧", &fakeProber{media: media(4, 4, true), frames: 119}, "相差 -1 帧"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := Run(tc.prober, Options{Video: "final.mp4", FPS: 30, Segments: segments(), OutDir: "out"})
			if err != nil {
				t.Fatal(err)
			}
			if report.OK || !strings.Contains(strings.Join(report.Problems, "\n"), tc.want) {
				t.Fatalf("应报出 %q：%v", tc.want, report.Problems)
			}
		})
	}
}

// TestBlankCutIsOnlyAHint：切黑是合法转场，机器分不清，只提示不判失败。
func TestBlankCutIsOnlyAHint(t *testing.T) {
	p := &fakeProber{media: media(4, 4, true), frames: 120, blankAt: map[float64]bool{1: true}}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, Segments: segments(), OutDir: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Hints) != 1 || !strings.Contains(report.Hints[0], "s1 → s2") {
		t.Fatalf("交界纯色帧应只进 hints：%+v", report)
	}
}

func TestMissingStreamDurationsIsAHint(t *testing.T) {
	p := &fakeProber{media: media(0, 0, true), frames: 120}
	report, _ := Run(p, Options{Video: "final.mp4", FPS: 30, Segments: segments(), OutDir: "out"})
	if !report.OK || len(report.Hints) == 0 {
		t.Fatalf("没有流时长时应给提示而不是失败：%+v", report)
	}
}

func TestSheetsArePaginated(t *testing.T) {
	var many []Segment
	for i := 0; i < 7; i++ {
		many = append(many, Segment{ID: "s", Frames: 30})
	}
	p := &fakeProber{media: media(7, 7, true), frames: 210}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, Segments: many, OutDir: "out", TilesPerSheet: 18})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sheets) != 2 || len(report.Sheets[0].Tiles) != 18 || len(report.Sheets[1].Tiles) != 3 {
		t.Fatalf("分页不符：%d 页", len(report.Sheets))
	}
	if filepath.Base(report.Sheets[1].Image) != "sheet-02.png" {
		t.Fatalf("第二页文件名不符：%s", report.Sheets[1].Image)
	}
}

// TestWithoutSegmentsSamplesAtFixedInterval：脱离镜头目录时只做与镜头无关的检查。
func TestWithoutSegmentsSamplesAtFixedInterval(t *testing.T) {
	// 容器时长 13 秒（音轨更长），画面实际只有 360 帧：取帧上限必须按实测帧数。
	p := &fakeProber{media: media(13, 13, true), frames: 360}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, OutDir: "out", Every: 5})
	if err != nil {
		t.Fatal(err)
	}
	var frames []int
	for _, tile := range report.Sheets[0].Tiles {
		frames = append(frames, tile.Frame)
	}
	if len(frames) != 4 || frames[0] != 0 || frames[1] != 150 || frames[2] != 300 || frames[3] != 359 {
		t.Fatalf("按间隔取帧不符：%v", frames)
	}
	if report.ExpectedFrames != 0 || len(report.Cuts) != 0 || p.columns[0] != 4 {
		t.Fatalf("没有镜头时不应做帧数与交界检查：%+v", report)
	}
}

func TestRunRejectsBadOptionsAndPropagatesErrors(t *testing.T) {
	if _, err := Run(&fakeProber{media: media(1, 1, true)}, Options{Video: "x", FPS: 0}); err == nil {
		t.Error("帧率为 0 应报错")
	}
	if _, err := Run(&fakeProber{media: media(1, 1, true)}, Options{Video: "x", FPS: 30, Segments: []Segment{{"s", 0}}}); err == nil {
		t.Error("镜头帧数为 0 应报错")
	}
	boom := errors.New("boom")
	if _, err := Run(&fakeProber{media: media(4, 4, true), countErr: boom}, Options{Video: "x", FPS: 30, Segments: segments()}); !errors.Is(err, boom) {
		t.Errorf("统计帧数失败应原样返回：%v", err)
	}
	report, err := Run(&fakeProber{media: mediaprobe.Media{Audio: &mediaprobe.AudioStream{}}}, Options{Video: "x", FPS: 30})
	if err != nil || report.OK {
		t.Errorf("没有视频流应判不合格：%+v %v", report, err)
	}
}

// TestRunAgainstRealFfmpeg 用真实 ffmpeg 拼出「两镜 + 音轨」的成片走一遍全流程。
func TestRunAgainstRealFfmpeg(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("本机没有 %s，跳过集成测试", tool)
		}
	}
	dir := t.TempDir()
	film := filepath.Join(dir, "final.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", film)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成成片失败：%v\n%s", err, output)
	}
	tc, err := mediaprobe.New(envutil.EnvMap())
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "review")
	report, err := Run(tc, Options{Video: film, FPS: 30, Segments: []Segment{{"a", 30}, {"b", 30}}, OutDir: outDir, ThumbWidth: 64})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.CountedFrames != 60 {
		t.Fatalf("真实成片应通过：%+v", report)
	}
	if err := report.WriteJSON(filepath.Join(outDir, "review.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sheet-01.png", "review.json"} {
		if info, err := os.Stat(filepath.Join(outDir, name)); err != nil || info.Size() == 0 {
			t.Errorf("缺少产物 %s", name)
		}
	}
}

// TestShortFilmSkipsOutOfRangeCutsAndTiles：成片比镜头合计短时，越过末尾的交界
// 与格子没有帧可取；应报帧数问题并给出报告，而不是抽帧报错中断。
func TestShortFilmSkipsOutOfRangeCutsAndTiles(t *testing.T) {
	p := &fakeProber{media: media(2, 2, true), frames: 60}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, Segments: segments(), OutDir: "out"})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || len(report.Cuts) != 1 {
		t.Fatalf("应只检查成片范围内的交界：%+v", report.Cuts)
	}
	for _, tile := range report.Sheets[0].Tiles {
		if tile.Frame >= 60 {
			t.Fatalf("联系表不应包含越界帧：%+v", tile)
		}
	}
	if !strings.Contains(strings.Join(report.Hints, "\n"), "已跳过") {
		t.Fatalf("应提示跳过了越界部分：%v", report.Hints)
	}
}

func TestSilentFilm(t *testing.T) {
	report, _ := Run(&fakeProber{media: media(4, 0, false), frames: 120}, Options{Video: "x", FPS: 30, Segments: segments(), Silent: true})
	if !report.OK {
		t.Fatalf("静音成片没有音轨是正常的：%v", report.Problems)
	}
	report, _ = Run(&fakeProber{media: media(4, 4, true), frames: 120}, Options{Video: "x", FPS: 30, Segments: segments(), Silent: true})
	if report.OK {
		t.Fatal("按静音复查时出现音轨应判不合格")
	}
}

// 响度只进 hints：偏离目标不改变 OK。
func TestLoudnessOutsideTargetIsHintOnly(t *testing.T) {
	p := &fakeProber{media: media(4, 4, true), frames: 120, loudness: mediaprobe.ProgramLoudness{IntegratedLUFS: -11.2, TruePeakDBTP: 0.4, RangeLU: 3}}
	target := &LoudnessTarget{IntegratedLUFS: -16, Tolerance: 1, TruePeakMax: -1}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, OutDir: "out", Loudness: target})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Loudness == nil || report.Loudness.TruePeakDBTP != 0.4 {
		t.Fatalf("响度偏离不应判不合格，且实测值要写进报告：%+v", report)
	}
	joined := strings.Join(report.Hints, "\n")
	if !strings.Contains(joined, "-11.2 LUFS") || !strings.Contains(joined, "0.4 dBTP") || !strings.Contains(joined, "384k") {
		t.Fatalf("应分别提示积分响度与真峰：%v", report.Hints)
	}
}

func TestLoudnessWithinTargetAddsNoHint(t *testing.T) {
	p := &fakeProber{media: media(4, 4, true), frames: 120, loudness: mediaprobe.ProgramLoudness{IntegratedLUFS: -16.6, TruePeakDBTP: -1.2}}
	report, err := Run(p, Options{Video: "final.mp4", FPS: 30, OutDir: "out", Loudness: &LoudnessTarget{IntegratedLUFS: -16, Tolerance: 1, TruePeakMax: -1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hints) != 0 {
		t.Fatalf("达标时不应有提示：%v", report.Hints)
	}
}

// 静音交付与没有目标时：前者不量，后者只记录不比对。
func TestLoudnessSkippedWhenSilentAndUncomparedWithoutTarget(t *testing.T) {
	silent := &fakeProber{media: media(4, 0, false), frames: 120}
	report, err := Run(silent, Options{Video: "final.mp4", FPS: 30, OutDir: "out", Silent: true})
	if err != nil || silent.measured != 0 || report.Loudness != nil {
		t.Fatalf("静音交付不应测响度：%+v, %v", report, err)
	}
	loud := &fakeProber{media: media(4, 4, true), frames: 120, loudness: mediaprobe.ProgramLoudness{IntegratedLUFS: -8, TruePeakDBTP: 1}}
	report, err = Run(loud, Options{Video: "final.mp4", FPS: 30, OutDir: "out"})
	if err != nil || report.Loudness == nil || len(report.Hints) != 0 {
		t.Fatalf("没有目标时只记录实测值：%+v, %v", report, err)
	}
}
