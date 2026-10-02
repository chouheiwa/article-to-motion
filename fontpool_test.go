package assets

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
	"gopkg.in/yaml.v3"
)

// 风格声明的字体必须真的在字体池里，且随字体带上许可证：OFL 要求分发时附带许可文本。
func TestStyleFontsExistWithLicenses(t *testing.T) {
	for _, style := range preset.AllStyles() {
		files, err := StyleFonts(style)
		if err != nil {
			t.Error(err)
			continue
		}
		for _, font := range style.Fonts {
			for _, name := range []string{font.File, font.License} {
				if _, ok := files[FontsDir+name]; !ok {
					t.Errorf("风格 %s 缺少 %s", style.ID, name)
				}
			}
		}
	}
}

// 字体池与 preset.AllFonts 必须双向一致：池里有而表里没有，am style fonts
// 列不出来、自定义风格永远用不上，只是白白撑大二进制；表里有而池里没有，
// am style add-font 会在用户项目里才失败。
func TestFontPoolMatchesAllFonts(t *testing.T) {
	listed := map[string]bool{}
	files, err := FontFiles(preset.AllFonts())
	if err != nil {
		t.Fatal(err)
	}
	for _, font := range preset.AllFonts() {
		listed[font.File], listed[font.License] = true, true
		for _, name := range []string{font.File, font.License} {
			if _, ok := files[FontsDir+name]; !ok {
				t.Errorf("AllFonts 声明的 %s 不在字体池里", name)
			}
		}
	}
	entries, err := fs.ReadDir(Files, "assets/fontpool")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "README.md" {
			continue
		}
		if !listed[name] {
			t.Errorf("字体池文件 %s 不在 preset.AllFonts 里", name)
		}
	}
}

func TestFontFilesRejectsUnknownFont(t *testing.T) {
	_, err := FontFiles([]preset.Font{{File: "no-such.woff2", Family: "X", Weight: 400, License: "LICENSE-x.txt"}})
	if err == nil || !strings.Contains(err.Error(), "no-such.woff2") {
		t.Fatalf("字体池里没有的字体应报错并点名，实际 %v", err)
	}
}

// frame.md 的 font_files 与风格表必须一致：声明了却没带进项目，渲染时静默回退；
// 带进项目却没声明，渲染 agent 不知道它存在，只是白占体积。族名也必须对上，
// 否则 @font-face 与字体栈对不上。
func TestFrameFontFilesMatchStyleFonts(t *testing.T) {
	available := map[string]preset.Font{}
	for _, font := range preset.SharedFonts {
		available[FontsDir+font.File] = font
	}
	for _, style := range preset.AllStyles() {
		body, err := Files.ReadFile("assets/styles/" + style.ID + "/" + preset.Default().ID + "/frame.md")
		if err != nil {
			t.Errorf("风格 %s 缺少 frame.md：%v", style.ID, err)
			continue
		}
		parts := bytes.SplitN(body, []byte("---"), 3)
		var tokens struct {
			Typography struct {
				FontFiles []struct {
					Family string `yaml:"family"`
					Weight int    `yaml:"weight"`
					File   string `yaml:"file"`
				} `yaml:"font_files"`
			} `yaml:"typography"`
		}
		if err := yaml.Unmarshal(parts[1], &tokens); err != nil {
			t.Fatal(err)
		}
		declared := map[string]bool{}
		for _, entry := range tokens.Typography.FontFiles {
			declared[entry.File] = true
			font, ok := available[entry.File]
			if !ok {
				for _, own := range style.Fonts {
					if FontsDir+own.File == entry.File {
						font, ok = own, true
					}
				}
			}
			if !ok {
				t.Errorf("风格 %s 的 font_files 声明了项目里不会有的字体：%s", style.ID, entry.File)
				continue
			}
			if !strings.EqualFold(entry.Family, font.Family) {
				t.Errorf("风格 %s 的 %s 族名写成 %q，字体表是 %q", style.ID, entry.File, entry.Family, font.Family)
			}
		}
		for _, own := range style.Fonts {
			if !declared[FontsDir+own.File] {
				t.Errorf("风格 %s 带进项目的字体 %s 没有写进 font_files", style.ID, own.File)
			}
		}
	}
}
