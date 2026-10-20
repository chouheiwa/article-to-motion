package assets

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

// Files 保存 am 二进制自带的项目骨架，分三棵源树：
//
//	assets/shared/                 与画幅、风格都无关，所有项目共用
//	assets/presets/<画幅>/          只随画幅变化（由 go generate 产出）
//	assets/styles/<风格>/<画幅>/    可选风格包 × 画幅（文本由 go generate 产出，示例图手绘）
//
// 每棵树的内部路径就是它在项目根下的目标路径，Initialize 直接叠加拷贝，
// 不做任何路径改写。
//
// assets/shared/assets/fonts 里的 CJK 字体必须随项目走：渲染机是干净的无头
// Chrome，字体栈里没有 @font-face 的字体族会静默回退，本地看着正常、成片排版
// 是错的。
//
// assets/shared/.agents/skills 是随项目下发的技能树。渲染工具靠 scene.ResolveSkill
// 从镜头目录逐级向上找 .agents/skills，落到项目根就能被发现，无需用户另行安装。
//
// .env.example 和 .agents 都要单列：//go:embed 对目录模式会跳过 . 开头的条目，
// 漏了不报错，只是整棵树静默消失——assets_test.go 的内容断言守着这一点。
//
// assets/fontpool 是风格独有字体的池子，不是源树：am init 只按所选风格声明
// 的字体挑文件拷进项目，见 StyleFonts。
//
//go:embed assets/shared assets/presets assets/styles assets/fontpool assets/shared/.env.example assets/shared/.agents
var Files embed.FS

// Shared 返回与画幅无关的那棵源树。
func Shared() (fs.FS, error) {
	return fs.Sub(Files, "assets/shared")
}

// Preset 返回指定画幅预设的素材树。
func Preset(id string) (fs.FS, error) {
	sub, err := fs.Sub(Files, "assets/presets/"+id)
	if err != nil {
		return nil, fmt.Errorf("找不到预设素材 %s: %w", id, err)
	}
	if _, err := fs.Stat(sub, "PROMPT-PRODUCTION.md"); err != nil {
		return nil, fmt.Errorf("预设素材 %s 不完整，缺少 PROMPT-PRODUCTION.md", id)
	}
	return sub, nil
}

// Style 返回指定风格在指定画幅下的素材树（frame.md、风格说明书、示例图）。
func Style(styleID, canvasID string) (fs.FS, error) {
	sub, err := fs.Sub(Files, "assets/styles/"+styleID+"/"+canvasID)
	if err != nil {
		return nil, fmt.Errorf("找不到风格素材 %s/%s: %w", styleID, canvasID, err)
	}
	if _, err := fs.Stat(sub, "frame.md"); err != nil {
		return nil, fmt.Errorf("风格素材 %s/%s 不完整，缺少 frame.md", styleID, canvasID)
	}
	return sub, nil
}

// BuiltinSkills 列出随二进制下发的技能名。
//
// 从嵌入树现读而不是写死一份清单：新增内置技能只要放进
// assets/shared/.agents/skills/ 就自动受保护，不会因为漏改这里而被上游同名
// 技能悄悄覆盖掉本仓库的 fork。
func BuiltinSkills() (map[string]bool, error) {
	entries, err := fs.ReadDir(Files, "assets/shared/.agents/skills")
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out[entry.Name()] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("内置技能树是空的：assets/shared/.agents/skills")
	}
	return out, nil
}

// FontsDir 是字体在用户项目里的目录，与 frame.md typography.font_files 的前缀一致。
const FontsDir = "assets/fonts/"

// StyleFonts 返回所选风格要从字体池带进项目的文件：字体本身与各自的许可证，
// 键是项目内的相对路径。许可证按文件去重——同一字体族的多个字重共用一份。
func StyleFonts(style preset.Style) (map[string][]byte, error) {
	out, err := FontFiles(style.Fonts)
	if err != nil {
		return nil, fmt.Errorf("风格 %s: %w", style.ID, err)
	}
	return out, nil
}

// FontFiles 从字体池取出给定字体及其许可证，键是项目内的相对路径。
// 自定义风格的 am style add-font 与内置风格的 am init 共用这一份读取逻辑。
func FontFiles(fonts []preset.Font) (map[string][]byte, error) {
	out := make(map[string][]byte, 2*len(fonts))
	for _, font := range fonts {
		for _, name := range []string{font.File, font.License} {
			if _, done := out[FontsDir+name]; done {
				continue
			}
			body, err := Files.ReadFile("assets/fontpool/" + name)
			if err != nil {
				return nil, fmt.Errorf("字体池里没有文件 %s", name)
			}
			out[FontsDir+name] = body
		}
	}
	return out, nil
}
