package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeFilm 拼出带音轨的成片：视频 videoSeconds 秒，音频 audioSeconds 秒。
func makeFilm(t *testing.T, path string, videoSeconds, audioSeconds string) {
	t.Helper()
	ffmpeg := requireFFmpeg(t)
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration="+videoSeconds,
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+audioSeconds,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成成片失败：%v\n%s", err, output)
	}
}

func TestReviewCommandWritesSheetsAndReport(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	scenes := filepath.Join(root, "scenes")
	makeSceneDir(t, scenes, "scene-001", 1, "testsrc=size=320x240:rate=30:duration=1")
	makeSceneDir(t, scenes, "scene-002", 1, "testsrc=size=320x240:rate=30:duration=1")
	film := filepath.Join(root, "final.mp4")
	makeFilm(t, film, "2", "2")
	outDir := filepath.Join(root, "production", "review")

	var out, errOut bytes.Buffer
	if code := Execute([]string{"review", film, "--scenes", scenes, "--fps", "30", "--out", outDir, "--thumb-width", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("复查应当通过，实际 %d：%s", code, errOut.String())
	}
	body, err := os.ReadFile(filepath.Join(outDir, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		OK             bool `json:"ok"`
		ExpectedFrames int  `json:"expected_frames"`
		Sheets         []struct {
			Image string `json:"image"`
			Tiles []any  `json:"tiles"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.ExpectedFrames != 60 || len(report.Sheets) != 1 || len(report.Sheets[0].Tiles) != 6 {
		t.Fatalf("报告不符：%s", body)
	}
	if _, err := os.Stat(report.Sheets[0].Image); err != nil {
		t.Fatalf("联系表没有生成：%v", err)
	}
}

// TestReviewCommandFailsOnMissingScene：镜头目录多出一镜，成片帧数对不上。
func TestReviewCommandFailsOnFrameMismatch(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	scenes := filepath.Join(root, "scenes")
	for _, id := range []string{"scene-001", "scene-002", "scene-003"} {
		makeSceneDir(t, scenes, id, 1, "testsrc=size=320x240:rate=30:duration=1")
	}
	film := filepath.Join(root, "final.mp4")
	makeFilm(t, film, "2", "2")
	var out, errOut bytes.Buffer
	code := Execute([]string{"review", film, "--scenes", scenes, "--fps", "30", "--out", filepath.Join(root, "review")}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "镜头帧数之和 90") {
		t.Fatalf("帧数不符应退出 1 并说明原因，实际 %d：%s", code, errOut.String())
	}
}

func TestReviewCommandRequiresOutAndFPS(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Execute([]string{"review", "x.mp4", "--fps", "30"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--out") {
		t.Errorf("缺 --out 应失败并点名：%d %s", code, errOut.String())
	}
	errOut.Reset()
	if code := Execute([]string{"review", "x.mp4", "--out", t.TempDir(), "--project-root", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--fps") {
		t.Errorf("无法确定帧率时应提示 --fps：%d %s", code, errOut.String())
	}
}

func TestSceneLintCommand(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	os.WriteFile(index, []byte("<script>\nconst x = Math.random();\n</script>"), 0o644)
	report := filepath.Join(dir, "lint.json")
	var out, errOut bytes.Buffer
	if code := Execute([]string{"scene", "lint", dir, "--report-json", report}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "index.html:2 [random]") {
		t.Fatalf("违规源码应退出 1 并定位到行：%d %s", code, errOut.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("应写出报告：%v", err)
	}
	os.WriteFile(index, []byte("<script>const x = rng(); // am-allow-nondeterministic: demo\nrequestAnimationFrame(f); // am-allow-nondeterministic: 预览\n</script>"), 0o644)
	out.Reset()
	errOut.Reset()
	if code := Execute([]string{"scene", "lint", dir}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "已豁免") {
		t.Fatalf("豁免后应通过并列出豁免项：%d %s %s", code, out.String(), errOut.String())
	}
}

func TestSceneFramesStripAndCrop(t *testing.T) {
	requireFFmpeg(t)
	dir := makeSceneDir(t, t.TempDir(), "scene-001", 2, "testsrc=size=1080x1440:rate=30:duration=2")
	var out, errOut bytes.Buffer
	if code := Execute([]string{"scene", "frames", dir, "--strip", "0.5:0.8", "--cols", "5", "--thumb-width", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("条带应成功：%d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "（10 帧，5 列") {
		t.Errorf("0.5 到 0.8 秒应为第 15–24 帧共 10 帧：%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "visual-qc", "strip-0_5-0_8.png")); err != nil {
		t.Errorf("条带文件没有生成：%v", err)
	}
	out.Reset()
	if code := Execute([]string{"scene", "frames", dir, "--at", "0", "--crop", "0,0,200,100"}, &out, &errOut); code != 0 {
		t.Fatalf("裁剪抽帧应成功：%d %s", code, errOut.String())
	}
	for _, args := range [][]string{
		{"--strip", "0:2", "--at", "0"}, // 互斥
		{"--strip", "0:end"},            // 2 秒片子的 0:end 是 60 帧，在上限内，合法
		{"--crop", "1000,0,200,100"},    // 超出画面
		{"--crop", "1,2,3"},             // 格式错误
		{"--strip", "1.5:0.5"},          // 倒序
	} {
		errOut.Reset()
		code := Execute(append([]string{"scene", "frames", dir}, args...), &out, &errOut)
		if args[1] == "0:end" {
			if code != 0 {
				t.Errorf("0:end 共 60 帧应成功：%s", errOut.String())
			}
			continue
		}
		if code != 1 {
			t.Errorf("%v 应失败", args)
		}
	}
}

func TestParseStripCapsLength(t *testing.T) {
	if _, err := parseStrip("0:5", 10, 30); err == nil || !strings.Contains(err.Error(), "上限 90") {
		t.Fatalf("超过 90 帧应报错：%v", err)
	}
}

// 项目里有 frame.md 时按它的 audio 目标比对；正弦测试音约 -21 LUFS，
// 偏离 -16±1 只进提示，退出码仍为 0，实测值写进报告。
func TestReviewCommandReportsLoudnessAgainstFrameTargets(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	frame := "---\nstyle_id: x\ncanvas:\n  width_px: 320\n  height_px: 240\n  fps: 30\naudio:\n  final_integrated_lufs: -16\n  final_lufs_tolerance: 1\n  true_peak_max_dbtp: -1\n---\n"
	os.WriteFile(filepath.Join(root, "frame.md"), []byte(frame), 0o644)
	film := filepath.Join(root, "final.mp4")
	makeFilm(t, film, "2", "2")
	outDir := filepath.Join(root, "review")
	var out, errOut bytes.Buffer
	if code := Execute([]string{"review", film, "--project-root", root, "--out", outDir, "--thumb-width", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("响度偏离不应让复查失败：%d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "积分响度") {
		t.Errorf("应提示积分响度偏离：%s", out.String())
	}
	body, _ := os.ReadFile(filepath.Join(outDir, "review.json"))
	if !strings.Contains(string(body), `"integrated_lufs"`) {
		t.Errorf("报告应包含实测响度：%s", body)
	}

	// frame.md 读不出目标时报错，不静默跳过。
	os.WriteFile(filepath.Join(root, "frame.md"), []byte("---\nstyle_id: x\ncanvas:\n  width_px: 320\n  height_px: 240\n  fps: 30\n---\n"), 0o644)
	errOut.Reset()
	if code := Execute([]string{"review", film, "--project-root", root, "--out", outDir}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "响度目标") {
		t.Errorf("缺响度目标应报错：%d %s", code, errOut.String())
	}
}
