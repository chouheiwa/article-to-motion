// Command gen 由 go generate 驱动，把 internal/preset 的画幅表与风格表渲染成
// 两棵项目模板源树：
//
//	assets/presets/<画幅>/          只随画幅变化（PROMPT-PRODUCTION.md）
//	assets/styles/<风格>/<画幅>/    风格 × 画幅（frame.md、风格说明书、示例图）
//	assets/styles/custom-draft/<画幅>/  自定义风格的草稿骨架（无示例图）
//
// 生成器刻意不做 YAML 重序列化：frame.md 的 frontmatter 用了 flow 映射、
// 保留尾零的浮点写法和人工排定的键序，yaml.Marshal 一律还原不出来，会让
// 「3:4 产物与手写版本逐字节相同」这条红线失守。这里只整体替换 canvas 与
// safe_area 两个块，其余原样透传。
package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/chouheiwa/articale-to-motion/internal/hyperframes"
	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"github.com/chouheiwa/articale-to-motion/internal/styleimage"
)

//go:embed templates
var templateFS embed.FS

const canvasMarker = "# @@CANVAS_AND_SAFE_AREA@@"

// 示例图在 contact sheet 里的缩略宽度，高度按画幅比例推算。
const thumbnailWidthPx = 405

// Templates 是一套风格的三份模板。
type Templates struct {
	Frontmatter string
	FrameBody   string
	GuideBody   string
}

