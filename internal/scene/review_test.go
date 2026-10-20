package scene

import (
	"github.com/chouheiwa/articale-to-motion/internal/determinism"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnimationPromptHasNoNumericQuotas：数量配额是可逐项打勾的指标，渲染器
// 会为了达标堆砌与文案无关的动效。强制贯穿全镜的环境动效与「1.5 秒不得静止」
// 还与 frame.md 的 ambient_per_scene_max: 1 和风格说明书的稳定停留要求冲突。
func TestAnimationPromptHasNoNumericQuotas(t *testing.T) {
	for _, dir := range []string{"", "/proj/.agents/skills"} {
		section := animationPrompt(dir)
		for _, banned := range []string{"至少组合 3 条", "3 个不同分类", "至少再动用 2 个属性", "45 帧", "贯穿整个镜头时长"} {
			if strings.Contains(section, banned) {
				t.Errorf("skillsDir=%q 时，动效提示词仍含配额 %q：\n%s", dir, banned, section)
			}
		}
	}
}

// TestBuildPromptRequiresSnapshotSelfReview：产物校验只查规格，观感问题只能靠
// 渲染器正式渲染前看自己的静帧发现。
func TestBuildPromptRequiresSnapshotSelfReview(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"画面自查（强制", "snapshot --at", "--describe false", "至少完成一轮", "越出画布安全区"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词缺少 %q", want)
		}
	}
	if strings.Contains(prompt, "上一次尝试失败") {
		t.Error("首次渲染不应出现重试说明")
	}
}

// TestHyperFramesCLIPromptAllowsSnapshot：自查靠 snapshot；preview 在无头环境里没用。
// --describe 默认会把画面发给外部视觉服务，必须显式关掉。
func TestHyperFramesCLIPromptAllowsSnapshot(t *testing.T) {
	section := hyperFramesCLIPrompt("")
	for _, want := range []string{"snapshot", "--describe false"} {
		if !strings.Contains(section, want) {
			t.Errorf("CLI 提示词缺少 %q：\n%s", want, section)
		}
	}
	if strings.Contains(section, "render、check、preview") {
		t.Errorf("CLI 提示词仍把 preview 列为可用命令：\n%s", section)
	}
}

// TestHyperFramesCreativePromptStaysWithinSingleScene：上游 creative 技能还覆盖
// 整片节拍；本项目只把它当作可选的单镜头构图参考，视觉方向保持自由。
func TestHyperFramesCreativePromptStaysWithinSingleScene(t *testing.T) {
	for _, dir := range []string{"", "/proj/.agents/skills"} {
		section := hyperFramesCreativePrompt(dir)
		for _, want := range []string{"house-style.md", "video-composition.md", "design-adherence.md", "可选的构图参考", "配色、字体、版式与动效由当前镜头自主决定", "不做整片规划"} {
			if !strings.Contains(section, want) {
				t.Errorf("skillsDir=%q 时，creative 提示词缺少 %q：\n%s", dir, want, section)
			}
		}
		for _, banned := range []string{"不另选色板", "design-picker", "以视觉规范为准"} {
			if strings.Contains(section, banned) {
				t.Errorf("skillsDir=%q 时，creative 提示词仍限制视觉选择 %q：\n%s", dir, banned, section)
			}
		}
	}
}

// TestBuildPromptIgnoresStyleExamples：风格示例不再成为渲染器的强制参照。
func TestBuildPromptPointsAtStyleExamples(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello","style_guide":"frame.md"}`)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(frameWithCanvas(1080, 1920)), 0o644)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"scene_archetypes", "example_png", "不照抄", "display_stack"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("提示词不应引用风格示例 %q", banned)
		}
	}
}

// TestBuildPromptCarriesRetryNote：重试时把失败原因交给渲染器，不再原样重发。
func TestBuildPromptCarriesRetryNote(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	retried := s.WithRetryNote("- 失败原因：渲染器退出码 3")
	if s.RetryNote != "" {
		t.Fatal("WithRetryNote 修改了原镜头")
	}
	prompt, err := BuildPrompt(retried, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"上一次尝试失败", "渲染器退出码 3", "不要原样重复"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词缺少 %q", want)
		}
	}
}

// TestBuildPromptAnnouncesDeterminismGate：源码扫描判失败不重试，渲染器必须事先
// 知道哪些写法会被拦、豁免怎么写；豁免标记必须与扫描器认的是同一个字符串。
func TestBuildPromptAnnouncesDeterminismGate(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"确定性（强制", "Math.random()", "requestAnimationFrame", "完整版本号", determinism.AllowMarker} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词缺少 %q", want)
		}
	}
}

// TestBuildPromptCarriesRenderWorkerBudget：并发渲染时每镜头的 worker 数写进
// 提示词；单镜头渲染不写，沿用上游 auto。
func TestBuildPromptCarriesRenderWorkerBudget(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	solo, _ := BuildPrompt(s, nil)
	if strings.Contains(solo, "--workers") {
		t.Error("单镜头渲染不应限制 worker 数")
	}
	shared, _ := BuildPrompt(s.WithRenderWorkers(3), nil)
	if !strings.Contains(shared, "--workers 3") {
		t.Error("并发渲染时提示词应带 --workers 3")
	}
	if s.RenderWorkers != 0 {
		t.Error("WithRenderWorkers 不得修改原镜头")
	}
}

// TestSelfReviewCoversMotionNotOnlyStills：静帧看不出动作。代码写的动画最常见的
// 毛病——一帧蹦出、匀速、齐动、信息点抢拍——只有连续帧能看出来。
func TestSelfReviewCoversMotionNotOnlyStills(t *testing.T) {
	for _, want := range []string{"连续截取该段的每一帧", "一帧蹦出", "同一帧、以同一幅度齐动", "上一个信息点读完之前", "自查量下限"} {
		if !strings.Contains(selfReviewSection, want) {
			t.Errorf("画面自查缺少 %q", want)
		}
	}
}
