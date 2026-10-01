package mediaprobe

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
)

// realProbeOutput 是 ffprobe 7.0 对一条真实 h264/yuv420p 视频的实际输出。
//
// 逐字保留而不是手写简化版：数字字段和字符串字段在 ffprobe 里是混着来的
// （width 是数字，nb_frames 和 duration 是字符串），照猜写解析必然出错。
const realProbeOutput = `{
    "streams": [
        {
            "index": 0,
            "codec_name": "h264",
            "codec_type": "video",
            "width": 1080,
            "height": 1440,
            "pix_fmt": "yuv420p",
            "r_frame_rate": "30/1",
            "avg_frame_rate": "30/1",
            "duration": "2.833000",
            "bit_rate": "58024",
            "nb_frames": "85"
        },
        {
            "index": 1,
            "codec_name": "aac",
            "codec_type": "audio",
            "sample_rate": "48000",
            "channels": 2
        }
    ],
    "format": {
        "filename": "scene-001.mp4",
        "nb_streams": 2,
        "format_name": "mov,mp4,m4a,3gp,3g2,mj2",
        "duration": "2.833000",
        "size": "8487"
    }
}`

func TestParseReadsRealFfprobeFieldTypes(t *testing.T) {
	media, err := parse([]byte(realProbeOutput), "scene-001.mp4")
	if err != nil {
		t.Fatalf("解析真实 ffprobe 输出失败：%v", err)
	}
	if media.DurationSeconds != 2.833 {
		t.Errorf("时长 = %v，期望 2.833", media.DurationSeconds)
	}
	if media.Video == nil {
		t.Fatal("没有解析出视频流")
	}
	v := media.Video
	if v.Codec != "h264" || v.WidthPx != 1080 || v.HeightPx != 1440 || v.PixelFormat != "yuv420p" {
		t.Errorf("视频流解析错误：%+v", v)
	}
	if v.NBFrames != 85 {
		t.Errorf("nb_frames = %d，期望 85（字符串字段）", v.NBFrames)
	}
	if v.FPS() != 30 {
		t.Errorf("FPS = %v，期望 30（由 r_frame_rate 的分数解析）", v.FPS())
	}
	if v.DurationSeconds != 2.833 {
		t.Errorf("视频流时长 = %v，期望 2.833（流级 duration 是字符串）", v.DurationSeconds)
	}
	if media.Audio == nil {
		t.Fatal("没有解析出音频流")
	}
	if media.Audio.SampleRate != 48000 || media.Audio.Channels != 2 {
		t.Errorf("音频流解析错误：%+v", media.Audio)
	}
	if media.Audio.DurationSeconds != 0 {
		t.Errorf("音频流没声明时长时应为 0，实际 %v", media.Audio.DurationSeconds)
	}
}

func TestParseHandlesMissingOptionalFields(t *testing.T) {
	// 没有音轨、容器没声明 nb_frames——静音母版的常见形态，不该报错。
	body := `{"streams":[{"codec_type":"video","codec_name":"h264","width":1080,"height":1440,
	"pix_fmt":"yuv420p","r_frame_rate":"30/1"}],"format":{"duration":"1.5"}}`
	media, err := parse([]byte(body), "x.mp4")
	if err != nil {
		t.Fatalf("缺少可选字段不该报错：%v", err)
	}
	if media.Audio != nil {
		t.Error("没有音频流时 Audio 应当是 nil")
	}
	if media.Video.NBFrames != 0 {
		t.Errorf("容器未声明 nb_frames 时应当是 0，实际 %d", media.Video.NBFrames)
	}
}

func TestParseAllowsAudioOnlyFile(t *testing.T) {
	// 纯音频文件是合法形态：dialogue 包的 TTS 片段、静音、拼接产物都没有视频流。
	body := `{"streams":[{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2}],
	"format":{"duration":"1.0"}}`
	media, err := parse([]byte(body), "x.m4a")
	if err != nil {
		t.Fatalf("纯音频文件不该报错：%v", err)
	}
	if media.Video != nil {
		t.Errorf("没有视频流时 Video 应当是 nil，实际 %+v", media.Video)
	}
	if media.Audio == nil || media.Audio.SampleRate != 48000 || media.Audio.Channels != 2 {
		t.Errorf("音频流解析错误：%+v", media.Audio)
	}
}