func readTemplate(name string) (string, error) {
	body, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// loadProduction 读取与风格无关的编排提示词模板。
func loadProduction() string {
	body, err := readTemplate("PROMPT-PRODUCTION.md")
	if err != nil {
		panic(err)
	}
	return body
}

// loadTemplates 读取一套风格的模板，目录名即风格 id。
func loadTemplates(styleID string) (Templates, error) {
	var tpl Templates
	for _, item := range []struct {
		name string
		dest *string
	}{
		{"frontmatter.yaml", &tpl.Frontmatter},
		{"frame.body.md", &tpl.FrameBody},
		{"style-guide.body.md", &tpl.GuideBody},
	} {
		body, err := readTemplate("styles/" + styleID + "/" + item.name)
		if err != nil {
			return Templates{}, fmt.Errorf("风格 %s 缺少模板 %s: %w", styleID, item.name, err)
		}
		*item.dest = body
	}
	return tpl, nil
}

// canvasAndSafeArea 渲染 frontmatter 里唯一随画幅变化的两个块。
// 输出格式必须与手写的 frame.md 完全一致：safe_area 用 flow 映射单行表示。
func canvasAndSafeArea(p preset.Preset) (string, error) {
	boxes, err := p.ResolveSafeArea()
	if err != nil {
		return "", err
	}
	lines := []string{
		"canvas:",
		fmt.Sprintf("  width_px: %d", p.Canvas.WidthPx),
		fmt.Sprintf("  height_px: %d", p.Canvas.HeightPx),
		fmt.Sprintf("  fps: %d", p.Canvas.FPS),
		"  orientation: " + p.Canvas.Orientation,
		"safe_area:",
	}
	for _, item := range boxes {
		lines = append(lines, fmt.Sprintf("  %s: {left_px: %d, right_px: %d, top_px: %d, bottom_px: %d}",
			item.Name, item.Box.LeftPx, item.Box.RightPx, item.Box.TopPx, item.Box.BottomPx))
	}
	return strings.Join(lines, "\n"), nil
}

// substitute 替换模板里随画幅变化的占位符。
//
// 版本号从 internal/hyperframes 取，不在模板里写字面量：改了常量忘了模板，
// 下发给渲染 agent 的锁定指令会停在旧版本上，而 am init 装的已经是新版。
func substitute(p preset.Preset, s string) string {
	s = strings.ReplaceAll(s, "{{CANVAS}}", p.Canvas.Label())
	return strings.ReplaceAll(s, "{{HYPERFRAMES_VERSION}}", hyperframes.PinnedVersion)
}

// numericPlaceholder 匹配 {{NAME}} 或带整数偏移的 {{NAME+8}} / {{NAME-16}}。
// 偏移留给个别风格在推导值上做微调（例如奇幻风的内容左边距比安全区多 8px）。
var numericPlaceholder = regexp.MustCompile(`\{\{([A-Z_]+)([+-][0-9]+)?\}\}`)

// substituteCanvas 替换风格模板里随画幅变化的占位符。
//
// 风格模板原先把 content_width_px: 904、max_width_px: 812 这类按 1080 宽
// 竖屏算出的像素，和「竖屏」「抖音右侧互动栏」这类措辞直接写死，同一套风格
// 因此没法下发横屏项目。现在像素一律从已推导的安全区算出，措辞取自画幅表。
func substituteCanvas(p preset.Preset, s string) (string, error) {
	boxes, err := p.ResolveSafeArea()
	if err != nil {
		return "", err
	}
	box := map[string]preset.ResolvedBox{}
	for _, item := range boxes {
		box[item.Name] = item.Box
	}
	width := p.Canvas.WidthPx
	numbers := map[string]int{
		"CONTENT_LEFT_PX":       box["main_content"].LeftPx,
		"CONTENT_WIDTH_PX":      width - box["main_content"].LeftPx - box["main_content"].RightPx,
		"SUBTITLE_MAX_WIDTH_PX": width - box["subtitles"].LeftPx - box["subtitles"].RightPx,
		"COVER_MAX_WIDTH_PX":    width - box["cover_title"].LeftPx - box["cover_title"].RightPx,
		"CRITICAL_LEFT_PX":      box["critical_text"].LeftPx,
		"CRITICAL_WIDTH_PX":     width - box["critical_text"].LeftPx - box["critical_text"].RightPx,
	}
	words := map[string]string{
		"ORIENTATION":     p.Platform.Orientation,
		"PLATFORM_UI":     p.Platform.UI,
		"UI_ZONE":         p.Platform.Zone,
		"AVOIDANCE_ZONES": p.Platform.Avoidance(box["critical_text"]),
	}
	var unknown []string
	out := numericPlaceholder.ReplaceAllStringFunc(s, func(match string) string {
		parts := numericPlaceholder.FindStringSubmatch(match)
		name, offset := parts[1], parts[2]
		if value, ok := numbers[name]; ok {
			if offset != "" {
				delta, _ := strconv.Atoi(offset)
				value += delta
			}
			return strconv.Itoa(value)
		}
		if value, ok := words[name]; ok && offset == "" {
			return value
		}
		if name == "CANVAS" || name == "HYPERFRAMES_VERSION" || strings.HasPrefix(name, "SCENE_") {
			return match
		}
		unknown = append(unknown, match)
		return match
	})
	if len(unknown) > 0 {
		return "", fmt.Errorf("预设 %s 遇到未知占位符：%s", p.ID, strings.Join(unknown, " "))
	}
	return substitute(p, out), nil
}

// RenderCanvas 返回画幅源树的文件：只有编排提示词随画幅变化而与风格无关。
func RenderCanvas(p preset.Preset, production string) (map[string][]byte, error) {
	text, err := substituteCanvas(p, production)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"PROMPT-PRODUCTION.md": []byte(text)}, nil
}

// RenderStyle 返回风格 × 画幅源树内相对路径到文件内容的映射。
func RenderStyle(p preset.Preset, style preset.Style, tpl Templates) (map[string][]byte, error) {
	block, err := canvasAndSafeArea(p)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(tpl.Frontmatter, canvasMarker) {
		return nil, fmt.Errorf("风格 %s 的 frontmatter 模板缺少标记 %s", style.ID, canvasMarker)
	}
	if err := checkIdentity(style, tpl.Frontmatter); err != nil {
		return nil, err
	}
	frontmatter, err := substituteCanvas(p, strings.Replace(tpl.Frontmatter, canvasMarker, block, 1))
	if err != nil {
		return nil, err
	}
	// 原文件结构是 ---\n<frontmatter>---<body>，body 自带前导换行。
	compose := func(body string) ([]byte, error) {
		text, err := substituteCanvas(p, body)
		if err != nil {
			return nil, err
		}
		if p.LayoutNotes != "" {
			text = strings.TrimRight(text, "\n") + "\n" + p.LayoutNotes
		}
		return []byte("---\n" + frontmatter + "---" + text), nil
	}
	frame, err := compose(tpl.FrameBody)
	if err != nil {
		return nil, err
	}
	guide, err := compose(tpl.GuideBody)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"frame.md": frame, style.GuideDoc(): guide}, nil
}

