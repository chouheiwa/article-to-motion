package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestRenderCanvasAndSafeAreaBlock(t *testing.T) {
	p, _ := preset.ByID("vertical-9x16")
	block, err := canvasAndSafeArea(p)
	if err != nil {
		t.Fatalf("canvasAndSafeArea: %v", err)
	}
	want := strings.Join([]string{
		"canvas:",
		"  width_px: 1080",
		"  height_px: 1920",
		"  fps: 30",
		"  orientation: vertical",
		"safe_area:",
		"  structural: {left_px: 40, right_px: 40, top_px: 96, bottom_px: 60}",
		"  main_content: {left_px: 88, right_px: 88, top_px: 120, bottom_px: 100}",
		"  critical_text: {left_px: 88, right_px: 180, top_px: 120, bottom_px: 260}",
		"  cover_title: {left_px: 88, right_px: 180, top_px: 260, bottom_px: 940}",
		"  subtitles: {left_px: 88, right_px: 180, top_px: 1470, bottom_px: 260}",
	}, "\n")
	if block != want {
		t.Errorf("生成块不符：\n实际:\n%s\n期望:\n%s", block, want)
	}
}

// frame.md 与说明书的 frontmatter 必须逐字节相同，validate.Style 会做 DeepEqual。
func TestRenderKeepsFrontmatterIdenticalAcrossFiles(t *testing.T) {
	p, _ := preset.ByID("vertical-9x16")
	for _, style := range preset.AllStyles() {
		t.Run(style.ID, func(t *testing.T) {
			files := renderStyle(t, p, style)
			frame := frontmatterOf(t, files["frame.md"])
			guide := frontmatterOf(t, files[style.GuideDoc()])
			if frame != guide {
				t.Error("frame.md 与说明书的 frontmatter 不一致")
			}
			if !strings.Contains(frame, "height_px: 1920") {
				t.Error("frontmatter 未写入 9:16 画布高度")
			}
		})
	}
}

func renderStyle(t *testing.T, p preset.Preset, style preset.Style) map[string][]byte {
	t.Helper()
	tpl, err := loadTemplates(style.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := RenderStyle(p, style, tpl)
	if err != nil {
		t.Fatalf("RenderStyle: %v", err)
	}
	return files
}

// 模板的 style_id / style_name 与风格表不一致时必须报错：validate 靠
// style_id 反查说明书文件名，对不上的项目会找不到自己的说明书。
func TestRenderStyleRejectsMismatchedIdentity(t *testing.T) {
	p, _ := preset.ByID("vertical-3x4")
	style := preset.DefaultStyle()
	tpl, err := loadTemplates(style.ID)
	if err != nil {
		t.Fatal(err)
	}
	tpl.Frontmatter = strings.Replace(tpl.Frontmatter, "style_id: "+style.ID, "style_id: other-v1", 1)
	if _, err := RenderStyle(p, style, tpl); err == nil {
		t.Fatal("style_id 与风格表不一致时应报错")
	}
}

// 每套注册的风格都必须有完整模板，否则 go generate 在中途失败。
func TestEveryStyleHasTemplates(t *testing.T) {
	for _, style := range preset.AllStyles() {
		if _, err := loadTemplates(style.ID); err != nil {
			t.Error(err)
		}
	}
}

// 自定义风格草稿要为每个画幅生成，且 style_id 必须是草稿身份：validate 与
// scene.Load 都靠它认出「定风格阶段还没做」。
func TestCustomDraftRendersForEveryCanvas(t *testing.T) {
	tpl, err := loadTemplates(preset.CustomStyle.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range preset.All() {
		files, err := RenderStyle(p, preset.CustomStyle, tpl)
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		front := frontmatterOf(t, files["frame.md"])
		if !strings.Contains(front, "style_id: "+preset.DraftStyleID+"\n") {
			t.Errorf("%s 的草稿 frame.md 缺少草稿 style_id", p.ID)
		}
		if _, ok := files[preset.CustomStyle.GuideDoc()]; !ok {
			t.Errorf("%s 缺少草稿说明书", p.ID)
		}
	}
}

func frontmatterOf(t *testing.T, body []byte) string {
	t.Helper()
	parts := strings.SplitN(string(body), "---", 3)
	if len(parts) != 3 {
		t.Fatalf("缺少 frontmatter")
	}
	return parts[1]
}

func TestRenderSubstitutesCanvasLabel(t *testing.T) {
	p, _ := preset.ByID("vertical-9x16")
	files, err := RenderCanvas(p, loadProduction())
	if err != nil {
		t.Fatal(err)
	}
	body := string(files["PROMPT-PRODUCTION.md"])
	if strings.Contains(body, "1080×1440") {
		t.Error("PROMPT-PRODUCTION.md 仍含 3:4 画幅")
	}
	if !strings.Contains(body, "1080×1920") {
		t.Error("PROMPT-PRODUCTION.md 未写入 9:16 画幅")
	}
	if strings.Contains(body, "{{CANVAS}}") {
		t.Error("占位符未被替换")
	}
}

// 模板标记丢失时必须报错，否则会静默产出没有 canvas 段的 frame.md。
func TestRenderRejectsTemplateWithoutMarker(t *testing.T) {
	p, _ := preset.ByID("vertical-3x4")
	tpl, err := loadTemplates(preset.DefaultStyle().ID)
	if err != nil {
		t.Fatal(err)
	}
	tpl.Frontmatter = "schema_version: 1\n"
	if _, err := RenderStyle(p, preset.DefaultStyle(), tpl); err == nil {
		t.Fatal("缺少标记时应报错")
	}
}

func TestRepoRootWalksUpToGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)
	got, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	// macOS 上 t.TempDir() 位于 /var，是 /private/var 的符号链接。
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("repoRoot = %s，期望 %s", gotResolved, wantResolved)
	}
}