func TestParseRejectsFileWithoutAnyStream(t *testing.T) {
	body := `{"streams":[],"format":{"duration":"1.0"}}`
	if _, err := parse([]byte(body), "x.bin"); err == nil {
		t.Error("没有任何音视频流的文件应当报错")
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := parse([]byte("not json"), "x.mp4"); err == nil {
		t.Error("非法 JSON 应当报错")
	}
}

// TestFPSParsesRationalRepresentations：ffprobe 用分数表示帧率，
// 29.97 会写成 30000/1001，不能按整数比较。
func TestFPSParsesRationalRepresentations(t *testing.T) {
	cases := map[string]float64{
		"30/1":       30,
		"30000/1001": 29.97002997002997,
		"25/1":       25,
		"60/1":       60,
		"0/0":        0,
		"":           0,
		"garbage":    0,
		"1/0":        0,
		"60000/1001": 59.94005994005994,
		"2997/100":   29.97,
	}
	for input, want := range cases {
		got := parseRational(input)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("parseRational(%q) = %v，期望 %v", input, got, want)
		}
	}
}

// TestCheckReportsEveryViolationNotJustTheFirst 是这个包的核心设计保证。
//
// 验收门每次只报一条，调用方就得「修一条、重跑一次、再发现下一条」，
// 一次完整成片检查要循环好几轮。全部报出来才能一次修完。
func TestCheckReportsEveryViolationNotJustTheFirst(t *testing.T) {
	media := Media{
		DurationSeconds: 5.0,
		Video: &VideoStream{
			Codec: "vp9", WidthPx: 720, HeightPx: 1280,
			PixelFormat: "yuv444p", FPSNum: 25, FPSDen: 1, NBFrames: 125,
		},
		Audio: &AudioStream{Codec: "aac", SampleRate: 44100, Channels: 1},
	}
	spec := Spec{
		WidthPx: 1080, HeightPx: 1440, FPS: 30,
		Codec: "h264", PixelFormat: "yuv420p",
		DurationSeconds: 2.833, Tolerance: 0.1,
		Audio: AudioAbsent,
	}
	problems := spec.Check(media)
	if len(problems) < 6 {
		t.Fatalf("每一项都不符，却只报了 %d 条：%v", len(problems), problems)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"分辨率", "帧率", "编码", "像素格式", "时长", "音轨"} {
		if !strings.Contains(joined, want) {
			t.Errorf("漏报了 %s：\n%s", want, joined)
		}
	}
	// 每条都要带实测值，否则调用方还得自己再跑一次 ffprobe 才知道差多少。
	for _, want := range []string{"720x1280", "vp9", "yuv444p"} {
		if !strings.Contains(joined, want) {
			t.Errorf("报告里没有实测值 %s：\n%s", want, joined)
		}
	}
}

func TestCheckPassesWhenEverythingMatches(t *testing.T) {
	media := Media{
		DurationSeconds: 2.833,
		Video: &VideoStream{
			Codec: "h264", WidthPx: 1080, HeightPx: 1440,
			PixelFormat: "yuv420p", FPSNum: 30, FPSDen: 1, NBFrames: 85,
		},
	}
	spec := Spec{
		WidthPx: 1080, HeightPx: 1440, FPS: 30, Codec: "h264",
		PixelFormat: "yuv420p", DurationSeconds: 2.833, Tolerance: 0.1,
		Audio: AudioAbsent, Frames: 85,
	}
	if problems := spec.Check(media); len(problems) != 0 {
		t.Errorf("全部相符却报了问题：%v", problems)
	}
}

// TestCheckZeroFieldsAreNotChecked：Spec 的零值表示「不检查这一项」，
// 否则每个调用方都得把全部字段填满才能只查一件事。
func TestCheckZeroFieldsAreNotChecked(t *testing.T) {
	media := Media{
		DurationSeconds: 5,
		Video:           &VideoStream{Codec: "vp9", WidthPx: 1, HeightPx: 1, PixelFormat: "rgb24", FPSNum: 7, FPSDen: 1},
		Audio:           &AudioStream{Codec: "aac", SampleRate: 8000, Channels: 5},
	}
	if problems := (Spec{}).Check(media); len(problems) != 0 {
		t.Errorf("空 Spec 不该检查任何东西，却报了：%v", problems)
	}
}

