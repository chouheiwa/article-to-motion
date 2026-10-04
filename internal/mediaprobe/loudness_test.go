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

func TestParseLoudnormTakesLastJSONBlockAndFloorsSilence(t *testing.T) {
	log := "[Parsed_loudnorm_0 @ 0x1] \n{\n\t\"input_i\" : \"-inf\",\n\t\"input_tp\" : \"-inf\",\n\t\"input_lra\" : \"0.00\"\n}\nsize=N/A time=00:00:02.00\n"
	got, err := parseLoudnorm("Metadata: {encoder}\n" + log)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntegratedLUFS != SilenceFloorDB || got.TruePeakDBTP != SilenceFloorDB || got.RangeLU != 0 {
		t.Fatalf("静音应记为地板值：%+v", got)
	}
	for _, bad := range []string{"no json here", `{"input_i":"loud","input_tp":"-1","input_lra":"1"}`, `{"input_i":"nan","input_tp":"-1","input_lra":"1"}`} {
		if _, err := parseLoudnorm(bad); err == nil {
			t.Fatalf("应当拒绝：%s", bad)
		}
	}
}

// TestMeasureProgramLoudnessOnEncodedTone：编码后的正弦能量稳定，积分响度与
// 真峰都应落在有限值上，且真峰高于积分响度。
func TestMeasureProgramLoudnessOnEncodedTone(t *testing.T) {
	tc := toolchain(t)
	path := filepath.Join(t.TempDir(), "tone.m4a")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=3", "-c:a", "aac", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成音频失败：%v\n%s", err, output)
	}
	got, err := tc.MeasureProgramLoudness(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntegratedLUFS < -30 || got.IntegratedLUFS > -15 || got.TruePeakDBTP <= got.IntegratedLUFS || got.TruePeakDBTP > 0 {
		t.Fatalf("实测值不合理：%+v", got)
	}
}
