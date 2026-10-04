package mediaprobe

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestClickTrackMarksDownbeatsLouder：静音上叠咔嗒，重拍处的峰值必须高于普通拍，
// 且时长与原音乐一致（48kHz 双声道 16 位）。
func TestClickTrackMarksDownbeatsLouder(t *testing.T) {
	tc := toolchain(t)
	dir := t.TempDir()
	music := filepath.Join(dir, "silence.wav")
	cmd := exec.Command(ffmpegFor(t), "-v", "error", "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono:d=2", music)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成音频失败：%v\n%s", err, output)
	}
	dest := filepath.Join(dir, "click.wav")
	if err := tc.ClickTrack(context.Background(), music, dest, []float64{0.5, 1.0}, []float64{0.5}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(body[:4]) != "RIFF" || binary.LittleEndian.Uint32(body[24:]) != 48000 || binary.LittleEndian.Uint16(body[22:]) != 2 {
		t.Fatal("WAV 头不符")
	}
	frames := (len(body) - 44) / 4
	if frames < 95900 || frames > 96100 {
		t.Fatalf("帧数 %d 与 2 秒不符", frames)
	}
	peak := func(at float64) float64 {
		start := int(at * 48000)
		best := 0.0
		for i := start; i < start+int(0.035*48000); i++ {
			best = math.Max(best, math.Abs(float64(int16(binary.LittleEndian.Uint16(body[44+4*i:])))))
		}
		return best / 32768
	}
	if down, normal, quiet := peak(0.5), peak(1.0), peak(1.5); down <= normal || normal < 0.1 || quiet > 0.001 {
		t.Fatalf("峰值不符：重拍 %.3f，普通拍 %.3f，无拍处 %.3f", down, normal, quiet)
	}
}
