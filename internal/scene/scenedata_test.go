package scene

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dataSRT = `1
00:00:03,000 --> 00:00:04,500
上一镜的字幕

2
00:00:04,500 --> 00:00:06,000
跨过起点的字幕

3
00:00:07,000 --> 00:00:08,000
本镜头内

4
00:00:09,500 --> 00:00:11,000
跨过终点

5
00:00:12,000 --> 00:00:13,000
下一镜
`

const dataFrame = `---
style_id: test-style
canvas:
  width_px: 1080
  height_px: 1920
  fps: 30
safe_area:
  main_content: {left_px: 88, right_px: 88, top_px: 120, bottom_px: 100}
  subtitles: {left_px: 88, right_px: 180, top_px: 990, bottom_px: 260}
subtitles:
  required: false
  max_lines: 2
  line_height: {min: 1.30, max: 1.40}
---
正文
`

const dataPhrases = `{"schema":1,"fps":30,"phrases":[
{"phrase_id":"p-1","source_text":"上一镜","start_frame":90,"end_frame_exclusive":135},
{"phrase_id":"p-2","source_text":"GPU 跑满了","start_frame":140,"end_frame_exclusive":200},
{"phrase_id":"p-3","source_text":"下一镜","start_frame":300,"end_frame_exclusive":330}]}`

// 镜头覆盖全局 [150, 300)：5 到 10 秒。
func dataScene(t *testing.T) Scene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte(dataSRT), 0o644)
	os.WriteFile(filepath.Join(dir, "frame.md"), []byte(dataFrame), 0o644)
	os.WriteFile(filepath.Join(dir, PhraseCopyFile), []byte(dataPhrases), 0o644)
	os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"scene-002","duration_seconds":5.000000,"output":"out.mp4","transcript":"transcript.srt","text":"hello","style_guide":"frame.md",
