package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestStyleFontsListsWholePool(t *testing.T) {
	var out bytes.Buffer
	if code := Execute([]string{"style", "fonts"}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, font := range append(preset.AllFonts(), preset.SharedFonts...) {
		if !strings.Contains(out.String(), font.Family) || !strings.Contains(out.String(), font.File) {
			t.Errorf("清单缺少 %s（%s）", font.Family, font.File)
		}
	}
}

// projectDir 返回一个带 frame.md 的目录，add-font 靠它认出项目根。
func projectDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "frame.md"), []byte("---\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStyleAddFontRejectsNonProject(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	if code := Execute([]string{"style", "add-font", "Fredoka", "--project-root", root}, &out, &out); code == 0 || !strings.Contains(out.String(), "frame.md") {
		t.Fatalf("非项目目录应失败，exit=%d：%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "assets")); err == nil {
		t.Fatal("失败时不应建出 assets/")
	}
}

func TestStyleAddFontCopiesFontAndLicense(t *testing.T) {
	root := projectDir(t)
	var out bytes.Buffer
	args := []string{"style", "add-font", "Fredoka", "--weight", "700", "--project-root", root}
	if code := Execute(args, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, name := range []string{preset.FontFredoka700.File, preset.FontFredoka700.License} {
		if _, err := os.Stat(filepath.Join(root, "assets", "fonts", name)); err != nil {
			t.Errorf("缺少 %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "assets", "fonts", preset.FontFredoka600.File)); err == nil {
		t.Error("指定字重时不应拷入其他字重")
	}
	if !strings.Contains(out.String(), "assets/fonts/") {
		t.Errorf("输出应列出字体相对路径：%s", out.String())
	}
	// 重复执行是幂等的。
	out.Reset()
	if code := Execute(args, &out, &out); code != 0 {
		t.Fatalf("重复执行应成功 exit %d: %s", code, out.String())
	}
}

func TestStyleAddFontWithoutWeightCopiesEveryWeight(t *testing.T) {
	root := projectDir(t)
	var out bytes.Buffer
	if code := Execute([]string{"style", "add-font", "fredoka", "--project-root", root}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, font := range []preset.Font{preset.FontFredoka600, preset.FontFredoka700} {
		if _, err := os.Stat(filepath.Join(root, "assets", "fonts", font.File)); err != nil {
			t.Errorf("缺少 %s", font.File)
		}
	}
}

func TestStyleAddFontRejectsUnknownAndConflicts(t *testing.T) {
	root := projectDir(t)
	var out bytes.Buffer
	if code := Execute([]string{"style", "add-font", "Comic Sans", "--project-root", root}, &out, &out); code == 0 || !strings.Contains(out.String(), "am style fonts") {
		t.Fatalf("未知字体应失败并指向 am style fonts，exit=%d：%s", code, out.String())
	}
	out.Reset()
	if code := Execute([]string{"style", "add-font", "Fredoka", "--weight", "900", "--project-root", root}, &out, &out); code == 0 {
		t.Fatalf("字体池里没有的字重应失败：%s", out.String())
	}
	dir := filepath.Join(root, "assets", "fonts")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, preset.FontCaveat600.File), []byte("user font"), 0o644)
	out.Reset()
	if code := Execute([]string{"style", "add-font", "Caveat", "--project-root", root}, &out, &out); code == 0 {
		t.Fatalf("目标已存在且内容不同应失败：%s", out.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, preset.FontCaveat600.File))
	if string(body) != "user font" {
		t.Fatal("不得覆盖用户已有的字体文件")
	}
}

// 端到端：旧版 init 自定义风格 → 模拟补齐兼容文件、画好 SVG →
// --regenerate-examples 渲染 PNG → validate style 通过。草稿阶段就能出图，
// 否则示例图要等 validate 通过才能生成，而 validate 又要先有示例图。
func TestCustomStyleEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	canvas := preset.Default()
	target := filepath.Join(t.TempDir(), "video")
	var out bytes.Buffer
	if code := Execute([]string{"init", target, "--canvas", canvas.ID, "--style", "custom", "--style-brief", "暖色胶片", "--skip-hyperframes"}, &out, &out); code != 0 {
		t.Fatalf("init exit %d: %s", code, out.String())
	}
	// 用默认风格的 token 充当定稿结果，只换身份。
	source := filepath.Join("..", "..", "assets", "styles", preset.DefaultStyle().ID, canvas.ID)
	frame, err := os.ReadFile(filepath.Join(source, "frame.md"))
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Replace(string(frame), "style_id: "+preset.DefaultStyle().ID, "style_id: custom-warm-film", 1)
	for _, name := range []string{"frame.md", preset.CustomStyle.GuideDoc()} {
		if err := os.WriteFile(filepath.Join(target, filepath.FromSlash(name)), []byte(final), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	examples := filepath.Join(target, "assets", "style-guide", "examples")
	os.MkdirAll(examples, 0o755)
	for _, id := range preset.ArchetypeIDs {
		svg, err := os.ReadFile(filepath.Join(source, "assets", "style-guide", "examples", id+".svg"))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(examples, id+".svg"), svg, 0o644)
	}
	stubImageToolsWithPNG(t, canvas.Canvas.WidthPx, canvas.Canvas.HeightPx)
	out.Reset()
	if code := Execute([]string{"validate", "style", "--project-root", target, "--regenerate-examples"}, &out, &out); code != 0 {
		t.Fatalf("regenerate + validate exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "旧版风格文件校验通过") {
		t.Fatalf("应校验通过：%s", out.String())
	}
}

// stubImageToolsWithPNG 在 PATH 里放 rsvg-convert、fonttools 与 magick 的桩。
// rsvg-convert 的桩写出一个只有 PNG 头的文件，宽高等于画布，足够过尺寸校验。
func stubImageToolsWithPNG(t *testing.T, width, height int) {
	t.Helper()
	bin := t.TempDir()
	header := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	header = append(header, byte(width>>24), byte(width>>16), byte(width>>8), byte(width))
	header = append(header, byte(height>>24), byte(height>>16), byte(height>>8), byte(height))
	template := filepath.Join(bin, "template.png")
	if err := os.WriteFile(template, header, 0o644); err != nil {
		t.Fatal(err)
	}
	copyOut := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; cp '" + template + "' \"$1\"; exit 0; fi\n  shift\ndone\nexit 1\n"
	touchOut := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; : > \"$1\"; exit 0; fi\n  shift\ndone\nexit 1\n"
	magick := "#!/bin/sh\nfor last; do :; done\n: > \"$last\"\n"
	for name, body := range map[string]string{"rsvg-convert": copyOut, "fonttools": touchOut, "magick": magick} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 桩里的 cp 要靠系统目录；真的图像工具装在 Homebrew 等目录，不在这两个里。
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
}
