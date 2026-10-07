// Package align 把强制对齐工具的逐字时间戳映射回终稿原文，产出句级字幕与短语时间轴。
//
// 过去这一步由操作者手工完成：对齐工具只认送进 TTS 的文本（年份写成逐位汉字、
// 英文缩写拆成字母），输出的又是丢了标点的逐字时间，要「按替换清单映射回终稿
// 原文」「按顺序逐字对回」，再手写 phrase-timeline.json。几千个字靠眼睛对，
// 错一个字后面整串错位，而且错得悄无声息。
//
// 本包把映射交给代码：终稿用轻量标记同时写出显示文本与朗读文本，朗读文本送
// TTS 与对齐；对齐结果与朗读文本做动态规划匹配（移植自 hypit 的
// speech-alignment），每个朗读单元都带着匹配标签，漏读、多读、读错都进报告。
package align

import "unicode"

// Token 是一个匹配单元：一个汉字，或一串连续的字母数字（一个英文词、一个数）。
type Token struct {
	// Text 是原样文本，Norm 是用于比对的规范化文本（全角转半角、小写、去掉词内连接符）。
	Text string
	Norm string
}

// span 是 Tokenize 在 rune 序列里找到的一个单元，左闭右开。
type span struct{ start, end int }

// isCJK 判断是否按单字成词：汉字、假名、谚文。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// normRune 把全角 ASCII 转成半角并转小写。只做逐 rune 的一对一映射，
// 保证规范化前后 rune 下标不变，单元位置可以直接回指原文。
func normRune(r rune) rune {
	switch {
	case r >= 0xFF01 && r <= 0xFF5E:
		r -= 0xFEE0
	case r == 0x3000:
		r = ' '
	}
	return unicode.ToLower(r)
}

// isWordRune 是可以组成英文词或数的字符（非 CJK 的字母、数字）。
func isWordRune(r rune) bool {
	return !isCJK(r) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// isJoiner 是词内连接符：3.5、GPT-4、don't 里的那个字符，只在两侧都是字母数字时算词内。
func isJoiner(r rune) bool { return r == '\'' || r == '.' || r == '-' || r == '’' }

// tokenize 在 rune 序列中切出匹配单元。标点和空白不成单元。
func tokenize(runes []rune) []span {
	var spans []span
	for i := 0; i < len(runes); {
		r := normRune(runes[i])
		switch {
		case isCJK(r):
			spans = append(spans, span{i, i + 1})
			i++
		case isWordRune(r):
			j := i + 1
			for j < len(runes) {
				next := normRune(runes[j])
				if isWordRune(next) {
					j++
					continue
				}
				if isJoiner(next) && j+1 < len(runes) && isWordRune(normRune(runes[j+1])) {
					j += 2
					continue
				}
				break
			}
			spans = append(spans, span{i, j})
			i = j
		default:
			i++
		}
	}
	return spans
}

// normalize 返回比对用文本：逐 rune 规范化后只保留字母与数字。
func normalize(runes []rune) string {
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		r = normRune(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// Tokenize 把一段文本切成匹配单元。
func Tokenize(text string) []Token {
	runes := []rune(text)
	spans := tokenize(runes)
	tokens := make([]Token, 0, len(spans))
	for _, s := range spans {
		tokens = append(tokens, Token{Text: string(runes[s.start:s.end]), Norm: normalize(runes[s.start:s.end])})
	}
	return tokens
}