// checkIdentity 保证模板里的 style_id / style_name 与风格表一致。
// validate 靠 style_id 反查说明书文件名，两边对不上时项目会找不到自己的说明书。
func checkIdentity(style preset.Style, frontmatter string) error {
	for _, line := range []string{"style_id: " + style.ID + "\n", "style_name: " + style.Name + "\n"} {
		if !strings.Contains(frontmatter, "\n"+line) && !strings.HasPrefix(frontmatter, line) {
			return fmt.Errorf("风格 %s 的 frontmatter 模板缺少 %q，与风格表不一致", style.ID, strings.TrimSpace(line))
		}
	}
	return nil
}

// withExamples 控制是否重新渲染 PNG。
//
// 默认关闭是刻意的：ImageMagick 不同版本对同一份 SVG 的输出字节不同，若把
// PNG 纳入 go generate，CI 的「重跑后 git diff 为空」这道门就会随 runner 上
// 的 ImageMagick 版本随机失败，失去意义。文本产物是确定性的，才适合当门禁。
//
// SVG 改动后手动跑一次 `go run ./gen -examples` 更新 PNG 并提交。
var withExamples = flag.Bool("examples", false, "同时用 rsvg-convert 重新渲染示例 PNG（contact sheet 用 ImageMagick 拼）")

// onlyPreset 把本次生成限定在单套预设。
//
// 配合 -examples 使用：不限定时会把所有预设的 PNG 一并重刷，而不同机器的
// ImageMagick 输出字节不同，会给没改过的预设带来无谓的二进制 diff。
var onlyPreset = flag.String("preset", "", "只生成指定画幅（默认全部）")

// onlyStyle 与 onlyPreset 同理，把 -examples 限定在单套风格。
var onlyStyle = flag.String("style", "", "只生成指定风格（默认全部）")

func main() {
	flag.Parse()
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	targets, err := selectTargets(*onlyPreset)
	if err != nil {
		fatal(err)
	}
	styles, err := selectStyles(*onlyStyle)
	if err != nil {
		fatal(err)
	}
	if err := generate(root, targets, styles, *withExamples, os.Stdout); err != nil {
		fatal(err)
	}
}

func selectTargets(id string) ([]preset.Preset, error) {
	if id == "" {
		return preset.All(), nil
	}
	p, ok := preset.ByID(id)
	if !ok {
		return nil, fmt.Errorf("未知预设 %q，可选：%s", id, strings.Join(preset.IDs(), " "))
	}
	return []preset.Preset{p}, nil
}

// generatedStyles 是生成器要产出的全部风格：内置风格加自定义草稿骨架。
//
// 草稿骨架不在内置风格表里（见 preset.CustomStyle），但它同样随画幅变化、
// 同样要过 CI 的幂等门，所以由生成器产出，而不是在 am init 时现拼。
func generatedStyles() []preset.Style {
	return append(preset.AllStyles(), preset.CustomStyle)
}

func selectStyles(id string) ([]preset.Style, error) {
	if id == "" {
		return generatedStyles(), nil
	}
	if id == preset.CustomStyle.ID {
		return []preset.Style{preset.CustomStyle}, nil
	}
	s, ok := preset.StyleByID(id)
	if !ok {
		return nil, fmt.Errorf("未知风格 %q，可选：%s", id, strings.Join(preset.StyleIDs(), " "))
	}
	return []preset.Style{s}, nil
}

