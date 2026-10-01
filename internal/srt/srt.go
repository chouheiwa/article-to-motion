// Package srt 读取字幕的时间信息：整体跨度（ReadSpan）与逐条时间码（ReadCues）。
//
// 跨度用来核对镜头拆分有没有漏掉或重复覆盖时间轴；逐条时间码用来与实际语音
// 比对。两者都不做样式、位置等 SRT 扩展语法的解析。
package srt

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// timecodeLine 匹配 "00:00:01,234 --> 00:00:05,678"。
//
// 分隔符同时接受逗号和点号：规范写逗号，但不少工具（尤其是从 VTT 转过来的）
// 产出点号，一律拒绝会让合法字幕被判成格式错误。
var timecodeLine = regexp.MustCompile(
	`^\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})`)

// Span 是一份字幕的时间跨度。
type Span struct {
	// StartSeconds 是第一条字幕的开始时间。
	StartSeconds float64
	// EndSeconds 是最后一条字幕的结束时间。
	EndSeconds float64
	// Cues 是解析到的字幕条数。
	Cues int
}

// Seconds 返回跨度长度，即镜头总时长应当匹配的目标值。
func (s Span) Seconds() float64 { return s.EndSeconds - s.StartSeconds }

// ReadSpan 从 SRT 文件读出时间跨度。
//
// 取的是所有时间码里的最小开始与最大结束，而不是第一行和最后一行：
// 字幕条目乱序或时间重叠时，按出现顺序取会得到一个偏小甚至为负的跨度。
func ReadSpan(path string) (Span, error) {
	file, err := os.Open(path)
	if err != nil {
		return Span{}, fmt.Errorf("无法读取字幕：%w", err)
	}
	defer file.Close()

	var span Span
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		match := timecodeLine.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		start, err := clockSeconds(match[1], match[2], match[3], match[4])
		if err != nil {
			return Span{}, err
		}
		end, err := clockSeconds(match[5], match[6], match[7], match[8])
		if err != nil {
			return Span{}, err
		}
		if span.Cues == 0 || start < span.StartSeconds {
			span.StartSeconds = start
		}
		if span.Cues == 0 || end > span.EndSeconds {
			span.EndSeconds = end
		}
		span.Cues++
	}
	if err := scanner.Err(); err != nil {
		return Span{}, fmt.Errorf("读取字幕失败：%w", err)
	}
	if span.Cues == 0 {
		return Span{}, fmt.Errorf("字幕里没有任何时间码：%s", path)
	}
	if span.Seconds() <= 0 {
		return Span{}, fmt.Errorf("字幕跨度非正：开始 %.3f 秒、结束 %.3f 秒", span.StartSeconds, span.EndSeconds)
	}
	return span, nil
}

// clockSeconds 把时分秒毫秒转成秒。
func clockSeconds(hours, minutes, seconds, millis string) (float64, error) {
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(minutes)
	s, errS := strconv.Atoi(seconds)
	// 毫秒位数不足时要右补零而不是左补：",5" 是 500 毫秒，不是 5 毫秒。
	ms, errMS := strconv.Atoi(millis + strings.Repeat("0", 3-len(millis)))
	if errH != nil || errM != nil || errS != nil || errMS != nil {
		return 0, fmt.Errorf("时间码解析失败：%s:%s:%s,%s", hours, minutes, seconds, millis)
	}
	if m > 59 || s > 59 {
		return 0, fmt.Errorf("时间码的分或秒超出范围：%s:%s:%s,%s", hours, minutes, seconds, millis)
	}
	total := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(s)*time.Second + time.Duration(ms)*time.Millisecond
	return total.Seconds(), nil
}

// Cue 是一条字幕。Index 是它在文件中的出现顺序（从 1 开始），不取文件里写的
// 序号：那一行常被手工编辑弄乱，报错时按出现顺序定位才可靠。
type Cue struct {
	Index        int     `json:"index"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Text         string  `json:"text"`
}

// ReadCues 按出现顺序读出全部字幕条目。时间码之后、空行之前的非空行是正文。
func ReadCues(path string) ([]Cue, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取字幕：%w", err)
	}
	defer file.Close()

	var cues []Cue
	var current *Cue
	var text []string
	flush := func() {
		if current != nil {
			current.Text = strings.Join(text, "\n")
			cues = append(cues, *current)
		}
		current, text = nil, nil
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\ufeff")
		if match := timecodeLine.FindStringSubmatch(line); match != nil {
			flush()
			start, err := clockSeconds(match[1], match[2], match[3], match[4])
			if err != nil {
				return nil, err
			}
			end, err := clockSeconds(match[5], match[6], match[7], match[8])
			if err != nil {
				return nil, err
			}
			// 零时长条目照收：对齐器常给纯标点条目输出 start == end，判对错是
			// 调用方的事；结束早于开始则是文件本身坏了。
			if end < start {
				return nil, fmt.Errorf("第 %d 条字幕的结束时间早于开始时间：%s", len(cues)+1, strings.TrimSpace(line))
			}
			current = &Cue{Index: len(cues) + 1, StartSeconds: start, EndSeconds: end}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if current == nil {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		text = append(text, trimmed)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取字幕失败：%w", err)
	}
	flush()
	if len(cues) == 0 {
		return nil, fmt.Errorf("字幕里没有任何时间码：%s", path)
	}
	return cues, nil
}