func TestCheckAudioExpectations(t *testing.T) {
	withAudio := Media{Video: &VideoStream{}, Audio: &AudioStream{Codec: "aac", SampleRate: 48000, Channels: 2}}
	silent := Media{Video: &VideoStream{}}

	if problems := (Spec{Audio: AudioAbsent}).Check(withAudio); len(problems) == 0 {
		t.Error("AudioAbsent 遇到音轨应当报错")
	}
	if problems := (Spec{Audio: AudioAbsent}).Check(silent); len(problems) != 0 {
		t.Errorf("AudioAbsent 遇到静音文件不该报错：%v", problems)
	}
	if problems := (Spec{Audio: AudioPresent}).Check(silent); len(problems) == 0 {
		t.Error("AudioPresent 遇到无音轨应当报错")
	}
	if problems := (Spec{Audio: AudioIgnore}).Check(withAudio); len(problems) != 0 {
		t.Errorf("AudioIgnore 不该检查音轨：%v", problems)
	}
	// 采样率和声道数只在有音轨时才有意义。
	problems := (Spec{Audio: AudioPresent, SampleRate: 48000, Channels: 2}).Check(withAudio)
	if len(problems) != 0 {
		t.Errorf("音频规格相符却报错：%v", problems)
	}
	problems = (Spec{Audio: AudioPresent, SampleRate: 48000, Channels: 2}).Check(
		Media{Video: &VideoStream{}, Audio: &AudioStream{SampleRate: 44100, Channels: 1}})
	if len(problems) != 2 {
		t.Errorf("采样率和声道都不符应当报 2 条，实际 %d 条：%v", len(problems), problems)
	}
}

// TestCheckFramesUsesDeclaredCountOnlyWhenAvailable：容器没声明 nb_frames 时
// 不能把 0 当成「0 帧」报错——那是「未知」，要靠 CountFrames 精确数。
func TestCheckFramesUsesDeclaredCountOnlyWhenAvailable(t *testing.T) {
	unknown := Media{Video: &VideoStream{NBFrames: 0}}
	if problems := (Spec{Frames: 85}).Check(unknown); len(problems) != 0 {
		t.Errorf("nb_frames 未知时不该判定帧数不符：%v", problems)
	}
	wrong := Media{Video: &VideoStream{NBFrames: 84}}
	if problems := (Spec{Frames: 85}).Check(wrong); len(problems) != 1 {
		t.Errorf("nb_frames 明确不符应当报错：%v", problems)
	}
}

func TestCheckToleranceIsRespected(t *testing.T) {
	media := Media{DurationSeconds: 2.9, Video: &VideoStream{}}
	if problems := (Spec{DurationSeconds: 2.833, Tolerance: 0.1}).Check(media); len(problems) != 0 {
		t.Errorf("差 0.067 秒在 0.1 容差内，不该报错：%v", problems)
	}
	if problems := (Spec{DurationSeconds: 2.833, Tolerance: 0.01}).Check(media); len(problems) != 1 {
		t.Errorf("差 0.067 秒超出 0.01 容差，应当报错：%v", problems)
	}
}

func TestSpecRejectsInvalidTolerance(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), -1} {
		if err := (Spec{DurationSeconds: 1, Tolerance: bad}).Validate(); err == nil {
			t.Errorf("容差 %v 应当被拒绝", bad)
		}
	}
	if err := (Spec{DurationSeconds: 1, Tolerance: 0.1}).Validate(); err != nil {
		t.Errorf("合法容差被拒绝：%v", err)
	}
}

// --- 以下需要真实 ffmpeg/ffprobe ---

func toolchain(t *testing.T) Toolchain {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("本机没有 ffprobe，跳过集成测试")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机没有 ffmpeg，跳过集成测试")
	}
	tc, err := New(envutil.EnvMap())
	if err != nil {
		t.Fatalf("无法建立工具链：%v", err)
	}
	return tc
}

// sample 用 ffmpeg 现场生成测试素材。不入库 testdata：二进制 fixture 会让
// 「测试通过」依赖某个特定 ffmpeg 版本编出的文件，现场生成才是在测真实链路。
func sample(t *testing.T, tc Toolchain, filter string, extra ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.mp4")
	argv := []string{"-v", "error", "-y", "-f", "lavfi", "-i", filter,
		"-c:v", "libx264", "-pix_fmt", "yuv420p"}
	argv = append(argv, extra...)
	argv = append(argv, path)
	cmd := exec.Command(ffmpegFor(t), argv...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成测试素材失败：%v\n%s", err, output)
	}
	return path
}

