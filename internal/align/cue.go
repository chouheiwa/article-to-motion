package align

import (
	"fmt"
	"math"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/reads"
)

// MaxCueUnits 是一条字幕的显示长度上限（阅读单位，口径同 reads.Units）。
// 句子超过它时在逗号、顿号处再切。
const MaxCueUnits = 32

// Cue 是 voice.srt 的一条：显示文本，时间来自其中朗读单元的起止。
type Cue struct {
	Index   int
	StartMs int64
	EndMs   int64
	Text    string
	// from/to 是 piece 区间，tok0/tok1 是朗读单元区间，都左闭右开。
	from, to   int
	tok0, tok1 int
}

func isSentenceEnd(s string) bool { return s != "" && strings.ContainsAny(s, "。！？!?；;") }
func isClauseEnd(s string) bool   { return s != "" && strings.ContainsAny(s, "，、,") }
func isCloser(s string) bool      { return s != "" && strings.ContainsAny(s, "”’」』）)】》\"'") }

// sentences 按句末标点与分段切句。句末标点后紧跟的右引号、右括号归前一句。
func (s *Script) sentences() [][2]int {
	var out [][2]int
	start, pending := 0, false
	for i, p := range s.pieces {
		if i > start && p.para != s.pieces[i-1].para {
			out = append(out, [2]int{start, i})
			start, pending = i, false
		}
		if pending && !(p.plain && isCloser(p.display)) {
			out = append(out, [2]int{start, i})
			start, pending = i, false
		}
		if p.plain && isSentenceEnd(p.display) {
			pending = true
		}
	}
	if start < len(s.pieces) {
		out = append(out, [2]int{start, len(s.pieces)})
	}
	return out
}

// splitLong 把显示长度超限的句子在逗号、顿号处切开，尽量让每段贴近上限。
func (s *Script) splitLong(sentence [2]int) [][2]int {
	if reads.Units(s.displayText(sentence[0], sentence[1])) <= MaxCueUnits {
		return [][2]int{sentence}
	}
	var clauses [][2]int
	start := sentence[0]
	for i := sentence[0]; i < sentence[1]; i++ {
		if s.pieces[i].plain && isClauseEnd(s.pieces[i].display) {
			clauses = append(clauses, [2]int{start, i + 1})
			start = i + 1
		}
	}
	if start < sentence[1] {
		clauses = append(clauses, [2]int{start, sentence[1]})
	}
	var out [][2]int
	current := clauses[0]
	for _, clause := range clauses[1:] {
		if reads.Units(s.displayText(current[0], clause[1])) <= MaxCueUnits {
			current[1] = clause[1]
			continue
		}
		out = append(out, current)
		current = clause
	}
	return append(out, current)
}

// buildCues 切出字幕并定时。没有朗读单元的片段（纯标点、只显示不朗读）并进前一条。
func buildCues(s *Script, timed []TimedToken) []Cue {
	var ranges [][2]int
	for _, sentence := range s.sentences() {
		ranges = append(ranges, s.splitLong(sentence)...)
	}
	var cues []Cue
	for _, r := range ranges {
		tok0, tok1 := s.tokenRange(r[0], r[1])
		if tok1 == tok0 {
			if len(cues) > 0 {
				cues[len(cues)-1].to = r[1]
			} else if len(ranges) > 1 {
				// 开头的纯显示片段留给下一条：记成空 cue，下一条吸收它。
				cues = append(cues, Cue{from: r[0], to: r[1], tok0: -1})
			}
			continue
		}
		if len(cues) > 0 && cues[len(cues)-1].tok0 < 0 {
			cues[len(cues)-1].to, cues[len(cues)-1].tok0, cues[len(cues)-1].tok1 = r[1], tok0, tok1
			continue
		}
		cues = append(cues, Cue{from: r[0], to: r[1], tok0: tok0, tok1: tok1})
	}
	if len(cues) > 0 && cues[len(cues)-1].tok0 < 0 {
		cues = cues[:len(cues)-1]
	}
	// 只有不显示的朗读（<|嗯>）的一条没有字幕文字，SRT 写不出空条目：并进相邻的一条。
	for i := 0; i < len(cues) && len(cues) > 1; {
		if s.displayText(cues[i].from, cues[i].to) != "" {
			i++
			continue
		}
		if i > 0 {
			cues[i-1].to, cues[i-1].tok1 = cues[i].to, cues[i].tok1
		} else {
			cues[1].from, cues[1].tok0 = cues[0].from, cues[0].tok0
		}
		cues = append(cues[:i], cues[i+1:]...)
	}
	var previousEnd int64
	for i := range cues {
		c := &cues[i]
		c.Index = i + 1
		c.Text = s.displayText(c.from, c.to)
		start, end := math.Inf(1), math.Inf(-1)
		for _, t := range timed[c.tok0:c.tok1] {
			start, end = min(start, t.Start), max(end, t.End)
		}
		c.StartMs, c.EndMs = toMs(start), toMs(end)
		// 取整后可能与上一条重叠：把起点推到上一条终点，再保证至少 1 毫秒。
		if i > 0 && c.StartMs < previousEnd {
			c.StartMs = previousEnd
		}
		if c.EndMs <= c.StartMs {
			c.EndMs = c.StartMs + 1
		}
		previousEnd = c.EndMs
	}
	return cues
}

// cueOfPiece 返回包含该 piece 的字幕编号（从 1 开始），找不到返回 0。
func cueOfPiece(cues []Cue, piece int) int {
	for _, c := range cues {
		if piece >= c.from && piece < c.to {
			return c.Index
		}
	}
	return 0
}

func toMs(seconds float64) int64 { return int64(math.Round(seconds * 1000)) }

// FormatSRT 把字幕写成 SRT 文本，毫秒精度。
func FormatSRT(cues []Cue) string {
	var b strings.Builder
	for i, c := range cues {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n", c.Index, srtClock(c.StartMs), srtClock(c.EndMs), c.Text)
	}
	return b.String()
}

// WriteSRT 原子写出字幕文件。
func WriteSRT(path string, cues []Cue) error {
	return fsutil.AtomicWrite(path, []byte(FormatSRT(cues)), 0o644)
}

func srtClock(ms int64) string {
	h := ms / 3_600_000
	m := ms / 60_000 % 60
	sec := ms / 1000 % 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, sec, ms%1000)
}