func TestRepoRootFailsWithoutGoMod(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := repoRoot(); err == nil {
		t.Fatal("找不到 go.mod 时应报错")
	}
}

// 缺手写 SVG 时必须报错并点名文件，而不是产出一套残缺示例。
func TestRenderExamplesReportsMissingSVG(t *testing.T) {
	p, _ := preset.ByID("vertical-3x4")
	err := renderExamples(filepath.Join("..", "..", ".."), t.TempDir(), p, preset.DefaultStyle(), io.Discard)
	if err == nil {
		t.Fatal("缺少 SVG 时应报错")
	}
	if !strings.Contains(err.Error(), "proposition.svg") {
		t.Errorf("错误应点名缺失文件，实际：%v", err)
	}
}

func TestCanvasAndSafeAreaRejectsBrokenAnchor(t *testing.T) {
	p := preset.Preset{
		ID:       "broken",
		Canvas:   preset.Canvas{WidthPx: 1080, HeightPx: 1440, FPS: 30, Orientation: "vertical"},
		SafeArea: []preset.Box{{Name: "x", Anchor: preset.Anchor("middle")}},
	}
	if _, err := canvasAndSafeArea(p); err == nil {
		t.Fatal("非法 anchor 应报错")
	}
}

// 生成的 frame.md 必须能被 validate 的 frontmatter 切分逻辑正确解析。
func TestRenderProducesParseableFrontmatter(t *testing.T) {
	for _, p := range preset.All() {
		t.Run(p.ID, func(t *testing.T) {
			body := string(renderStyle(t, p, preset.DefaultStyle())["frame.md"])
			if !strings.HasPrefix(body, "---\n") {
				t.Error("frame.md 未以 --- 开头")
			}
			if strings.Count(body, "\n---") < 1 {
				t.Error("frame.md 缺少 frontmatter 结束标记")
			}
			if strings.Contains(body, canvasMarker) {
				t.Error("canvas 标记未被替换")
			}
		})
	}
}

// outputs 列出 generate 应写出的全部文本产物（相对 assets/）。
func outputs() []string {
	var out []string
	for _, p := range preset.All() {
		out = append(out, filepath.Join("presets", p.ID, "PROMPT-PRODUCTION.md"))
		for _, style := range generatedStyles() {
			dir := filepath.Join("styles", style.ID, p.ID)
			out = append(out, filepath.Join(dir, "frame.md"), filepath.Join(dir, filepath.FromSlash(style.GuideDoc())))
		}
	}
	return out
}

