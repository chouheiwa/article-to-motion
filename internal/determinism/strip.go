package determinism

import (
	"regexp"
	"strings"
)

// blank 把 [start, end) 区间替换成空格，保留换行，使行号与偏移不变。
func blank(text []byte, start, end int) {
	for i := start; i < end && i < len(text); i++ {
		if text[i] != '\n' {
			text[i] = ' '
		}
	}
}

// lexJS 对 JS 源码做最小词法处理，返回两份与原文等长的文本：
//
//   - withStrings：剥掉注释，字符串原样保留。网络规则要看 fetch 的 URL 字面量。
//   - codeOnly：再把字符串、模板字符串与正则字面量的内容也抹成空格。
//     时钟、随机数等规则只在这份上匹配：画面文案写着「Date.now() 已禁用」
//     不是违规，误判一次这道门就没人信了。
//
// 正则字面量必须识别：`/["']/` 里的引号会被当成字符串开头，把后面的注释整段
// 吞掉，注释里提到的禁用 API 就成了违规。判定沿用常见启发式：`/` 前一个有效
// 字符是运算符或开括号、或前一个词是 return 等关键字时，它开始一个正则。
func lexJS(source string) (withStrings, codeOnly string) {
	text := []byte(source)
	code := []byte(source)
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\'' || c == '"' || c == '`':
			end := skipString(text, i)
			blank(code, i+1, end)
			i = end
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			end := i
			for end < len(text) && text[end] != '\n' {
				end++
			}
			blank(text, i, end)
			blank(code, i, end)
			i = end
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := i + 2
			for end+1 < len(text) && !(text[end] == '*' && text[end+1] == '/') {
				end++
			}
			end = min(end+2, len(text))
			blank(text, i, end)
			blank(code, i, end)
			i = end - 1
		case c == '/' && regexAllowed(text, i):
			end := skipRegex(text, i)
			blank(code, i+1, end)
			i = end
		}
	}
	return string(text), string(code)
}

// regexKeywords 之后的 `/` 开始正则而不是除号。
var regexKeywords = []string{"return", "typeof", "case", "do", "else", "in", "of", "void", "yield", "await"}

// regexAllowed 判断 pos 处的 `/` 是否开始一个正则字面量。
func regexAllowed(text []byte, pos int) bool {
	j := pos - 1
	for j >= 0 && (text[j] == ' ' || text[j] == '\t' || text[j] == '\r') {
		j--
	}
	if j < 0 || text[j] == '\n' {
		return true
	}
	if strings.IndexByte("(,=:[!&|?{};+-*%<>~^", text[j]) >= 0 {
		return true
	}
	end := j + 1
	for j >= 0 && isIdent(text[j]) {
		j--
	}
	word := string(text[j+1 : end])
	for _, keyword := range regexKeywords {
		if word == keyword {
			return true
		}
	}
	return false
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// skipRegex 从开头的 `/` 跳到结尾的 `/`，字符类 [...] 里的 `/` 不算结尾。
// 行内找不到结尾时退回到开头之后：说明判断错了，它其实是除号。
func skipRegex(text []byte, start int) int {
	inClass := false
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				return i
			}
		case '\n':
			return start
		}
	}
	return start
}

// skipString 从引号位置跳到字符串结尾，返回结尾引号的下标。
func skipString(text []byte, start int) int {
	quote := text[start]
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case quote:
			return i
		case '\n':
			// 普通引号字符串不能跨行；未闭合时在行尾停下，避免吞掉后续代码。
			if quote != '`' {
				return i
			}
		}
	}
	return len(text)
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// stripHTMLComments 剥掉 HTML 注释，保留换行。
func stripHTMLComments(source string) string {
	text := []byte(source)
	for _, loc := range htmlComment.FindAllIndex(text, -1) {
		blank(text, loc[0], loc[1])
	}
	return string(text)
}

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripCSSComments 剥掉 CSS 块注释，保留换行。
func stripCSSComments(source string) string {
	text := []byte(source)
	for _, loc := range cssComment.FindAllIndex(text, -1) {
		blank(text, loc[0], loc[1])
	}
	return string(text)
}
