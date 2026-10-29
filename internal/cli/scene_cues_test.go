package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/scene"
)

// 四条字幕，30fps 下：[0,30) [45,90) [90,120) [150,180)。
const cuesSRT = `1
00:00:00,000 --> 00:00:01,000
第一句

2
00:00:01,500 --> 00:00:03,000
第二句

3
00:00:03,000 --> 00:00:04,000
第三句

4
00:00:05,000 --> 00:00:06,000
第四句
`

const cuesPhrases = `{"schema":1,"fps":30,"phrases":[
{"phrase_id":"p-1","source_text":"第一句","start_frame":0,"end_frame_exclusive":30},
{"phrase_id":"p-2","source_text":"第二句","start_frame":45,"end_frame_exclusive":90},
{"phrase_id":"p-4","source_text":"第四句","start_frame":150,"end_frame_exclusive":180}]}
`

// cuesProject 建一个三镜头项目，返回项目根。scene-001 的 scene.json 带一个
// renderer 字段，并故意把 text 放在 id 前面，用来检查字段顺序被保留。
func cuesProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, id := range []string{"scene-001", "scene-002", "scene-003"} {
		dir := filepath.Join(root, "scenes", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte(cuesSRT), 0o644)
		body := `{"id":"` + id + `","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"文案"}`
		if id == "scene-001" {
			body = `{"text":"文案 <b>&</b>","id":"scene-001","renderer":"codex","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt"}`
		}
		os.WriteFile(filepath.Join(dir, "scene.json"), []byte(body), 0o644)
	}
	os.MkdirAll(filepath.Join(root, "production"), 0o755)
	os.WriteFile(filepath.Join(root, "transcription-production.srt"), []byte(cuesSRT), 0o644)
	os.WriteFile(filepath.Join(root, "production", "phrase-timeline.json"), []byte(cuesPhrases), 0o644)
	writePlan(t, root, `{"schema":1,"scenes":[
  {"id":"scene-001","dir":"scenes/scene-001"},
  {"id":"scene-002","dir":"scenes/scene-002","first_subtitle":2},
  {"id":"scene-003","dir":"scenes/scene-003","first_phrase":"p-4","cut":"prev_end"}]}`)
	return root
}

