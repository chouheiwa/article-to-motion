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
