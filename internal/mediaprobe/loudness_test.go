package mediaprobe

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLoudnessSeparatesToneFromSilence：0.5 秒静音 + 0.5 秒正弦 + 0.5 秒静音，
// 10ms 一窗共 150 窗；静音段必须落在地板附近。ffmpeg sine 默认振幅 1/8，
// 理论 RMS = 0.125/√2，即 -21.07 dBFS。
func TestLoudnessSeparatesToneFromSilence(t *testing.T) {
	tc := toolchain(t)
	path := filepath.Join(t.TempDir(), "tone.wav")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono:d=0.5",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=0.5",
		"-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono:d=0.5",
		"-filter_complex", "[0][1][2]concat=n=3:v=0:a=1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成音频失败：%v\n%s", err, output)
	}
	levels, err := tc.Loudness(path, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) != 150 {
		t.Fatalf("窗口数 = %d，期望 150", len(levels))
	}
	if levels[20] > -90 || levels[130] > -90 {
		t.Errorf("静音段电平过高：%.1f / %.1f", levels[20], levels[130])
	}
	if levels[75] < -21.6 || levels[75] > -20.6 {
		t.Errorf("正弦段电平 %.1f dBFS 不在预期范围", levels[75])
	}
}

func TestLoudnessRejectsBadWindowAndInput(t *testing.T) {
	tc := toolchain(t)
	if _, err := tc.Loudness("x.wav", 0); err == nil {
		t.Error("窗口为 0 应报错")
	}
	if _, err := tc.Loudness("x.wav", 1e-9); err == nil {
		t.Error("窗口短于一个采样应报错")
	}
	if _, err := tc.Loudness(filepath.Join(t.TempDir(), "missing.wav"), 0.01); err == nil {
		t.Error("文件不存在应报错")
	}
	if got := rmsToDB(0); got != SilenceFloorDB {
		t.Errorf("rmsToDB(0) = %v", got)
	}
}