func writePlan(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "production", "scene-plan.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCues(root string, extra ...string) (string, error) {
	var out bytes.Buffer
	cmd := newSceneCuesCommand(&out)
	args := append([]string{
		"--project-root", root,
		"--plan", filepath.Join(root, "production", "scene-plan.json"),
		"--srt", filepath.Join(root, "transcription-production.srt"),
		"--phrases", filepath.Join(root, "production", "phrase-timeline.json"),
		"--total-frames", "240",
	}, extra...)
	cmd.SetArgs(append(args, filepath.Join(root, "scenes")))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestSceneCuesWritesDurationsAndTiming(t *testing.T) {
	root := cuesProject(t)
	out, err := runCues(root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := map[string][2]int{"scene-001": {0, 45}, "scene-002": {45, 90}, "scene-003": {90, 240}}
	for id, span := range want {
		s, err := scene.Load(filepath.Join(root, "scenes", id))
		if err != nil {
			t.Fatalf("%s 写回后应仍能加载：%v", id, err)
		}
		if s.Timing == nil || s.Timing.StartFrame != span[0] || s.Timing.EndFrameExclusive != span[1] || s.Timing.FPS != 30 {
			t.Errorf("%s 的 timing 块不对：%+v", id, s.Timing)
			continue
		}
		if s.Timing.Phrases != scene.PhraseCopyFile || len(s.Timing.Plan) != 64 {
			t.Errorf("%s 的 timing 块缺少短语或计划摘要：%+v", id, s.Timing)
		}
		if err := scene.VerifyTimingInputs(s); err != nil {
			t.Errorf("%s 写回后应通过时间合同校验：%v", id, err)
		}
		copied, _ := os.ReadFile(filepath.Join(s.Directory, scene.PhraseCopyFile))
		if string(copied) != cuesPhrases {
			t.Errorf("%s 的短语时间轴副本应与原件逐字节相同", id)
		}
	}
	body, _ := os.ReadFile(filepath.Join(root, "scenes", "scene-002", "scene.json"))
	if !strings.Contains(string(body), `"duration_seconds": 1.500000`) {
		t.Errorf("duration_seconds 应写成 6 位小数：\n%s", body)
	}
	first, _ := os.ReadFile(filepath.Join(root, "scenes", "scene-001", "scene.json"))
	text := string(first)
	order := []string{`"text"`, `"id"`, `"renderer": "codex"`, `"duration_seconds": 1.500000`, `"output"`, `"transcript"`, `"timing"`}
	last := -1
	for _, key := range order {
		at := strings.Index(text, key)
		if at <= last {
			t.Fatalf("字段顺序应保留、timing 追加在末尾，%s 位置不对：\n%s", key, text)
		}
		last = at
	}
	if !strings.Contains(text, `"文案 <b>&</b>"`) {
		t.Errorf("其他字段的原文应原样保留：\n%s", text)
	}
	if !strings.Contains(out, "scene-003") || !strings.Contains(out, "5.000000") {
		t.Errorf("应打印镜头区间表：\n%s", out)
	}

	// 无变化的重跑不改动文件，--check 通过。
	stat, _ := os.Stat(filepath.Join(root, "scenes", "scene-001", "scene.json"))
	if out, err := runCues(root); err != nil {
		t.Fatalf("重跑失败：%v\n%s", err, out)
	}
	again, _ := os.Stat(filepath.Join(root, "scenes", "scene-001", "scene.json"))
	if !again.ModTime().Equal(stat.ModTime()) {
		t.Error("内容不变时不应重写 scene.json")
	}
	if out, err := runCues(root, "--check"); err != nil {
		t.Fatalf("--check 应通过：%v\n%s", err, out)
	}
}

func TestSceneCuesCheckDetectsHandEdits(t *testing.T) {
	root := cuesProject(t)
	if out, err := runCues(root); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	path := filepath.Join(root, "scenes", "scene-002", "scene.json")
	body, _ := os.ReadFile(path)
	edited := strings.Replace(string(body), `"duration_seconds": 1.500000`, `"duration_seconds": 1.533333`, 1)
	os.WriteFile(path, []byte(edited), 0o644)
	os.WriteFile(filepath.Join(root, "scenes", "scene-003", scene.PhraseCopyFile), []byte(`{}`), 0o644)

	_, err := runCues(root, "--check")
	if err == nil {
		t.Fatal("手改时长后 --check 应失败")
	}
	for _, want := range []string{"scene-002", "46 帧", "45 帧", "scene-003", scene.PhraseCopyFile} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误应列出 %q：%v", want, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != edited {
		t.Error("--check 不得写文件")
	}

	// 计划改了而镜头没重跑：plan 摘要不再相符。
	if out, err := runCues(root); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	writePlan(t, root, `{"schema":1,"scenes":[
  {"id":"scene-001","dir":"scenes/scene-001"},
  {"id":"scene-002","dir":"scenes/scene-002","first_subtitle":2,"cut":"mid_gap"},
  {"id":"scene-003","dir":"scenes/scene-003","first_phrase":"p-4","cut":"prev_end"}]}`)
	if _, err := runCues(root, "--check"); err == nil || !strings.Contains(err.Error(), "timing 块") {
		t.Errorf("计划变化后 --check 应报 timing 块不符：%v", err)
	}
}

func TestSceneCuesRejectsPlanThatDoesNotMatchScenes(t *testing.T) {
	root := cuesProject(t)
	cases := map[string]string{
		`{"schema":1,"scenes":[{"id":"scene-001","dir":"scenes/scene-001"},{"id":"scene-002","dir":"scenes/scene-002","first_subtitle":2}]}`:                                                                "3 个镜头目录",
		`{"schema":1,"scenes":[{"id":"scene-001","dir":"scenes/scene-001"},{"id":"scene-003","dir":"scenes/scene-003","first_subtitle":2},{"id":"scene-002","dir":"scenes/scene-002","first_subtitle":4}]}`: "字典序",
		`{"schema":1,"scenes":[{"id":"scene-001","dir":"scenes/scene-001"},{"id":"wrong","dir":"scenes/scene-002","first_subtitle":2},{"id":"scene-003","dir":"scenes/scene-003","first_subtitle":4}]}`:     "id wrong",
		`{"schema":1,"scenes":[{"id":"scene-001","dir":"scenes/scene-001"},{"id":"scene-002","dir":"scenes/nope","first_subtitle":2},{"id":"scene-003","dir":"scenes/scene-003","first_subtitle":4}]}`:      "不存在",
	}
	for plan, want := range cases {
		writePlan(t, root, plan)
		if _, err := runCues(root); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("应报 %q，实际 %v", want, err)
		}
	}
	untouched, _ := os.ReadFile(filepath.Join(root, "scenes", "scene-002", "scene.json"))
	if strings.Contains(string(untouched), "timing") {
		t.Error("校验失败时不得写任何文件")
	}
}

