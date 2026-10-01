package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeVoice 生成「0.5 秒静音 + 1 秒声音 + 0.5 秒静音 + 1 秒声音」的配音。
func makeVoice(t *testing.T, path string) {
	t.Helper()
	ffmpeg := requireFFmpeg(t)
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=0.5",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=1",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=0.5",
		"-f", "lavfi", "-i", "sine=frequency=500:sample_rate=48000:duration=1",
		"-filter_complex", "[0][1][2][3]concat=n=4:v=0:a=1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成配音失败：%v\n%s", err, output)
	}
}

func TestAudioCheckCommand(t *testing.T) {
	dir := t.TempDir()
	voice := filepath.Join(dir, "voice.wav")
	makeVoice(t, voice)
	aligned := filepath.Join(dir, "aligned.srt")
	os.WriteFile(aligned, []byte("1\n00:00:00,500 --> 00:00:01,500\n第一句\n\n2\n00:00:02,000 --> 00:00:03,000\n第二句\n"), 0o644)
	var out, errOut bytes.Buffer
	if code := Execute([]string{"audio", "check", "--audio", voice, "--srt", aligned}, &out, &errOut); code != 0 {
		t.Fatalf("对齐的字幕应通过，实际 %d：%s", code, errOut.String())
	}
	early := filepath.Join(dir, "early.srt")
	os.WriteFile(early, []byte("1\n00:00:00,100 --> 00:00:01,100\n第一句\n\n2\n00:00:01,600 --> 00:00:02,600\n第二句\n"), 0o644)
	report := filepath.Join(dir, "check.json")
	out.Reset()
	errOut.Reset()
	code := Execute([]string{"audio", "check", "--audio", voice, "--srt", early, "--report-json", report}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "第 1 条字幕比声音早 0.400 秒") {
		t.Fatalf("整体早 0.4 秒应被拦下，实际 %d：%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "-0.400") {
		t.Errorf("应打印中位偏差：%s", out.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Errorf("应写出报告：%v", err)
	}
}

func TestAudioCheckRequiresInputs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Execute([]string{"audio", "check", "--audio", "x.wav"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--srt") {
		t.Errorf("缺 --srt 应失败并点名：%d %s", code, errOut.String())
	}
}

func TestAudioCheckRejectsNonPositiveOptions(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Execute([]string{"audio", "check", "--audio", "x.wav", "--srt", "x.srt", "--tolerance", "0"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "--tolerance") {
		t.Errorf("容差为 0 应报错而不是悄悄换成默认值：%d %s", code, errOut.String())
	}
}
