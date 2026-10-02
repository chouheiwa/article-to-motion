package validate

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

const customID = "custom-warm-film"

// customFixture 模拟定风格阶段完成后的项目：沿用默认风格的 token 与示例图，
// 只把身份改成自定义风格，说明书挪到自定义风格的固定路径。
func customFixture(t *testing.T, presetID string) string {
	t.Helper()
	root := builtinFixture(t, preset.DefaultStyle(), presetID)
	body, err := os.ReadFile(filepath.Join(root, "frame.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(body), "style_id: "+preset.DefaultStyle().ID, "style_id: "+customID, 1)
	text = strings.Replace(text, "style_name: "+preset.DefaultStyle().Name, "style_name: 暖色胶片", 1)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(preset.DefaultStyle().GuideDoc()))); err != nil {
		t.Fatal(err)
	}
	writeCustomPair(t, root, text)
	return root
}

// writeCustomPair 把同一份内容写进 frame.md 与自定义说明书，保证两份 token 一致。
func writeCustomPair(t *testing.T, root, text string) {
	t.Helper()
	for _, name := range []string{"frame.md", filepath.FromSlash(preset.CustomStyle.GuideDoc())} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func tamperCustom(t *testing.T, root, from, to string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "frame.md"))
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(body), from, to, 1)
	if replaced == string(body) {
		t.Fatalf("frame.md 里找不到 %q", from)
	}
	writeCustomPair(t, root, replaced)
}

func TestStyleAcceptsFinalizedCustomStyle(t *testing.T) {
	for _, p := range preset.All() {
		if err := Style(customFixture(t, p.ID)); err != nil {
			t.Fatalf("%s 下定稿的自定义风格应通过：%v", p.ID, err)
		}
	}
}

// am init --style custom 刚下发的草稿必须被明确拒绝，并说清还差哪一步，
// 而不是报一串示例图缺失之类让人摸不着头脑的错误。
func TestStyleRejectsCustomDraftWithActionableMessage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join("..", "..", "assets", "styles", preset.CustomStyle.ID, preset.Default().ID)
	for _, name := range []string{"frame.md", filepath.FromSlash(preset.CustomStyle.GuideDoc())} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := Style(root)
	if err == nil || !strings.Contains(err.Error(), "定风格") || !strings.Contains(err.Error(), "style-brief.md") {
		t.Fatalf("草稿应报定风格阶段未完成并指向描述文件，实际：%v", err)
	}
}

func TestStyleRejectsMalformedCustomID(t *testing.T) {
	root := customFixture(t, preset.Default().ID)
	tamperCustom(t, root, "style_id: "+customID, "style_id: custom-Warm")
	if err := Style(root); err == nil || !strings.Contains(err.Error(), "未知风格") {
		t.Fatalf("不合命名规则的自定义 id 应报未知风格，实际：%v", err)
	}
}

func TestStyleRejectsCustomStyleWithoutGuide(t *testing.T) {
	root := customFixture(t, preset.Default().ID)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(preset.CustomStyle.GuideDoc()))); err != nil {
		t.Fatal(err)
	}
	if err := Style(root); err == nil {
		t.Fatal("自定义风格缺说明书应报错")
	}
}

func TestStyleChecksMoodRefs(t *testing.T) {
	const ref = "assets/style-guide/mood/film.png"
	cases := []struct {
		name    string
		path    string
		create  bool
		wantErr string
	}{
		{"存在的参照图", ref, true, ""},
		{"缺失的参照图", ref, false, "mood_refs"},
		{"逃出项目", "../film.png", false, "项目"},
		{"不是图片", "docs/notes.txt", true, "mood_refs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := customFixture(t, preset.Default().ID)
			tamperCustom(t, root, "forbidden:\n", "mood_refs:\n  - "+tc.path+"\nforbidden:\n")
			if tc.create {
				path := filepath.Join(root, filepath.FromSlash(tc.path))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := Style(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过：%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应报含 %q 的错误，实际：%v", tc.wantErr, err)
			}
		})
	}
}

func TestStyleRejectsMoodRefsThatAreNotAList(t *testing.T) {
	root := customFixture(t, preset.Default().ID)
	tamperCustom(t, root, "forbidden:\n", "mood_refs: film.png\nforbidden:\n")
	if err := Style(root); err == nil || !strings.Contains(err.Error(), "mood_refs") {
		t.Fatalf("mood_refs 不是数组应报错，实际：%v", err)
	}
}

// stubImageTools 在 PATH 里放 rsvg-convert、fonttools 与 magick 的桩，只负责创建产物文件。
func stubImageTools(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	magick := "#!/bin/sh\nfor last; do :; done\n: > \"$last\"\n"
	rsvg := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; : > \"$1\"; exit 0; fi\n  shift\ndone\nexit 1\n"
	for name, body := range map[string]string{"magick": magick, "rsvg-convert": rsvg, "fonttools": rsvg} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// 自定义风格的示例图源是项目自己的 SVG：不能拿内置模板覆盖它，也不做重着色。
func TestRegenerateExamplesRendersProjectSVGsForCustomStyle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	root := customFixture(t, preset.Default().ID)
	examples := filepath.Join(root, "assets", "style-guide", "examples")
	const marker = "<!-- project-owned -->"
	for _, id := range preset.ArchetypeIDs {
		if err := os.WriteFile(filepath.Join(examples, id+".svg"), []byte("<svg>"+marker+"</svg>"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Remove(filepath.Join(examples, id+".png"))
	}
	stubImageTools(t)
	var output bytes.Buffer
	if err := RegenerateExamples(root, &output); err != nil {
		t.Fatal(err)
	}
	for _, id := range preset.ArchetypeIDs {
		svg, err := os.ReadFile(filepath.Join(examples, id+".svg"))
		if err != nil || !strings.Contains(string(svg), marker) {
			t.Errorf("%s.svg 被改写了：%v", id, err)
		}
		if _, err := os.Stat(filepath.Join(examples, id+".png")); err != nil {
			t.Errorf("缺少 %s.png", id)
		}
	}
	if !strings.Contains(output.String(), "contact-sheet.png") {
		t.Fatalf("输出缺少 contact sheet：%s", output.String())
	}
}

func TestRegenerateExamplesListsEveryMissingCustomSVG(t *testing.T) {
	root := customFixture(t, preset.Default().ID)
	examples := filepath.Join(root, "assets", "style-guide", "examples")
	for _, id := range preset.ArchetypeIDs {
		os.Remove(filepath.Join(examples, id+".svg"))
	}
	err := RegenerateExamples(root, &bytes.Buffer{})
	if err == nil {
		t.Fatal("缺 SVG 应报错")
	}
	for _, id := range preset.ArchetypeIDs {
		if !strings.Contains(err.Error(), id+".svg") {
			t.Errorf("错误应列出全部缺失的 SVG，缺 %s：%v", id, err)
		}
	}
}