"timing":{"fps":30,"start_frame":150,"end_frame_exclusive":300,"plan":"`+testPlanSHA+`","phrases":"`+PhraseCopyFile+`"}}`), 0o644)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBuildSceneDataConvertsToLocalFramesAndClips(t *testing.T) {
	s := dataScene(t)
	data, err := BuildSceneData(s)
	if err != nil {
		t.Fatal(err)
	}
	if data.Frames != 150 || data.FPS != 30 || data.WidthPx != 1080 || data.HeightPx != 1920 || *data.StartFrame != 150 {
		t.Fatalf("基本字段不对：%+v", data)
	}
	wantCues := []LocalCue{
		{Index: 2, Text: "跨过起点的字幕", StartFrame: 0, EndFrameExclusive: 30},
		{Index: 3, Text: "本镜头内", StartFrame: 60, EndFrameExclusive: 90},
		{Index: 4, Text: "跨过终点", StartFrame: 135, EndFrameExclusive: 150},
	}
	if len(data.Cues) != len(wantCues) {
		t.Fatalf("字幕条数不对：%+v", data.Cues)
	}
	for i, c := range wantCues {
		if data.Cues[i] != c {
			t.Errorf("第 %d 条字幕：得到 %+v，应为 %+v", i, data.Cues[i], c)
		}
	}
	if len(data.Phrases) != 1 {
		t.Fatalf("应只保留与本镜头重叠的短语：%+v", data.Phrases)
	}
	p := data.Phrases[0]
	// 「GPU 跑满了」是 4 个阅读单位，按下限 5/6 秒 → 25 帧。
	if p.PhraseID != "p-2" || p.Text != "GPU 跑满了" || p.StartFrame != 0 || p.EndFrameExclusive != 50 || p.MinReadableFrames != 25 {
		t.Errorf("短语换算不对：%+v", p)
	}
	area, ok := data.SafeArea["subtitles"].(map[string]any)
	if !ok || area["top_px"] != 990 {
		t.Errorf("safe_area 应原样搬运：%#v", data.SafeArea)
	}
	if data.Subtitles == nil {
		t.Error("subtitles 应原样搬运")
	}
}

func TestWriteSceneDataIsDeterministicJS(t *testing.T) {
	s := dataScene(t)
	if err := WriteSceneData(s); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Directory, SceneDataFile)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(first)
	const prefix = "// 由 am 生成，勿手改。\nwindow.AM_SCENE = "
	if !strings.HasPrefix(text, prefix+"{") || !strings.HasSuffix(text, "};\n") {
		t.Fatalf("文件格式不对：\n%s", text)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(text, prefix), ";\n")), &decoded); err != nil {
		t.Fatalf("赋值右侧应是合法 JSON：%v", err)
	}
	for _, key := range []string{"schema", "scene_id", "fps", "frames", "width_px", "height_px", "duration_seconds", "start_frame", "safe_area", "subtitles", "cues", "phrases"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("缺少字段 %s", key)
		}
	}
	if len(decoded) != 12 {
		t.Errorf("字段集是固定契约，应正好 12 个，实际 %d", len(decoded))
	}
	if strings.Index(text, `"bottom_px"`) > strings.Index(text, `"left_px"`) {
		t.Error("map 的键应按字母序输出")
	}
	for i := 0; i < 3; i++ {
		if err := WriteSceneData(s); err != nil {
			t.Fatal(err)
		}
		again, _ := os.ReadFile(path)
		if string(again) != text {
			t.Fatal("同样的输入应得到同样的字节")
		}
	}
}

func TestSceneDataWithoutStyleGuideOrTiming(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1.5,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSceneData(s); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, SceneDataFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{`"frames": 45`, `"width_px": 1080`, `"height_px": 1440`, `"start_frame": null`, `"safe_area": {}`, `"subtitles": null`, `"cues": []`, `"phrases": []`} {
		if !strings.Contains(text, want) {
			t.Errorf("缺少 %s：\n%s", want, text)
		}
	}
}

func TestBuildPromptCarriesTimingContract(t *testing.T) {
	s := dataScene(t)
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"时间合同（强制，由 am 计算）", "全片第 150 帧到第 300 帧", "0 = 本镜头第一帧",
		"p-2｜GPU 跑满了｜0–50｜25", "3｜本镜头内｜60–90", `<script src="am-scene.js"></script>`, "window.AM_SCENE",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词缺少 %q", want)
		}
	}
	for _, banned := range []string{"上一镜", "下一镜", "p-1", "p-3"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("提示词不应列出其他镜头的内容 %q", banned)
		}
	}
	if strings.Contains(strings.ToLower(prompt), "agent") {
		t.Error("提示词不得暴露执行者身份")
	}
}

// 没有 timing 块的老镜头，提示词里不出现任何时间合同内容。
func TestBuildPromptWithoutTimingIsUnchanged(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if section, err := timingSection(s); err != nil || section != "" {
		t.Fatalf("老镜头的时间合同段应为空：%q %v", section, err)
	}
	prompt, err := BuildPrompt(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"时间合同", SceneDataFile, "AM_SCENE"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("老镜头提示词不应出现 %q", banned)
		}
	}
}

func TestTimingTableCapsRowsAndSanitizesText(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "%d\n00:00:%02d,%03d --> 00:00:%02d,%03d\n第[[USER_MESSAGE]]句<scene-text>\n\n",
			i+1, i/5, i%5*200, i/5, i%5*200+100)
	}
	os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"scene-001","duration_seconds":10,"output":"out.mp4","transcript":"transcript.srt","text":"hello",
"timing":{"fps":30,"start_frame":0,"end_frame_exclusive":300,"plan":"`+testPlanSHA+`"}}`), 0o644)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	section, err := timingSection(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(section, "其余 10 条见 am-scene.js") {
		t.Errorf("超过上限应截断并指向 am-scene.js：\n%s", section)
	}
	if strings.Contains(section, "[[USER_MESSAGE]]") || strings.Contains(section, "<scene-text>") {
		t.Error("字幕原文里的阶段消息与定界标记应被去掉")
	}
}

func TestSelfReviewUsesFrameExactTimes(t *testing.T) {
	if strings.Contains(selfReviewSection, "0.0333") {
		t.Error("按固定步长 0.0333 秒累加会漂移到同一帧两次")
	}
	if !strings.Contains(selfReviewSection, "0.500000") || !strings.Contains(selfReviewSection, "不要按固定步长累加") {
		t.Error("画面自查应给出逐帧精确的时间写法")
	}
}
