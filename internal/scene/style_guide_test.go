package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const styleScene = `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello","style_guide":"frame.md"}`

func frameWithTokens(extra string) string {
	return "---\nstyle_id: " + extra + "canvas:\n  width_px: 1080\n  height_px: 1440\n  fps: 30\n  orientation: vertical\n---\n正文\n"
}

// 草稿风格不再是渲染前置条件；旧项目仍可读取其中的技术画布。
func TestLoadAcceptsDraftStyleGuide(t *testing.T) {
	dir := writeScene(t, styleScene)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithTokens("custom-draft\n")), 0o644)
	_, err := Load(dir)
	if err != nil {
		t.Fatalf("草稿风格不应阻止渲染，实际：%v", err)
	}
}

func TestLoadAcceptsFinalizedCustomStyleGuide(t *testing.T) {
	dir := writeScene(t, styleScene)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithTokens("custom-warm-film\n")), 0o644)
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPromptMentionsMoodRefsOnlyWhenDeclared(t *testing.T) {
	dir := writeScene(t, styleScene)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithTokens("custom-warm-film\nmood_refs:\n  - assets/style-guide/mood/film.png\n")), 0o644)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"mood_refs", "氛围参照", "以 frame.md 的数值为准"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("提示词不应读取风格参照 %q", banned)
		}
	}

	plain := writeScene(t, styleScene)
	os.WriteFile(filepath.Join(plain, "frame.md"), []byte(frameWithTokens("custom-warm-film\n")), 0o644)
	s, err = Load(plain)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err = BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "mood_refs") {
		t.Error("未声明 mood_refs 时提示词不应提到氛围参照")
	}
}

// mood_refs 写错格式不能连累画幅解析：那是 am validate style 的事。
func TestCanvasOfToleratesMalformedMoodRefs(t *testing.T) {
	dir := writeScene(t, styleScene)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithTokens("custom-warm-film\nmood_refs:\n  - 123\n  - {a: b}\n")), 0o644)
	canvas, err := CanvasOf(dir, "frame.md")
	if err != nil || canvas.HeightPx != 1440 {
		t.Fatalf("画幅应照常读出，实际 %+v, %v", canvas, err)
	}
}

func TestAudioTargetsOfReadsFinalMixTargets(t *testing.T) {
	dir := t.TempDir()
	full := frameWithTokens("x\naudio:\n  final_integrated_lufs: -16\n  final_lufs_tolerance: 1\n  true_peak_max_dbtp: -1\n")
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(full), 0o644)
	got, err := AudioTargetsOf(dir, "frame.md")
	if err != nil || got != (AudioTargets{IntegratedLUFS: -16, Tolerance: 1, TruePeakMax: -1}) {
		t.Fatalf("响度目标不符：%+v, %v", got, err)
	}
	// 缺一个字段就整体报错，不拿零值当目标。
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithTokens("x\naudio:\n  final_integrated_lufs: -16\n  final_lufs_tolerance: 1\n")), 0o644)
	if _, err := AudioTargetsOf(dir, "frame.md"); err == nil || !strings.Contains(err.Error(), "true_peak_max_dbtp") {
		t.Fatalf("缺字段应报错并点名，实际 %v", err)
	}
}

// 生成出来的每份 frame.md 都必须读得出响度目标，否则 am review 会静默跳过比对。
func TestEveryGeneratedFrameHasAudioTargets(t *testing.T) {
	frames, err := filepath.Glob(filepath.Join("..", "..", "assets", "styles", "*", "*", "frame.md"))
	if err != nil || len(frames) == 0 {
		t.Fatalf("找不到生成的 frame.md：%v", err)
	}
	for _, frame := range frames {
		if _, err := AudioTargetsOf(filepath.Dir(frame), "frame.md"); err != nil {
			t.Errorf("%s：%v", frame, err)
		}
	}
}