func TestGenerateWritesEveryPresetIntoRoot(t *testing.T) {
	root := t.TempDir()
	var log bytes.Buffer
	if err := generate(root, preset.All(), generatedStyles(), false, &log); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, name := range outputs() {
		if _, err := os.ReadFile(filepath.Join(root, "assets", name)); err != nil {
			t.Fatalf("缺少 %s: %v", name, err)
		}
	}
	for _, p := range preset.All() {
		body, _ := os.ReadFile(filepath.Join(root, "assets", "presets", p.ID, "PROMPT-PRODUCTION.md"))
		if !strings.Contains(string(body), p.Canvas.Label()) {
			t.Errorf("%s 的 PROMPT-PRODUCTION.md 未写入画幅 %s", p.ID, p.Canvas.Label())
		}
	}
	// 日志按路径排序，便于 diff 比对。
	want := "generated assets/styles/" + preset.DefaultStyle().ID + "/vertical-9x16/frame.md"
	if !strings.Contains(log.String(), want) {
		t.Errorf("日志缺少 %s：%s", want, log.String())
	}
}

// generate 的产物必须与仓库里签入的一致——这正是 CI 幂等门要保证的事。
func TestGenerateMatchesCommittedAssets(t *testing.T) {
	root := t.TempDir()
	if err := generate(root, preset.All(), generatedStyles(), false, io.Discard); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, name := range outputs() {
		fresh, err := os.ReadFile(filepath.Join(root, "assets", name))
		if err != nil {
			t.Fatal(err)
		}
		committed, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fresh, committed) {
			t.Errorf("%s 与签入产物不一致，需重跑 go generate", name)
		}
	}
}

func TestSelectStyles(t *testing.T) {
	all, err := selectStyles("")
	if err != nil || len(all) != len(preset.AllStyles())+1 {
		t.Fatalf("空 id 应返回全部内置风格加自定义草稿：%v %v", len(all), err)
	}
	draft, err := selectStyles(preset.DraftStyleID)
	if err != nil || len(draft) != 1 || draft[0].ID != preset.DraftStyleID {
		t.Fatalf("应能单独生成自定义草稿：%v %v", draft, err)
	}
	if _, err := selectStyles("no-such-style"); err == nil {
		t.Fatal("未知风格应报错")
	}
}

func TestSelectTargets(t *testing.T) {
	all, err := selectTargets("")
	if err != nil || len(all) != len(preset.All()) {
		t.Fatalf("空 id 应返回全部：%v %v", len(all), err)
	}
	one, err := selectTargets("vertical-9x16")
	if err != nil || len(one) != 1 || one[0].ID != "vertical-9x16" {
		t.Fatalf("单选失败：%v %v", one, err)
	}
	if _, err := selectTargets("square-1x1"); err == nil {
		t.Fatal("未知 id 应报错")
	}
}

// TestProductionPromptPinsYearReading：TTS 会把「2025 年」读成「两千零二十五年」，
// 而且同一音色对不同年份的处理不一致，只能在生成前逐个钉住。两家 TTS 的
// 做法与试听核对项都要在，删掉任何一处都会让年份读法回到碰运气。
func TestProductionPromptPinsYearReading(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{
		"四位数年份一律逐位读",
		"不得读成「一千九百七十二年」「两千零二十五年」",
		`--pronunciation "2025年/二零二五年"`,
		"直接写成逐位汉字",
		"每个四位年份是否逐位读出",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION 模板缺少年份读法约定：%q", want)
		}
	}
}

