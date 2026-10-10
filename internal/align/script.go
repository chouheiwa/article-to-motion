package align

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// 终稿标记语法（production/script-final.md）：
//
//	<显示|朗读>         显示文本与朗读文本不同：<2024年|二零二四年>、<API|A P I>
//	<|嗯>               只朗读、不显示
//	<显示|>             只显示、不朗读（没有时间，随所在字幕）
//	@{id}……@{/id}       受保护短语，进 phrase-timeline.json；可以嵌套，不能交叉
//	# 开头的行          标题，忽略
//	空行                分段
//	\< \> \@ \| \\      转义
//
// 同一段内的换行不产生文字；两侧都是字母数字时补一个空格，免得两个英文词粘成一个。

// phraseIDPattern 是短语 id 的合法形式。
var phraseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// piece 是终稿的最小显示单位：一个普通字符，或一个 <显示|朗读> 标记。
type piece struct {
	display, spoken string
	line, col       int
	para            int
	// plain 表示来自普通文本（显示即朗读），只有它参与断句判断。
	plain bool
	// softBreak 是段内换行留下的占位，解析结束后决定是否变成空格。
	softBreak bool
}

// ScriptToken 是终稿朗读侧的一个匹配单元，记着它落在哪几个 piece 上。
type ScriptToken struct {
	Token
	firstPiece, lastPiece int
	Line, Col             int
}

// PhraseSpan 是一个受保护短语在终稿里的位置，piece 下标左闭右开。
type PhraseSpan struct {
	ID                   string
	startPiece, endPiece int
	Line, Col            int
	// Order 是按开始位置排出的出现顺序，从 1 开始。
	Order int
}

// Script 是解析后的终稿。
type Script struct {
	pieces  []piece
	Tokens  []ScriptToken
	Phrases []PhraseSpan
	// pieceToken 是每个 piece 上第一个开始的朗读单元下标，没有时为 -1。
	pieceToken []int
}

// ParseError 带着出错位置（行、列都从 1 开始，列按字符计）。
type ParseError struct {
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("第 %d 行第 %d 列：%s", e.Line, e.Col, e.Msg)
}

// LoadScript 读取并解析终稿文件。
func LoadScript(path string) (*Script, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取终稿：%w", err)
	}
	script, err := ParseScript(string(body))
	if err != nil {
		return nil, fmt.Errorf("%s %w", path, err)
	}
	return script, nil
}

type openPhrase struct {
	id         string
	startPiece int
	line, col  int
}

