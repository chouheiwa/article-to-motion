package align

import "strings"

// evToken 是对齐结果切出的一个匹配单元。一个条目里有多个单元时（例如一条里
// 是整句英文），按字符数比例分时间。
type evToken struct {
	Token
	start, end float64
	segment    int
}

func evidenceTokens(items []Item) []evToken {
	var out []evToken
	for _, item := range items {
		tokens := Tokenize(item.Text)
		total := 0
		for _, t := range tokens {
			total += len([]rune(t.Norm))
		}
		consumed := 0
		for _, t := range tokens {
			width := len([]rune(t.Norm))
			start := item.Start + (item.End-item.Start)*float64(consumed)/float64(total)
			consumed += width
			end := item.Start + (item.End-item.Start)*float64(consumed)/float64(total)
			out = append(out, evToken{Token: t, start: start, end: end, segment: item.Segment})
		}
	}
	return out
}

// TimedToken 是带时间与匹配标签的终稿朗读单元。
type TimedToken struct {
	ScriptToken
	Label string
	// Heard 是同组对齐单元的文本，供报告对照。
	Heard      string
	Start, End float64
	// Interpolated 表示时间不是测出来的，而是在前后已定时单元之间按字数插值。
	Interpolated bool
}

type window struct {
	start, end float64
	ok         bool
}

// locate 按匹配路径给每个朗读单元定时，没配上的单元插值补齐。
func locate(source []ScriptToken, evidence []evToken, groups []group) []TimedToken {
	timed := make([]TimedToken, len(source))
	located := make([]window, len(source))
	for _, g := range groups {
		heard := make([]string, 0, g.e1-g.e0)
		for _, e := range evidence[g.e0:g.e1] {
			heard = append(heard, e.Text)
		}
		for i := g.s0; i < g.s1; i++ {
			timed[i] = TimedToken{ScriptToken: source[i], Label: g.label, Heard: strings.Join(heard, " ")}
		}
		if g.s1 > g.s0 && g.e1 > g.e0 {
			locateGroup(g, source, evidence, located)
		}
	}
	startBound, endBound := 0.0, 0.0
	if len(evidence) > 0 {
		startBound, endBound = evidence[0].start, evidence[0].end
		for _, e := range evidence {
			startBound = min(startBound, e.start)
			endBound = max(endBound, e.end)
		}
	}
	fillMissing(located, source, startBound, endBound, timed)
	for i := range timed {
		timed[i].Start, timed[i].End = located[i].start, located[i].end
	}
	return timed
}

// locateGroup 在一个分组内做字符级对齐：每个朗读单元取它配上的对齐字符的起止，
// 首单元的起点与末单元的终点贴到分组窗口的边上。
func locateGroup(g group, source []ScriptToken, evidence []evToken, located []window) {
	type timedChar struct{ start, end float64 }
	var evChars []rune
	var evTimes []timedChar
	win := window{start: evidence[g.e0].start, end: evidence[g.e0].end, ok: true}
	for _, e := range evidence[g.e0:g.e1] {
		win.start, win.end = min(win.start, e.start), max(win.end, e.end)
		runes := []rune(e.Norm)
		for k, r := range runes {
			step := (e.end - e.start) / float64(len(runes))
			evChars = append(evChars, r)
			evTimes = append(evTimes, timedChar{e.start + step*float64(k), e.start + step*float64(k+1)})
		}
	}
	if g.s1-g.s0 == 1 {
		located[g.s0] = win
		return
	}
	var srcChars []rune
	var owner []int
	for i := g.s0; i < g.s1; i++ {
		for _, r := range source[i].Norm {
			srcChars = append(srcChars, r)
			owner = append(owner, i)
		}
	}
	for _, p := range alignCharacters(srcChars, evChars) {
		i := owner[p.source]
		t := evTimes[p.evidence]
		if !located[i].ok {
			located[i] = window{start: t.start, end: t.end, ok: true}
		} else {
			located[i].end = max(located[i].end, t.end)
		}
	}
	if located[g.s0].ok {
		located[g.s0].start = win.start
	}
	if located[g.s1-1].ok {
		located[g.s1-1].end = win.end
	}
}

// fillMissing 把没定时的连续单元铺在左右已定时邻居之间的空隙里，按字数分配；
// 开头和结尾的缺口以整段对齐结果的起止为界。
func fillMissing(values []window, source []ScriptToken, startBound, endBound float64, timed []TimedToken) {
	for cursor := 0; cursor < len(values); {
		if values[cursor].ok {
			cursor++
			continue
		}
		runStart := cursor
		for cursor < len(values) && !values[cursor].ok {
			cursor++
		}
		runEnd := cursor
		left, right := startBound, endBound
		if runStart > 0 {
			left = values[runStart-1].end
		}
		if runEnd < len(values) {
			right = values[runEnd].start
		}
		right = max(left, right)
		total := 0
		for i := runStart; i < runEnd; i++ {
			total += max(1, len([]rune(source[i].Norm)))
		}
		consumed := 0
		for i := runStart; i < runEnd; i++ {
			start := left + (right-left)*float64(consumed)/float64(total)
			consumed += max(1, len([]rune(source[i].Norm)))
			end := left + (right-left)*float64(consumed)/float64(total)
			values[i] = window{start: start, end: end, ok: true}
			timed[i].Interpolated = true
		}
	}
}