// generate 把画幅源树与风格 × 画幅源树的文本产物写进 assets/。
// examples 为真时另用 rsvg-convert 重渲染示例 PNG。
func generate(root string, targets []preset.Preset, styles []preset.Style, examples bool, out io.Writer) error {
	production := loadProduction()
	for _, p := range targets {
		dir := filepath.Join("assets", "presets", p.ID)
		files, err := RenderCanvas(p, production)
		if err != nil {
			return fmt.Errorf("渲染画幅 %s: %w", p.ID, err)
		}
		if err := writeFiles(root, dir, files, out); err != nil {
			return err
		}
	}
	for _, style := range styles {
		tpl, err := loadTemplates(style.ID)
		if err != nil {
			return err
		}
		for _, p := range targets {
			files, err := RenderStyle(p, style, tpl)
			if err != nil {
				return fmt.Errorf("渲染风格 %s × 画幅 %s: %w", style.ID, p.ID, err)
			}
			dir := filepath.Join("assets", "styles", style.ID, p.ID)
			if err := writeFiles(root, dir, files, out); err != nil {
				return err
			}
			// 草稿骨架没有示例图：示例图由定风格阶段按用户描述现画。
			if examples && style.ID != preset.CustomStyle.ID {
				if err := renderExamples(root, filepath.Join(root, dir), p, style, out); err != nil {
					return fmt.Errorf("风格 %s × 画幅 %s 的示例图: %w", style.ID, p.ID, err)
				}
			}
		}
	}
	return nil
}

// writeFiles 把 files 写进 root/dir，按路径排序输出日志，让日志稳定可比对。
func writeFiles(root, dir string, files map[string][]byte, out io.Writer) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(root, dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
		fmt.Fprintln(out, "generated", filepath.ToSlash(filepath.Join(dir, name)))
	}
	return nil
}

// renderExamples 把该风格 × 画幅手写的 4 张 SVG 转成 PNG 并拼 contact sheet。
// SVG 是手工绘制的排版基准，生成器不改动它们的内容。
func renderExamples(repo, dir string, p preset.Preset, style preset.Style, out io.Writer) error {
	examples := filepath.Join(dir, "assets", "style-guide", "examples")
	// 先查手绘 SVG 是否齐全：缺稿是作者该修的问题，不该被缺工具的报错盖住。
	for _, name := range preset.ArchetypeIDs {
		if _, err := os.Stat(filepath.Join(examples, name+".svg")); err != nil {
			return fmt.Errorf("缺少手写示例图 %s: %w", name+".svg", err)
		}
	}
	if err := styleimage.CheckRenderer(); err != nil {
		return err
	}
	fontDir, cleanup, err := styleimage.DecompressFonts(exampleFonts(repo, style))
	if err != nil {
		return err
	}
	defer cleanup()
	pngPaths := make([]string, 0, len(preset.ArchetypeIDs))
	for _, name := range preset.ArchetypeIDs {
		svgPath := filepath.Join(examples, name+".svg")
		pngPath := filepath.Join(examples, name+".png")
		if err := styleimage.RenderSVG(svgPath, pngPath, fontDir); err != nil {
			return err
		}
		if err := styleimage.Compact(pngPath); err != nil {
			return err
		}
		fmt.Fprintln(out, "generated", filepath.ToSlash(pngPath))
		pngPaths = append(pngPaths, pngPath)
	}
	thumb := fmt.Sprintf("%dx%d", thumbnailWidthPx, thumbnailWidthPx*p.Canvas.HeightPx/p.Canvas.WidthPx)
	sheet := filepath.Join(examples, "contact-sheet.png")
	if err := styleimage.ContactSheet(pngPaths, sheet, thumb, style.SheetBackground, "4x1"); err != nil {
		return err
	}
	fmt.Fprintln(out, "generated", filepath.ToSlash(sheet))
	return nil
}

// exampleFonts 列出示例图可用的字体：共用正文字体加该风格从字体池带的字体，
// 与 am init 下发给项目的字体完全一致——示例图不该用到项目里没有的字体。
func exampleFonts(repo string, style preset.Style) []string {
	var out []string
	for _, font := range preset.SharedFonts {
		out = append(out, filepath.Join(repo, "assets", "shared", "assets", "fonts", font.File))
	}
	for _, font := range style.Fonts {
		out = append(out, filepath.Join(repo, "assets", "fontpool", font.File))
	}
	return out
}

// repoRoot 从当前工作目录向上找到含 go.mod 的目录。
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("找不到仓库根目录（向上未发现 go.mod）")
		}
		dir = parent
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
