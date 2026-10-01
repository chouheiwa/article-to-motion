package voicecheck

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

// levels 按 10ms 一窗构造电平：loud 区间内 -20 dBFS，其余 -90。
func levels(total float64, loud ...Segment) []float64 {
	out := make([]float64, int(math.Round(total/0.01)))
	for i := range out {
		at := float64(i) * 0.01
		out[i] = -90
		for _, segment := range loud {
			if at >= segment.Start-1e-9 && at < segment.End-1e-9 {
				out[i] = -20
			}
		}
	}
	return out
}

func cue(index int, start, end float64) srt.Cue {
	return srt.Cue{Index: index, StartSeconds: start, EndSeconds: end, Text: "句子"}
}

func TestSpeechMergesShortGapsAndDropsClicks(t *testing.T) {
	// 1.00–1.50 与 1.55–2.00 之间 50ms 间隙（字间），应合并；3.00–3.02 是咔哒声。
	speech, threshold := Speech(levels(4, Segment{1, 1.5}, Segment{1.55, 2}, Segment{3, 3.02}), Options{})
	if len(speech) != 1 || speech[0] != (Segment{1, 2}) {
		t.Fatalf("有声区间不符：%v", speech)
	}
	if threshold != -55 {
		t.Errorf("阈值应为峰值 -20 减 35 dB，实际 %v", threshold)
	}
}

func TestAlignedCuesPass(t *testing.T) {
	speech, threshold := Speech(levels(6, Segment{0.5, 2}, Segment{2.5, 4.5}), Options{})
	report := Check([]srt.Cue{cue(1, 0.5, 2.05), cue(2, 2.45, 4.5)}, speech, threshold, Options{})
	if !report.OK || len(report.Hints) != 0 {
		t.Fatalf("对齐的字幕不应有问题：%+v", report)
	}
	if report.MedianStartDelta == nil || math.Abs(*report.MedianStartDelta) > 0.05 {
		t.Fatalf("中位偏移应接近 0：%v", report.MedianStartDelta)
	}
}

// TestSystematicEarlyOffsetIsCaught：记忆里记下的真实故障——TTS 自带 SRT 整体早 0.4 秒。
func TestSystematicEarlyOffsetIsCaught(t *testing.T) {
	speech, threshold := Speech(levels(8, Segment{1, 2.5}, Segment{3, 5}, Segment{5.6, 7}), Options{})
	report := Check([]srt.Cue{cue(1, 0.6, 2.1), cue(2, 2.6, 4.6), cue(3, 5.2, 6.6)}, speech, threshold, Options{})
	// 整体提前时每句末尾的 0.4 秒也失去覆盖：3 条「早」加 3 段「未覆盖」。
	if report.OK || len(report.Problems) != 6 || !strings.Contains(report.Problems[0], "早 0.400 秒") {
		t.Fatalf("每条都应报早 0.4 秒并报出末尾未覆盖：%v", report.Problems)
	}
	if report.MedianStartDelta == nil || *report.MedianStartDelta != -0.4 {
		t.Fatalf("中位偏移应为 -0.4：%v", report.MedianStartDelta)
	}
	if !strings.Contains(strings.Join(report.Hints, "\n"), "整体比声音早约 0.400 秒") {
		t.Fatalf("应提示整体校正：%v", report.Hints)
	}
}

// TestContinuousSpeechSplitAcrossCuesIsNotLate：一口气读完的长句被拆成两条字幕，
// 第二条开始时声音早已响起——这是正常拆分，不是字幕晚了，也不计入偏移中位数。
func TestContinuousSpeechSplitAcrossCuesIsNotLate(t *testing.T) {
	speech, threshold := Speech(levels(5, Segment{1, 4}), Options{})
	report := Check([]srt.Cue{cue(1, 1, 2.5), cue(2, 2.5, 4)}, speech, threshold, Options{})
	if !report.OK {
		t.Fatalf("连续发声拆成两条不应报错：%v", report.Problems)
	}
	if report.Cues[1].OwnsOnset || *report.MedianStartDelta != 0 {
		t.Fatalf("第二条不应拥有起点、不应计入中位数：%+v %v", report.Cues[1], *report.MedianStartDelta)
	}
}