// ParseScript 解析终稿标记。
func ParseScript(text string) (*Script, error) {
	s := &Script{}
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var stack []openPhrase
	seen := map[string]bool{}
	para := 0
	paraHasContent := false
	for index, raw := range lines {
		lineNo := index + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimLeft(line, " \t　")
		if strings.TrimSpace(trimmed) == "" || strings.HasPrefix(trimmed, "#") {
			if paraHasContent {
				para++
				paraHasContent = false
			}
			continue
		}
		if paraHasContent {
			s.pieces = append(s.pieces, piece{line: lineNo, col: 1, para: para, softBreak: true})
		}
		paraHasContent = true
		runes := []rune(line)
		add := func(p piece) {
			p.para = para
			s.pieces = append(s.pieces, p)
		}
		for i := 0; i < len(runes); {
			col := i + 1
			r := runes[i]
			switch {
			case r == '\\':
				if i+1 >= len(runes) || !isEscapable(runes[i+1]) {
					return nil, &ParseError{lineNo, col, `无效转义：只能转义 \< \> \@ \| \\`}
				}
				ch := string(runes[i+1])
				add(piece{display: ch, spoken: ch, line: lineNo, col: col, plain: true})
				i += 2
			case r == '<':
				display, spoken, next, err := parseAlternative(runes, i, lineNo)
				if err != nil {
					return nil, err
				}
				add(piece{display: display, spoken: spoken, line: lineNo, col: col})
				i = next
			case r == '@' && i+1 < len(runes) && runes[i+1] == '{':
				end := indexRune(runes, i+2, '}')
				if end < 0 {
					return nil, &ParseError{lineNo, col, "短语标记缺少 }"}
				}
				body := string(runes[i+2 : end])
				if strings.HasPrefix(body, "/") {
					id := body[1:]
					if len(stack) == 0 {
						return nil, &ParseError{lineNo, col, fmt.Sprintf("@{/%s} 没有对应的 @{%s}", id, id)}
					}
					top := stack[len(stack)-1]
					if top.id != id {
						return nil, &ParseError{lineNo, col, fmt.Sprintf("@{/%s} 与未闭合的 @{%s}（第 %d 行第 %d 列）交叉：短语只能嵌套，不能交叉", id, top.id, top.line, top.col)}
					}
					stack = stack[:len(stack)-1]
					if top.startPiece == len(s.pieces) {
						return nil, &ParseError{top.line, top.col, fmt.Sprintf("短语 %s 是空的", id)}
					}
					s.Phrases = append(s.Phrases, PhraseSpan{ID: id, startPiece: top.startPiece, endPiece: len(s.pieces), Line: top.line, Col: top.col})
				} else {
					if !phraseIDPattern.MatchString(body) {
						return nil, &ParseError{lineNo, col, fmt.Sprintf("短语 id %q 不合法：只能用小写字母、数字、_ 和 -，且以字母或数字开头", body)}
					}
					if seen[body] {
						return nil, &ParseError{lineNo, col, fmt.Sprintf("短语 id %s 重复", body)}
					}
					seen[body] = true
					stack = append(stack, openPhrase{id: body, startPiece: len(s.pieces), line: lineNo, col: col})
				}
				i = end + 1
			default:
				ch := string(r)
				add(piece{display: ch, spoken: ch, line: lineNo, col: col, plain: true})
				i++
			}
		}
	}
	if len(stack) > 0 {
		top := stack[len(stack)-1]
		return nil, &ParseError{top.line, top.col, fmt.Sprintf("短语 @{%s} 没有闭合", top.id)}
	}
	s.resolveSoftBreaks()
	s.sortPhrases()
	s.tokenize()
	return s, nil
}

