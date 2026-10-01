package srt

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcription.srt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSpanCoversFirstStartToLastEnd(t *testing.T) {
	path := write(t, `1
00:00:01,500 --> 00:00:04,333
第一句

2
00:00:04,333 --> 00:00:09,000
第二句
`)
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatal(err)
	}
	if span.Cues != 2 {
		t.Errorf("字幕条数 = %d，期望 2", span.Cues)
	}
	if math.Abs(span.StartSeconds-1.5) > 1e-9 {
		t.Errorf("开始 = %v，期望 1.5", span.StartSeconds)
	}
	if math.Abs(span.EndSeconds-9.0) > 1e-9 {
		t.Errorf("结束 = %v，期望 9", span.EndSeconds)
	}
	if math.Abs(span.Seconds()-7.5) > 1e-9 {
		t.Errorf("跨度 = %v，期望 7.5", span.Seconds())
	}
}

// TestReadSpanUsesMinMaxNotFileOrder：条目乱序时按出现顺序取首尾
// 会得到偏小甚至为负的跨度。取最小开始与最大结束才是对的。
func TestReadSpanUsesMinMaxNotFileOrder(t *testing.T) {
	path := write(t, `1
00:00:10,000 --> 00:00:12,000
后面的句子写在了前面

2
00:00:01,000 --> 00:00:03,000
前面的句子写在了后面
`)
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(span.Seconds()-11.0) > 1e-9 {
		t.Errorf("跨度 = %v，期望 11（1.0 到 12.0）", span.Seconds())
	}
}

func TestReadSpanAcceptsDotAsMillisecondSeparator(t *testing.T) {
	path := write(t, "1\n00:00:00.000 --> 00:00:02.500\n从 VTT 转过来的\n")
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatalf("点号分隔符应当被接受：%v", err)
	}
	if math.Abs(span.Seconds()-2.5) > 1e-9 {
		t.Errorf("跨度 = %v，期望 2.5", span.Seconds())
	}
}

// TestShortMillisecondsPadRight：",5" 是 500 毫秒，不是 5 毫秒。
// 左补零会把跨度算错两个数量级。
func TestShortMillisecondsPadRight(t *testing.T) {
	path := write(t, "1\n00:00:00,0 --> 00:00:01,5\n短毫秒\n")
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(span.Seconds()-1.5) > 1e-9 {
		t.Errorf("跨度 = %v，期望 1.5（毫秒位要右补零）", span.Seconds())
	}
}

func TestReadSpanHandlesHoursAndLongFiles(t *testing.T) {
	path := write(t, "1\n01:02:03,004 --> 01:02:04,000\n一小时后\n")
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatal(err)
	}
	want := 3723.004
	if math.Abs(span.StartSeconds-want) > 1e-6 {
		t.Errorf("开始 = %v，期望 %v", span.StartSeconds, want)
	}
}

func TestReadSpanRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"没有时间码":  "1\n就是一段文字\n",
		"空文件":    "",
		"跨度为零":   "1\n00:00:02,000 --> 00:00:02,000\n零长度\n",
		"分钟超出范围": "1\n00:99:00,000 --> 00:99:01,000\n非法\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadSpan(write(t, body)); err == nil {
				t.Errorf("%s 应当报错", name)
			}
		})
	}
	if _, err := ReadSpan(filepath.Join(t.TempDir(), "缺失.srt")); err == nil {
		t.Error("文件不存在应当报错")
	}
}

// TestReadSpanIgnoresNonTimecodeLines：序号、文本和空行都不该干扰解析，
// 文本里出现的类时间码字样也不该被当成时间码。
func TestReadSpanIgnoresNonTimecodeLines(t *testing.T) {
	path := write(t, `1
00:00:00,000 --> 00:00:05,000
这句台词里提到了 00:10:00,000 --> 00:20:00,000 这样的写法

2
00:00:05,000 --> 00:00:06,000
结束
`)
	span, err := ReadSpan(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(span.Seconds()-6.0) > 1e-9 {
		t.Errorf("跨度 = %v，期望 6——正文里的时间码字样不该参与计算", span.Seconds())
	}
}

func TestReadCuesParsesTextAndKeepsFileOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.srt")
	body := "\ufeff7\n00:00:00,500 --> 00:00:02,000\n第一行\n第二行\n\n3\n00:00:02.3 --> 00:00:04,100\nsecond\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cues, err := ReadCues(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 {
		t.Fatalf("应解析出 2 条，实际 %d", len(cues))
	}
	if cues[0].Index != 1 || cues[0].StartSeconds != 0.5 || cues[0].EndSeconds != 2 || cues[0].Text != "第一行\n第二行" {
		t.Errorf("第 1 条不符：%+v", cues[0])
	}
	if cues[1].Index != 2 || cues[1].StartSeconds != 2.3 || cues[1].Text != "second" {
		t.Errorf("第 2 条不符（序号按出现顺序，毫秒右补零）：%+v", cues[1])
	}
}

func TestReadCuesRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty.srt":    "no timecodes here\n",
		"reverse.srt":  "1\n00:00:02,000 --> 00:00:01,000\nx\n",
		"badclock.srt": "1\n00:61:00,000 --> 00:62:00,000\nx\n",
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := ReadCues(path); err == nil {
			t.Errorf("%s 应报错", name)
		}
	}
	if _, err := ReadCues(filepath.Join(dir, "missing.srt")); err == nil {
		t.Error("文件不存在应报错")
	}
}

func TestReadCuesKeepsZeroLengthCues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "align.srt")
	os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:01,000\n，\n"), 0o644)
	cues, err := ReadCues(path)
	if err != nil || len(cues) != 1 || cues[0].StartSeconds != cues[0].EndSeconds {
		t.Fatalf("零时长条目应保留：%v %v", cues, err)
	}
}