// 风格模板里随画幅变化的像素与措辞必须按画幅填入：竖屏保持原值，横屏换成
// 横屏的推导值与平台措辞，并追加横屏构图要点。
func TestRenderStyleFillsCanvasPlaceholders(t *testing.T) {
	style := preset.DefaultStyle()
	vertical, _ := preset.ByID("vertical-3x4")
	landscape, _ := preset.ByID("landscape-16x9")
	v := string(renderStyle(t, vertical, style)["frame.md"])
	l := string(renderStyle(t, landscape, style)["frame.md"])
	for _, want := range []string{"content_width_px: 904", "竖屏", "抖音右侧互动栏"} {
		if !strings.Contains(v, want) {
			t.Errorf("竖屏 frame.md 缺少 %q", want)
		}
	}
	for _, want := range []string{"content_width_px: 1680", "横屏", "播放器底部控制栏", "## 横屏构图要点"} {
		if !strings.Contains(l, want) {
			t.Errorf("横屏 frame.md 缺少 %q", want)
		}
	}
	for _, leaked := range []string{"{{", "抖音"} {
		if strings.Contains(l, leaked) {
			t.Errorf("横屏 frame.md 不应出现 %q", leaked)
		}
	}
}

func TestSubstituteCanvasRejectsUnknownPlaceholder(t *testing.T) {
	p, _ := preset.ByID("vertical-3x4")
	if _, err := substituteCanvas(p, "x {{NO_SUCH_TOKEN}} y"); err == nil {
		t.Fatal("未知占位符应报错，而不是原样留在产物里")
	}
	got, err := substituteCanvas(p, "{{CONTENT_LEFT_PX+8}}/{{CONTENT_WIDTH_PX-16}}")
	if err != nil || got != "96/888" {
		t.Fatalf("偏移计算错误：%q %v", got, err)
	}
}

// 自定义风格的草稿只有靠编排提示词里的定风格阶段才会被定稿；这一节被删掉，
// 项目会卡在 scene.Load 的草稿拦截上，而编排工具不知道该做什么。
func TestProductionPromptCarriesCustomStyleStage(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{preset.DraftStyleID, "docs/style-brief.md", "am style add-font", "am validate style --regenerate-examples", "mood_refs", "$imagegen", preset.CustomStyle.GuideDoc()} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION.md 的定风格阶段缺少 %q", want)
		}
	}
}

// 各平台封面比例不同（抖音 4:3 + 3:4、B 站 16:10……）；这一节被删掉，交付物就只剩第 0 帧。
func TestProductionPromptRequiresPlatformCovers(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{"douyin", "xiaohongshu", "channels", "bilibili", "zhihu", "16:10", "安全区", "covers/src/", "cover.images", "designed_image"} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION.md 的平台封面一节缺少 %q", want)
		}
	}
}

// 整片处理方案是分镜与每个镜头 prompt.md 的上游；它若开始重新定义配色和字体，
// 就会和 frame.md 争夺视觉一致性的唯一来源。
func TestProductionPromptCarriesTreatment(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{
		"### 整片处理方案",
		"production/treatment.md",
		"钩子—揭示—收束",
		"持续物件",
		"配色、字体、安全区和版式骨架仍由 `frame.md` 管，`treatment.md` 不得重新定义它们",
		"先改 `treatment.md`",
		"- `production/treatment.md` 整片处理方案",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION.md 的整片处理方案缺少 %q", want)
		}
	}
	if strings.Index(body, "### 整片处理方案") > strings.Index(body, "## 第三阶段") {
		t.Error("整片处理方案必须在第三阶段（TTS）之前")
	}
}

// 镜头各自渲染、互相看不到，跨镜头交接只能靠 prompt.md 里写明的入场与出场状态，
// 整片复查要拿 seam-NN.png 对这两段约定。
func TestProductionPromptDeclaresSceneHandoff(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{
		"在 `prompt.md` 中写明镜头交接：入场状态",
		"出场状态（最后一帧留给下一镜头",
		"声明的出场状态与入场状态",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION.md 缺少镜头交接约定 %q", want)
		}
	}
}

// amix 默认按输入路数压低每一路，混音偏小后只能靠响度规范化硬拉。
func TestProductionPromptMixesWithoutAmixNormalize(t *testing.T) {
	body := loadProduction()
	for _, want := range []string{"`amix`", "`normalize=0`", "`event_id`", "`phrase_id`"} {
		if !strings.Contains(body, want) {
			t.Errorf("PROMPT-PRODUCTION.md 的声音设计缺少 %q", want)
		}
	}
}