func TestSceneCuesValidatesFlags(t *testing.T) {
	root := cuesProject(t)
	if _, err := runCues(root, "--fps", "25"); err == nil || !strings.Contains(err.Error(), "--fps 25") {
		t.Errorf("--fps 与画幅不符应拒绝：%v", err)
	}
	if _, err := runCues(root, "--total-frames", "90"); err == nil || !strings.Contains(err.Error(), "不早于总帧数") {
		t.Errorf("锚点超出总帧数应拒绝：%v", err)
	}
}

func TestSceneCuesWritesLookAndHandoffsToBothSides(t *testing.T) {
	root := cuesProject(t)
	writePlan(t, root, `{"schema":1,"look":{"background":"#0F1720","ink":"#F5F1E8","accent":"#F2B134"},"scenes":[
  {"id":"scene-001","dir":"scenes/scene-001","chapter":"问题"},
  {"id":"scene-002","dir":"scenes/scene-002","first_subtitle":2,"chapter":"问题"},
  {"id":"scene-003","dir":"scenes/scene-003","first_phrase":"p-4","cut":"prev_end","chapter":"机制",
   "handoff":{"device":"push_in","bridge":"琥珀色圆形表面铺满画面"}}]}`)
	if out, err := runCues(root); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	load := func(id string) scene.Scene {
		s, err := scene.Load(filepath.Join(root, "scenes", id))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, second, third := load("scene-001"), load("scene-002"), load("scene-003")
	if first.Timing.Look.Background != "#0F1720" || third.Timing.Look != first.Timing.Look {
		t.Errorf("每个镜头都应带同一份全片配色：%+v %+v", first.Timing.Look, third.Timing.Look)
	}
	if first.Timing.Exit != (scene.Handoff{}) || second.Timing.Enter != (scene.Handoff{}) {
		t.Errorf("没写 handoff 的章节内交界是硬切：%+v %+v", first.Timing.Exit, second.Timing.Enter)
	}
	want := scene.Handoff{Device: "push_in", Bridge: "琥珀色圆形表面铺满画面", CrossChapter: true}
	exit, enter := second.Timing.Exit, third.Timing.Enter
	if exit.Peer != "scene-003" || enter.Peer != "scene-002" {
		t.Errorf("交接两侧的 peer 不对：%+v %+v", exit, enter)
	}
	exit.Peer, enter.Peer = "", ""
	if exit != want || enter != want {
		t.Errorf("交界两侧应拿到同一份约定：%+v %+v", exit, enter)
	}
	if third.Timing.Chapter != "机制" {
		t.Errorf("章节应写进 timing：%q", third.Timing.Chapter)
	}
	if out, err := runCues(root, "--check"); err != nil {
		t.Fatalf("--check 应通过：%v\n%s", err, out)
	}

	// 改了交接约定却没重跑，--check 要指出来。
	writePlan(t, root, strings.Replace(mustRead(t, filepath.Join(root, "production", "scene-plan.json")), "push_in", "flood", 1))
	out, err := runCues(root, "--check")
	if err == nil || !strings.Contains(err.Error(), "enter=scene-002/flood") {
		t.Fatalf("交接改动应被 --check 发现：%v\n%s", err, out)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
