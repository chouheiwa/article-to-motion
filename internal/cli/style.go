package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

// styleBriefPath 是旧版自定义风格描述的位置，供存量项目读取。
const styleBriefPath = preset.CustomStyleBriefPath

// maxStyleBriefRunes 限制描述长度：描述会原样进入编排提示词，过长的粘贴多半
// 是贴错了文件，而不是真的需要这么长的风格说明。
const maxStyleBriefRunes = 4000

// styleOptions 供错误信息与帮助文字共用的可读清单。
func styleOptions() string {
	lines := make([]string, 0, len(preset.AllStyles())+1)
	// 自定义风格排在第一项：内置风格只作为旧版参数保留。
	lines = append(lines, fmt.Sprintf("  %-32s%s", preset.CustomStyleFlag, preset.CustomStyle.Label))
	for _, s := range preset.AllStyles() {
		lines = append(lines, fmt.Sprintf("  %-32s%s", s.ID, s.Label))
	}
	return strings.Join(lines, "\n")
}

func newStylePicker() choicePicker[preset.Style] {
	choices := append([]preset.Style{preset.CustomStyle}, preset.AllStyles()...)
	return choicePicker[preset.Style]{title: "选择风格", choices: choices, label: func(s preset.Style) string { return s.Label }}
}

// resolveStyle 决定本次 init 使用哪套风格。
//
// 风格参数只为兼容旧版初始化流程；渲染提示词不会把它当作视觉要求。
func resolveStyle(flag string, _ *os.File, _ io.Writer) (preset.Style, error) {
	if flag == "" || flag == preset.CustomStyleFlag {
		return preset.CustomStyle, nil
	}
	s, ok := preset.StyleByID(flag)
	if !ok {
		return preset.Style{}, fmt.Errorf("未知风格 %q，可选：\n%s", flag, styleOptions())
	}
	return s, nil
}

// resolveStyleBrief 取得自定义风格的文字描述；内置风格不接受描述。
//
// 内置风格带描述直接报错而不是忽略：用户以为描述生效了，成片却还是原样的
// 内置风格，这比一条报错更难发现。自定义风格在非交互环境缺描述时使用一条
// 无交互的旧版初始化仍需要一个描述文件，因此写入兼容性说明。
const defaultStyleBrief = preset.AutomaticStyleBrief

func resolveStyleBrief(style preset.Style, flag, file string, stdin *os.File, stdout io.Writer) (string, error) {
	if style.ID != preset.CustomStyle.ID {
		if flag != "" || file != "" {
			return "", fmt.Errorf("--style-brief / --style-brief-file 只能与 --style %s 一起使用", preset.CustomStyleFlag)
		}
		return "", nil
	}
	var raw string
	switch {
	case flag != "" && file != "":
		return "", fmt.Errorf("--style-brief 与 --style-brief-file 只能二选一")
	case flag != "":
		raw = flag
	case file != "":
		body, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("读取风格描述文件失败：%w", err)
		}
		raw = string(body)
	default:
		return defaultStyleBrief, nil
	}
	brief := strings.TrimSpace(raw)
	if brief == "" {
		return "", fmt.Errorf("风格描述不能为空")
	}
	if n := len([]rune(brief)); n > maxStyleBriefRunes {
		return "", fmt.Errorf("风格描述过长（%d 字，上限 %d）", n, maxStyleBriefRunes)
	}
	return brief, nil
}

// styleBriefDocument 是写进项目的描述文件内容。
func styleBriefDocument(brief string) []byte {
	return []byte("# 旧版风格描述（兼容文件）\n\n" +
		"> 由旧版 `am init` 写入，仅供存量项目参考。新镜头的配色、字体、版式、材质与动效由镜头自行决定；改动本文件不会形成渲染要求。\n\n" +
		brief + "\n")
}
