package beat

import (
	"context"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
)

// TestAnalyzeWithRealBeatThis 真跑一遍分析。需要装好 beat-this 的 Python：
//
//	AM_TEST_BEAT_PYTHON=/path/to/venv/bin/python go test ./internal/beat/ -run RealBeatThis
//
// 素材是 120 BPM 的合成鼓点（每 0.5 秒一下底鼓），只断言普通拍的间隔——
// 这么单调的信号里重拍落在哪一拍本来就没有定论。
func TestAnalyzeWithRealBeatThis(t *testing.T) {
	python := os.Getenv("AM_TEST_BEAT_PYTHON")
	if python == "" {
		t.Skip("未设置 AM_TEST_BEAT_PYTHON，跳过真实节拍分析")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("本机没有 ffmpeg")
	}
	root := t.TempDir()
	config := "schema: 1\ncheckpoint: small0\npython: " + python + "\n"
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(t.TempDir(), "kick.wav")
	kick := "aevalsrc=sin(2*PI*(50+80*exp(-30*mod(t\\,0.5)))*mod(t\\,0.5))*exp(-12*mod(t\\,0.5)):s=44100:d=16"
	if output, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", kick, music).CombinedOutput(); err != nil {
		t.Fatalf("生成鼓点失败：%v\n%s", err, output)
	}
	media, err := mediaprobe.New(envutil.EnvMap())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(root, music, media, false); err != nil {
		t.Fatal(err)
	}
	grid, err := Analyze(context.Background(), root, envutil.EnvMap(), media, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if s := grid.Summarize(); math.Abs(s.BPM-120) > 2 || len(grid.Beats) < 25 {
		t.Fatalf("应测出约 120 BPM：%s，%d 拍", s, len(grid.Beats))
	}
	if _, err := os.Stat(filepath.Join(root, ClickFile)); err != nil {
		t.Fatalf("应生成试听轨：%v", err)
	}
	if _, err := ReadDraft(filepath.Join(root, DraftFile)); err != nil {
		t.Fatalf("应写出草稿网格：%v", err)
	}
}
