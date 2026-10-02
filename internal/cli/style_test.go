package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestResolveStyleAcceptsKnownID(t *testing.T) {
	s, err := resolveStyle(preset.DefaultStyle().ID, nil, &bytes.Buffer{})
	if err != nil || s.ID != preset.DefaultStyle().ID {
		t.Fatalf("resolveStyle = %v, %v", s.ID, err)
	}
}

func TestResolveStyleRejectsUnknownIDAndListsChoices(t *testing.T) {
	_, err := resolveStyle("no-such-style", nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("未知风格应报错")
	}
	for _, id := range preset.StyleIDs() {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("错误信息应列出 %s，实际：%v", id, err)
		}
	}
}

// --style 出现之前的脚本只传 --canvas，非交互时必须回落到默认风格而不是报错。
func TestResolveStyleDefaultsWithoutTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := resolveStyle("", f, &bytes.Buffer{})
	if err != nil || s.ID != preset.DefaultStyle().ID {
		t.Fatalf("非交互应取默认风格，实际 %v, %v", s.ID, err)
	}
}

// 每套风格 × 画幅都要能 init 成功（共用树、画幅树、风格树之间没有路径冲突），
// 且产物能过 validate style（说明书能按 style_id 找到、示例图尺寸对、字体随项目走）。
func TestInitEveryStyleAndCanvasPassesStyleValidation(t *testing.T) {
	for _, style := range preset.AllStyles() {
		for _, canvas := range preset.All() {
			t.Run(style.ID+"/"+canvas.ID, func(t *testing.T) {
				target := filepath.Join(t.TempDir(), "video")
				var out bytes.Buffer
				if code := Execute([]string{"init", target, "--canvas", canvas.ID, "--style", style.ID, "--skip-hyperframes"}, &out, &out); code != 0 {
					t.Fatalf("init 失败 exit=%d：%s", code, out.String())
				}
				if !strings.Contains(out.String(), style.Name) {
					t.Errorf("init 输出应点名风格 %s：%s", style.Name, out.String())
				}
				if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(style.GuideDoc()))); err != nil {
					t.Errorf("缺少风格说明书：%v", err)
				}
				out.Reset()
				if code := Execute([]string{"validate", "style", "--project-root", target}, &out, &out); code != 0 {
					t.Fatalf("validate style 失败 exit=%d：%s", code, out.String())
				}
			})
		}
	}
}

func TestInitRejectsUnknownStyle(t *testing.T) {
	var out bytes.Buffer
	code := Execute([]string{"init", filepath.Join(t.TempDir(), "video"), "--canvas", "vertical-3x4", "--style", "no-such-style", "--skip-hyperframes"}, &out, &out)
	if code == 0 || !strings.Contains(out.String(), "未知风格") {
		t.Fatalf("未知风格应失败，exit=%d：%s", code, out.String())
	}
}

func TestResolveStyleAcceptsCustomFlag(t *testing.T) {
	s, err := resolveStyle(preset.CustomStyleFlag, nil, &bytes.Buffer{})
	if err != nil || s.ID != preset.CustomStyle.ID {
		t.Fatalf("--style custom 应取自定义草稿，实际 %v, %v", s.ID, err)
	}
}

func TestStylePickerOffersCustomLast(t *testing.T) {
	picker := newStylePicker()
	last := picker.choices[len(picker.choices)-1]
	if last.ID != preset.CustomStyle.ID || len(picker.choices) != len(preset.AllStyles())+1 {
		t.Fatalf("选择框末尾应是自定义风格，实际 %s（共 %d 项）", last.ID, len(picker.choices))
	}
}

func TestResolveStyleBrief(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	file := filepath.Join(t.TempDir(), "brief.txt")
	os.WriteFile(file, []byte("  复古胶片\n手写标注  \n"), 0o644)
	cases := []struct {
		name    string
		style   preset.Style
		flag    string
		file    string
		want    string
		wantErr string
	}{
		{"内置风格不带描述", preset.DefaultStyle(), "", "", "", ""},
		{"内置风格带描述", preset.DefaultStyle(), "暖色", "", "", "只能与 --style custom"},
		{"自定义用参数", preset.CustomStyle, " 暖色胶片 ", "", "暖色胶片", ""},
		{"自定义用文件", preset.CustomStyle, "", file, "复古胶片\n手写标注", ""},
		{"两者都给", preset.CustomStyle, "暖色", file, "", "只能二选一"},
		{"非交互缺描述", preset.CustomStyle, "", "", "", "--style-brief"},
		{"空白描述", preset.CustomStyle, "   ", "", "", "不能为空"},
		{"文件不存在", preset.CustomStyle, "", file + ".missing", "", "读取"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveStyleBrief(tc.style, tc.flag, tc.file, devNull, &bytes.Buffer{})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("应报含 %q 的错误，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v；期望 %q", got, err, tc.want)
			}
		})
	}
}

// init 自定义风格：写出描述与草稿，validate style 明确报「定风格未完成」。
func TestInitCustomStyleWritesBriefAndDraft(t *testing.T) {
	for _, canvas := range preset.All() {
		t.Run(canvas.ID, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "video")
			var out bytes.Buffer
			args := []string{"init", target, "--canvas", canvas.ID, "--style", "custom", "--style-brief", "暖色胶片、手写标注", "--skip-hyperframes"}
			if code := Execute(args, &out, &out); code != 0 {
				t.Fatalf("init 失败 exit=%d：%s", code, out.String())
			}
			brief, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(styleBriefPath)))
			if err != nil || !strings.Contains(string(brief), "暖色胶片、手写标注") {
				t.Fatalf("描述未写入 %s：%v", styleBriefPath, err)
			}
			if !strings.Contains(out.String(), "定风格") {
				t.Errorf("init 输出应提示下一步要定风格：%s", out.String())
			}
			for _, name := range []string{"frame.md", preset.CustomStyle.GuideDoc()} {
				if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(name))); err != nil {
					t.Errorf("缺少 %s", name)
				}
			}
			out.Reset()
			code := Execute([]string{"validate", "style", "--project-root", target}, &out, &out)
			if code == 0 || !strings.Contains(out.String(), "定风格") {
				t.Fatalf("草稿项目的 validate style 应失败并提示定风格，exit=%d：%s", code, out.String())
			}
		})
	}
}

func TestInitCustomStyleRequiresBriefWithoutTerminal(t *testing.T) {
	var out bytes.Buffer
	code := Execute([]string{"init", filepath.Join(t.TempDir(), "video"), "--canvas", "vertical-3x4", "--style", "custom", "--skip-hyperframes"}, &out, &out)
	if code == 0 || !strings.Contains(out.String(), "--style-brief") {
		t.Fatalf("非交互缺描述应失败并点名参数，exit=%d：%s", code, out.String())
	}
}