func TestLateStartSilentCueAndUncoveredSpeech(t *testing.T) {
	speech, threshold := Speech(levels(10, Segment{1, 2}, Segment{3, 4}, Segment{6, 7}), Options{})
	report := Check([]srt.Cue{
		cue(1, 1.5, 2),   // 晚 0.5 秒开始，且 1.0–1.5 没字幕
		cue(2, 3, 4),     // 正常
		cue(3, 8.5, 9.5), // 落在静音里
	}, speech, threshold, Options{})
	joined := strings.Join(report.Problems, "\n")
	for _, want := range []string{"第 1 条字幕比声音晚 0.500 秒", "第 3 条字幕（8.500–9.500 秒）整段落在静音里", "1.000–1.500 秒有声音但没有字幕覆盖", "6.000–7.000 秒有声音但没有字幕覆盖"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少问题 %q：\n%s", want, joined)
		}
	}
}

// TestLingeringEndIsOnlyAHint：终稿字幕按可读单元延长停留是允许的。
func TestLingeringEndIsOnlyAHint(t *testing.T) {
	speech, threshold := Speech(levels(5, Segment{1, 2}), Options{})
	report := Check([]srt.Cue{cue(1, 1, 3)}, speech, threshold, Options{})
	if !report.OK || len(report.Hints) != 1 || !strings.Contains(report.Hints[0], "停留 1.000 秒") {
		t.Fatalf("结尾停留应只进 hints：%+v", report)
	}
}

// TestNoSpeechAtAll：数字静音文件每窗都是地板值，峰值也是地板值；
// 阈值只相对峰值的话，整段静音会被当成发声。
func TestNoSpeechAtAll(t *testing.T) {
	silent := make([]float64, 100)
	for i := range silent {
		silent[i] = -120
	}
	speech, threshold := Speech(silent, Options{})
	report := Check([]srt.Cue{cue(1, 0, 1)}, speech, threshold, Options{})
	if report.OK || report.Problems[0] != "音频里没有检测到任何发声" {
		t.Fatalf("全静音应报错：%+v", report)
	}
}

func TestMedianAndWriteJSON(t *testing.T) {
	if got := medianOf([]float64{0.3, -0.1, 0.1, 0.2}); got != 0.15 {
		t.Errorf("偶数个中位数 = %v", got)
	}
	speech, threshold := Speech(levels(3, Segment{1, 2}), Options{})
	report := Check([]srt.Cue{cue(1, 1, 2)}, speech, threshold, Options{})
	if err := report.WriteJSON(filepath.Join(t.TempDir(), "check.json")); err != nil {
		t.Fatal(err)
	}
}

// TestEarlyCueOverlappingPreviousTailIsCaught：审查报告的复现——第 2 条字幕提前
// 0.5 秒开始，开头擦到上一句的尾音；不能因为擦边就把上一句当成它的首段发声。
func TestEarlyCueOverlappingPreviousTailIsCaught(t *testing.T) {
	speech, threshold := Speech(levels(5, Segment{0, 2}, Segment{2.4, 4}), Options{})
	report := Check([]srt.Cue{cue(1, 0, 1.5), cue(2, 1.9, 4)}, speech, threshold, Options{})
	if report.OK || !strings.Contains(strings.Join(report.Problems, "\n"), "第 2 条字幕比声音早 0.500 秒") {
		t.Fatalf("擦边的提前字幕应被报出：%v", report.Problems)
	}
	if !report.Cues[1].OwnsOnset {
		t.Fatal("第 2 条应拥有自己的发声起点并计入中位数")
	}
}

func TestZeroLengthCueIsOnlyAHint(t *testing.T) {
	speech, threshold := Speech(levels(3, Segment{1, 2}), Options{})
	report := Check([]srt.Cue{cue(1, 1, 2), cue(2, 2.5, 2.5)}, speech, threshold, Options{})
	if !report.OK || len(report.Hints) != 1 || len(report.Cues) != 1 {
		t.Fatalf("零时长字幕应只进 hints：%+v", report)
	}
}