func isEscapable(r rune) bool { return strings.ContainsRune(`<>@|\`, r) }

func indexRune(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// parseAlternative 解析从 runes[start] == '<' 开始的 <显示|朗读>，返回下一个待读下标。
func parseAlternative(runes []rune, start, lineNo int) (string, string, int, error) {
	var parts [2]strings.Builder
	part := 0
	for i := start + 1; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\\':
			if i+1 >= len(runes) || !isEscapable(runes[i+1]) {
				return "", "", 0, &ParseError{lineNo, i + 1, `无效转义：只能转义 \< \> \@ \| \\`}
			}
			parts[part].WriteRune(runes[i+1])
			i++
		case '|':
			if part == 1 {
				return "", "", 0, &ParseError{lineNo, i + 1, "<显示|朗读> 里只能有一个 |，字面的 | 写成 \\|"}
			}
			part = 1
		case '<':
			return "", "", 0, &ParseError{lineNo, i + 1, "<显示|朗读> 不能嵌套，字面的 < 写成 \\<"}
		case '>':
			if part == 0 {
				return "", "", 0, &ParseError{lineNo, start + 1, "<显示|朗读> 缺少 |；字面的 < 写成 \\<"}
			}
			display, spoken := parts[0].String(), parts[1].String()
			if display == "" && spoken == "" {
				return "", "", 0, &ParseError{lineNo, start + 1, "<|> 两侧都是空的"}
			}
			return display, spoken, i + 1, nil
		default:
			parts[part].WriteRune(r)
		}
	}
	return "", "", 0, &ParseError{lineNo, start + 1, "<显示|朗读> 没有在本行闭合"}
}

// resolveSoftBreaks 把段内换行变成空格或什么都不是：只有两侧都是字母数字时才需要空格。
func (s *Script) resolveSoftBreaks() {
	for i := range s.pieces {
		if !s.pieces[i].softBreak || i == 0 || i+1 >= len(s.pieces) {
			continue
		}
		before := []rune(s.pieces[i-1].spoken)
		after := []rune(s.pieces[i+1].spoken)
		if len(before) > 0 && len(after) > 0 && isWordRune(normRune(before[len(before)-1])) && isWordRune(normRune(after[0])) {
			s.pieces[i].display, s.pieces[i].spoken = " ", " "
		}
	}
}

// sortPhrases 按开始位置排出现顺序；同一位置开始的按开标记先后（外层在前）。
func (s *Script) sortPhrases() {
	// 闭合顺序是内层先、外层后，这里按开始 piece 稳定排序后外层仍可能排在后面，
	// 所以同位置时按开标记的行列再排一次。
	phrases := s.Phrases
	for i := 1; i < len(phrases); i++ {
		for j := i; j > 0 && phraseBefore(phrases[j], phrases[j-1]); j-- {
			phrases[j], phrases[j-1] = phrases[j-1], phrases[j]
		}
	}
	for i := range phrases {
		phrases[i].Order = i + 1
	}
}

func phraseBefore(a, b PhraseSpan) bool {
	if a.startPiece != b.startPiece {
		return a.startPiece < b.startPiece
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// tokenize 按段切出朗读侧单元，并记下每个单元覆盖的 piece。
func (s *Script) tokenize() {
	s.pieceToken = make([]int, len(s.pieces))
	for i := range s.pieceToken {
		s.pieceToken[i] = -1
	}
	for start := 0; start < len(s.pieces); {
		end := start
		for end < len(s.pieces) && s.pieces[end].para == s.pieces[start].para {
			end++
		}
		var runes []rune
		var owner []int
		for p := start; p < end; p++ {
			for _, r := range s.pieces[p].spoken {
				runes = append(runes, r)
				owner = append(owner, p)
			}
		}
		for _, sp := range tokenize(runes) {
			first, last := owner[sp.start], owner[sp.end-1]
			if s.pieceToken[first] < 0 {
				s.pieceToken[first] = len(s.Tokens)
			}
			s.Tokens = append(s.Tokens, ScriptToken{
				Token:      Token{Text: string(runes[sp.start:sp.end]), Norm: normalize(runes[sp.start:sp.end])},
				firstPiece: first, lastPiece: last,
				Line: s.pieces[first].line, Col: s.pieces[first].col,
			})
		}
		start = end
	}
}

// SpokenText 返回送进 TTS 与对齐工具的文本：标记取朗读侧，标题行丢掉，段与段之间换行，标点保留。
func (s *Script) SpokenText() string {
	var b strings.Builder
	for i, p := range s.pieces {
		if i > 0 && p.para != s.pieces[i-1].para {
			b.WriteString("\n")
		}
		b.WriteString(p.spoken)
	}
	paras := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(paras))
	for _, para := range paras {
		if t := strings.TrimFunc(para, unicode.IsSpace); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// displayText 拼出 piece 区间 [from, to) 的显示文本。
func (s *Script) displayText(from, to int) string {
	var b strings.Builder
	for _, p := range s.pieces[from:to] {
		b.WriteString(p.display)
	}
	return strings.TrimFunc(b.String(), unicode.IsSpace)
}

// tokenRange 返回首 piece 落在 [from, to) 内的朗读单元下标区间，左闭右开。
// 区间里没有朗读单元时返回空区间 (k, k)，k 是其后第一个单元的下标。
func (s *Script) tokenRange(from, to int) (int, int) {
	first := sort.Search(len(s.Tokens), func(i int) bool { return s.Tokens[i].firstPiece >= from })
	end := sort.Search(len(s.Tokens), func(i int) bool { return s.Tokens[i].firstPiece >= to })
	return first, max(first, end)
}