func TestProbeReadsRealFile(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=1080x1440:rate=30:duration=2")
	media, err := tc.Probe(path)
	if err != nil {
		t.Fatalf("Probe 失败：%v", err)
	}
	if media.Video.WidthPx != 1080 || media.Video.HeightPx != 1440 {
		t.Errorf("分辨率 = %dx%d，期望 1080x1440", media.Video.WidthPx, media.Video.HeightPx)
	}
	if media.Video.Codec != "h264" || media.Video.PixelFormat != "yuv420p" {
		t.Errorf("编码/像素格式 = %s/%s", media.Video.Codec, media.Video.PixelFormat)
	}
	if math.Abs(media.Video.FPS()-30) > 1e-6 {
		t.Errorf("帧率 = %v", media.Video.FPS())
	}
	if media.Audio != nil {
		t.Error("lavfi testsrc 不该有音轨")
	}
	if math.Abs(media.DurationSeconds-2) > 0.05 {
		t.Errorf("时长 = %v，期望约 2", media.DurationSeconds)
	}
}

func TestProbeRejectsMissingAndNonMediaFiles(t *testing.T) {
	tc := toolchain(t)
	if _, err := tc.Probe(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Error("不存在的文件应当报错")
	}
	junk := filepath.Join(t.TempDir(), "junk.mp4")
	if err := os.WriteFile(junk, []byte("this is not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.Probe(junk); err == nil {
		t.Error("非媒体文件应当报错")
	}
}

func TestCountFramesIsExact(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=2")
	got, err := tc.CountFrames(path)
	if err != nil {
		t.Fatalf("CountFrames 失败：%v", err)
	}
	if got != 60 {
		t.Errorf("帧数 = %d，期望 60", got)
	}
}

func TestDecodeAcceptsIntactFileAndRejectsTruncated(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=2")
	if err := tc.Decode(path); err != nil {
		t.Errorf("完好文件应当可完整解码：%v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken.mp4")
	// 砍掉后半段：moov 在尾部时会直接损坏，即便还能读出头也解不完。
	if err := os.WriteFile(broken, body[:len(body)/3], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tc.Decode(broken); err == nil {
		t.Error("截断文件应当解码失败")
	}
}

func TestFrameStatsDetectsFlatAndDarkFrames(t *testing.T) {
	tc := toolchain(t)

	black := sample(t, tc, "color=c=black:size=320x240:rate=30:duration=1")
	stats, err := tc.FrameStats(black, 0)
	if err != nil {
		t.Fatalf("FrameStats 失败：%v", err)
	}
	if !stats.Flat() {
		t.Errorf("纯黑帧应当判定为平坦：mean=%v stddev=%v", stats.Mean, stats.StdDev)
	}
	if !stats.Dark() {
		t.Errorf("纯黑帧应当判定为暗：mean=%v", stats.Mean)
	}

	white := sample(t, tc, "color=c=white:size=320x240:rate=30:duration=1")
	stats, err = tc.FrameStats(white, 0)
	if err != nil {
		t.Fatalf("FrameStats 失败：%v", err)
	}
	if !stats.Flat() {
		t.Errorf("纯白帧也是空白帧，应当判定为平坦：stddev=%v", stats.StdDev)
	}
	if stats.Dark() {
		t.Errorf("纯白帧不该判定为暗：mean=%v", stats.Mean)
	}

	// 有内容的帧不该触发——这是误判的方向，比漏判更该守。
	busy := sample(t, tc, "testsrc=size=320x240:rate=30:duration=1")
	stats, err = tc.FrameStats(busy, 0)
	if err != nil {
		t.Fatalf("FrameStats 失败：%v", err)
	}
	if stats.Flat() {
		t.Errorf("有内容的帧被误判为空白：stddev=%v", stats.StdDev)
	}
}

// TestFrameStatsDoesNotFlagSparseTextOnDarkBackground 钉死空白帧阈值的上界。
//
// 上一条测试用的 testsrc 是高对比彩条，标准差远高于任何合理阈值，
// 约束不到阈值本身——把阈值放大 25 倍它照样通过。真正的误判风险是这一类帧：
// 深色背景上只有一行标题，亮部不到画面的 1%，标准差落在 20 出头。
// 这是合法封面，判成空白帧就是假阳性。
func TestFrameStatsDoesNotFlagSparseTextOnDarkBackground(t *testing.T) {
	tc := toolchain(t)
	path := filepath.Join(t.TempDir(), "sparse.mp4")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=#0a0a0a:size=320x240:rate=30:duration=1",
		"-vf", "drawbox=x=40:y=110:w=60:h=12:color=white@1:t=fill",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成稀疏文字素材失败：%v\n%s", err, output)
	}
	stats, err := tc.FrameStats(path, 0)
	if err != nil {
		t.Fatalf("FrameStats 失败：%v", err)
	}
	if stats.Flat() {
		t.Errorf("深色背景 + 稀疏文字被误判为空白帧：mean=%.2f stddev=%.2f", stats.Mean, stats.StdDev)
	}
	if stats.Blank() {
		t.Errorf("合法封面被 Blank 判定命中：mean=%.2f stddev=%.2f", stats.Mean, stats.StdDev)
	}
	// 这一帧确实偏暗，Dark 会报——但它只是提示，不该让 Blank 成立。
	if !stats.Dark() {
		t.Logf("提示：该帧 mean=%.2f 未触发 Dark，阈值可能偏低", stats.Mean)
	}
}

func TestFrameStatsSeeksToRequestedTime(t *testing.T) {
	tc := toolchain(t)
	// 前 1 秒纯黑，之后是彩色测试图；抽 0 秒和 1.5 秒应当得到不同结果。
	path := filepath.Join(t.TempDir(), "split.mp4")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:size=320x240:rate=30:duration=1",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration=1",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[out]", "-map", "[out]",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成分段素材失败：%v\n%s", err, output)
	}
	first, err := tc.FrameStats(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	later, err := tc.FrameStats(path, 1.5)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Flat() {
		t.Errorf("第 0 帧是纯黑，应当平坦：stddev=%v", first.StdDev)
	}
	if later.Flat() {
		t.Errorf("1.5 秒处是测试图，不该平坦：stddev=%v——说明 seek 没生效", later.StdDev)
	}
}

func TestExtractFrameWritesReadablePNG(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=1")
	dest := filepath.Join(t.TempDir(), "nested", "frame-0.png")
	if err := tc.ExtractFrame(path, 0, dest); err != nil {
		t.Fatalf("ExtractFrame 失败：%v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("抽帧产物不存在：%v", err)
	}
	if len(body) < 8 || string(body[1:4]) != "PNG" {
		t.Errorf("产物不是 PNG，前 8 字节：%v", body[:min(8, len(body))])
	}
}

// 注：min 用 Go 1.21 起的内置函数，本包不再自行定义。

func TestExtractFrameRejectsTimeBeyondDuration(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=1")
	dest := filepath.Join(t.TempDir(), "frame.png")
	if err := tc.ExtractFrame(path, 99, dest); err == nil {
		t.Error("超出时长的抽帧应当报错，而不是静默产出空文件")
	}
}

func TestNewReportsMissingToolWithExitCode127Marker(t *testing.T) {
	_, err := New(map[string]string{"PATH": t.TempDir()})
	if err == nil {
		t.Fatal("PATH 里没有 ffprobe 时应当报错")
	}
	if !strings.Contains(err.Error(), envutil.MissingToolPrefix) {
		t.Errorf("错误信息缺少退出码 127 的判定前缀：%s", err)
	}
}

// ffmpegFor 解析测试用的 ffmpeg 路径。Toolchain 已改为惰性解析 ffmpeg，
// 不再持有该字段，但生成测试素材仍然需要它。
func ffmpegFor(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("本机没有 ffmpeg，跳过集成测试")
	}
	return path
}

func TestVerifySeparatesProblemsFromProbeFailures(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=1")

	// 规格不符：跑成功了，但不合格。
	report, err := tc.Verify(path, VerifyOptions{Spec: Spec{WidthPx: 1080, HeightPx: 1440}})
	if err != nil {
		t.Fatalf("规格不符不该返回 error，应当体现在 Problems 里：%v", err)
	}
	if report.OK || len(report.Problems) == 0 {
		t.Errorf("规格不符却判定通过：%+v", report)
	}

	// 检查跑不成：返回 error，而不是一份 OK=false 的报告。
	if _, err := tc.Verify(filepath.Join(t.TempDir(), "nope.mp4"), VerifyOptions{}); err == nil {
		t.Error("文件不存在应当返回 error")
	}
}

func TestVerifyExactFramesOverridesContainerDeclaration(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=2")
	report, err := tc.Verify(path, VerifyOptions{Spec: Spec{Frames: 60}, ExactFrames: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Errorf("60 帧应当通过：%v", report.Problems)
	}
	if report.Media.Video.NBFrames != 60 {
		t.Errorf("报告里的帧数应当是精确统计值 60，实际 %d", report.Media.Video.NBFrames)
	}
	report, err = tc.Verify(path, VerifyOptions{Spec: Spec{Frames: 59}, ExactFrames: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Error("帧数不符应当判定失败")
	}
}

// parse 放行纯音频文件后 media.Video 可能是 nil。ExactFrames 会调用 CountFrames
// 再回填 media.Video.NBFrames——对纯音频文件必须整段跳过这一步，直接落到
// Spec.Check 给出「文件没有视频流」这条问题，而不是对 nil 的 Video 取字段 panic。
func TestVerifyExactFramesOnAudioOnlyFileDoesNotPanic(t *testing.T) {
	tc := toolchain(t)
	ffmpeg := ffmpegFor(t)
	path := filepath.Join(t.TempDir(), "audio-only.wav")
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成素材失败：%v\n%s", err, output)
	}
	report, err := tc.Verify(path, VerifyOptions{ExactFrames: true})
	if err != nil {
		t.Fatalf("纯音频文件不该返回 error，应当体现在 Problems 里：%v", err)
	}
	if report.OK {
		t.Error("没有视频流应当判定失败")
	}
	found := false
	for _, problem := range report.Problems {
		if strings.Contains(problem, "没有视频流") {
			found = true
		}
	}
	if !found {
		t.Errorf("Problems 里应当含「没有视频流」，实际 %v", report.Problems)
	}
}

func TestVerifyFrameZeroBlankIsAProblemButDarkIsOnlyAHint(t *testing.T) {
	tc := toolchain(t)

	black := sample(t, tc, "color=c=black:size=320x240:rate=30:duration=1")
	report, err := tc.Verify(black, VerifyOptions{CheckFrameZero: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Error("纯黑第 0 帧应当判定失败")
	}
	if report.FrameZero == nil {
		t.Error("报告里应当带上第 0 帧的统计量")
	}

	// 深色背景 + 稀疏文字：偏暗但可读，只该进 Hints，不该让 OK 变 false。
	dark := filepath.Join(t.TempDir(), "dark.mp4")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=#0a0a0a:size=320x240:rate=30:duration=1",
		"-vf", "drawbox=x=40:y=110:w=60:h=12:color=white@1:t=fill",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", dark)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成素材失败：%v\n%s", err, output)
	}
	report, err = tc.Verify(dark, VerifyOptions{CheckFrameZero: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Errorf("偏暗但有内容的封面不该判定失败：%v", report.Problems)
	}
	if len(report.Hints) == 0 {
		t.Error("偏暗应当留下提示")
	}
}

// TestVerifyDecodeCatchesTruncatedFile 证明 Decode 能发现 Probe 发现不了的损坏。
//
// 必须用 +faststart 把 moov atom 放到文件头：默认编码把它写在尾部，
// 一截断连 Probe 都失败，测到的就是「文件读不了」而不是「能读但解不完」——
// 后者才是这条检查存在的理由。
func TestVerifyDecodeCatchesTruncatedFile(t *testing.T) {
	tc := toolchain(t)
	path := sample(t, tc, "testsrc=size=320x240:rate=30:duration=2", "-movflags", "+faststart")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken.mp4")
	if err := os.WriteFile(broken, body[:len(body)/3], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.Probe(broken); err != nil {
		t.Fatalf("前置条件不成立：moov 在头部时 Probe 应当仍然成功：%v", err)
	}
	report, err := tc.Verify(broken, VerifyOptions{Decode: true})
	if err != nil {
		t.Fatalf("Probe 成功时 Verify 不该返回 error：%v", err)
	}
	if report.OK {
		t.Error("截断文件应当在解码检查里被发现")
	}
	// 不开 Decode 时这个文件会被判定通过——这正是过去的状态。
	report, err = tc.Verify(broken, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Error("不开 Decode 时截断文件应当「通过」，否则这条测试没在测 Decode")
	}
}

func TestReportWriteJSONIsMachineReadable(t *testing.T) {
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		Path:          "final.mp4",
		OK:            false,
		Problems:      []string{"分辨率不符：期望 1080x1440，实测 720x1280"},
		Media:         Media{DurationSeconds: 1.5, Video: &VideoStream{WidthPx: 720, HeightPx: 1280}},
	}
	path := filepath.Join(t.TempDir(), "nested", "video-report.json")
	if err := report.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version": 1`, `"ok": false`, `"width_px": 720`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("报告里缺少 %s：\n%s", want, body)
		}
	}
}
