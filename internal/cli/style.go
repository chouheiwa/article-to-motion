package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

// styleBriefPath 是自定义风格描述在项目里的位置，编排提示词的定风格阶段从这里读。
const styleBriefPath = "docs/style-brief.md"

// maxStyleBriefRunes 限制描述长度：描述会原样进入编排提示词，过长的粘贴多半
// 是贴错了文件，而不是真的需要这么长的风格说明。
const maxStyleBriefRunes = 4000

// styleOptions 供错误信息与帮助文字共用的可读清单。
func styleOptions() string {
	lines := make([]string, 0, len(preset.AllStyles())+1)
	for _, s := range preset.AllStyles() {
		lines = append(lines, fmt.Sprintf("  %-32s%s", s.ID, s.Label))
	}
	lines = append(lines, fmt.Sprintf("  %-32s%s", preset.CustomStyleFlag, preset.CustomStyle.Label))
	return strings.Join(lines, "\n")
}

func newStylePicker() choicePicker[preset.Style] {
	choices := append(preset.AllStyles(), preset.CustomStyle)
	return choicePicker[preset.Style]{title: "选择风格", choices: choices, label: func(s preset.Style) string { return s.Label }}
}

// resolveStyle 决定本次 init 使用哪套风格。
//
// 与画幅不同，非交互且未传 --style 时取默认风格而不报错：风格选错在第一张
// 示例图上就看得出来，不像画幅那样要到成片才暴露；而且 --style 出现之前的
// 脚本和 CI 只传 --canvas，失败关闭会让它们全部坏掉。
func resolveStyle(flag string, stdin *os.File, stdout io.Writer) (preset.Style, error) {
	if flag == preset.CustomStyleFlag {
		return preset.CustomStyle, nil
	}
	if flag != "" {
		s, ok := preset.StyleByID(flag)
		if !ok {
			return preset.Style{}, fmt.Errorf("未知风格 %q，可选：\n%s", flag, styleOptions())
		}
		return s, nil
	}
	if !isTerminal(stdin) {
		return preset.DefaultStyle(), nil
	}
	return runPicker(newStylePicker(), "风格", stdout)
}

// resolveStyleBrief 取得自定义风格的文字描述；内置风格不接受描述。
//
// 内置风格带描述直接报错而不是忽略：用户以为描述生效了，成片却还是原样的
// 内置风格，这比一条报错更难发现。自定义风格在非交互环境缺描述同样报错——
// 定风格阶段没有描述可读，只能凭空编一套。
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
	case isTerminal(stdin):
		fmt.Fprint(stdout, "用一段话描述想要的风格（配色、质感、字体气质、参考对象等），回车结束：\n> ")
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("读取风格描述失败：%w", err)
		}
		raw = line
	default:
		return "", fmt.Errorf("自定义风格需要描述：用 --style-brief \"…\" 或 --style-brief-file <文件> 提供")
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
	return []byte("# 风格描述\n\n" +
		"> 由 `am init --style " + preset.CustomStyleFlag + "` 写入。制作开始前的定风格阶段按这里的描述生成 " +
		"`frame.md`、`" + preset.CustomStyle.GuideDoc() + "` 与四张示例图；改了描述要重新定风格。\n\n" +
		brief + "\n")
}
