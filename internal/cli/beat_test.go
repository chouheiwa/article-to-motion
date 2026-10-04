package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/beat"
	"github.com/chouheiwa/articale-to-motion/internal/scene"
)

// beatProject 建一个已导入 6.5 秒音乐的卡点项目：120 BPM、4/4、弱起 0.5 秒，
// 共 3 小节（重拍在 0.5、2.5、4.5 秒）。草稿网格直接写盘，代替需要 Python 的分析。
func beatProject(t *testing.T) string {
	t.Helper()
	ffmpeg := requireFFmpeg(t)
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := Execute(append(args, "--project-root", root), &out, &errOut); code != 0 {
			t.Fatalf("%v 失败：%s", args, errOut.String())
		}
	}
	run("beat", "init")
	frame := "---\nstyle_id: x\ncanvas:\n  width_px: 320\n  height_px: 240\n  fps: 30\n---\n"
	if err := os.WriteFile(filepath.Join(root, "frame.md"), []byte(frame), 0o644); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(t.TempDir(), "loop.wav")
	if output, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000:duration=6.5", music).CombinedOutput(); err != nil {
		t.Fatalf("生成音乐失败：%v\n%s", err, output)
	}
	run("beat", "import", music)
	track, err := beat.LoadTrack(root)
	if err != nil {
		t.Fatal(err)
	}
	draft := beat.Grid{Schema: 1, AudioSHA: track.AudioSHA, Duration: track.Duration, Source: "test"}
	for i := 0; i < 13; i++ {
		draft.Beats = append(draft.Beats, float64(i)*0.5)
	}
	// 故意把重拍放错一拍，再用 regrid 纠正。
	draft.Downbeats = []float64{0, 2, 4, 6}
	if _, err := beat.SaveDraft(root, draft); err != nil {
		t.Fatal(err)
	}
	run("beat", "regrid", "--meter", "4", "--anchor", "1")
	run("beat", "confirm")
	return root
}

func writeBeatPlan(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, beat.PlanFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBeatCuesWriteDurationsOnDownbeats(t *testing.T) {
	root := beatProject(t)
	grid, err := beat.LoadGrid(root)
	if err != nil {
		t.Fatal(err)
	}
	if grid.Downbeats[0] != 0.5 || len(grid.Downbeats) != 3 {
		t.Fatalf("regrid 后重拍应从 0.5 秒起：%v", grid.Downbeats)
	}
	scenes := filepath.Join(root, "scenes")
	makeSceneDir(t, scenes, "scene-001", 1, "testsrc=size=320x240:rate=30:duration=1")
	makeSceneDir(t, scenes, "scene-002", 1, "testsrc=size=320x240:rate=30:duration=1")
	writeBeatPlan(t, root, `{"schema":1,"scenes":[{"id":"scene-001","bars":2},{"id":"scene-002","bars":1}]}`)

	var out, errOut bytes.Buffer
	if code := Execute([]string{"beat", "cues", scenes, "--project-root", root}, &out, &errOut); code != 0 {
		t.Fatalf("cues 失败：%s", errOut.String())
	}
	var first struct {
		Duration float64   `json:"duration_seconds"`
		Beat     *beat.Cue `json:"beat"`
	}
	body, _ := os.ReadFile(filepath.Join(scenes, "scene-001", "scene.json"))
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	// 弱起 + 2 小节：切点在第 3 小节的重拍 4.5 秒，即第 135 帧。
	if first.Beat == nil || first.Beat.Frames != 135 || first.Duration != 4.5 {
		t.Fatalf("第一个镜头应到 4.5 秒的重拍：%s", body)
	}
	var local beat.Local
	cues, _ := os.ReadFile(filepath.Join(scenes, "scene-002", beat.CueDataFile))
	if err := json.Unmarshal(cues, &local); err != nil || local.Beats[0].Frame != 0 || !local.Beats[0].Downbeat || local.Beats[0].Bar != 3 {
		t.Fatalf("第二个镜头应从第 3 小节重拍起：%s", cues)
	}

	errOut.Reset()
	if code := Execute([]string{"validate", "beat", "--project-root", root}, &out, &errOut); code != 0 {
		t.Fatalf("校验应通过：%s", errOut.String())
	}
	// 重跑是幂等的：内容不变时不动文件，避免 run-all 把镜头判为过期。
	before, _ := os.Stat(filepath.Join(scenes, "scene-001", "scene.json"))
	Execute([]string{"beat", "cues", scenes, "--project-root", root}, &out, &errOut)
	after, _ := os.Stat(filepath.Join(scenes, "scene-001", "scene.json"))
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("内容不变的重跑不应改写 scene.json")
	}

	// 手改镜头时长，切点离开重拍。
	var fields map[string]any
	json.Unmarshal(body, &fields)
	fields["duration_seconds"] = 4.0
	edited, _ := json.Marshal(fields)
	os.WriteFile(filepath.Join(scenes, "scene-001", "scene.json"), edited, 0o644)
	errOut.Reset()
	if code := Execute([]string{"validate", "beat", "--project-root", root}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "不要手改") {
		t.Fatalf("手改时长应被拦下：%d %s", code, errOut.String())
	}
}

func TestBeatCuesRejectPlanMismatchWithoutWriting(t *testing.T) {
	root := beatProject(t)
	scenes := filepath.Join(root, "scenes")
	makeSceneDir(t, scenes, "scene-001", 1, "testsrc=size=320x240:rate=30:duration=1")
	makeSceneDir(t, scenes, "scene-002", 1, "testsrc=size=320x240:rate=30:duration=1")
	original, _ := os.ReadFile(filepath.Join(scenes, "scene-001", "scene.json"))
	for name, plan := range map[string]string{
		"小节合计": `{"schema":1,"scenes":[{"id":"scene-001","bars":1},{"id":"scene-002","bars":1}]}`,
		"镜头顺序": `{"schema":1,"scenes":[{"id":"scene-002","bars":2},{"id":"scene-001","bars":1}]}`,
	} {
		writeBeatPlan(t, root, plan)
		var out, errOut bytes.Buffer
		if code := Execute([]string{"beat", "cues", scenes, "--project-root", root}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), name) {
			t.Errorf("%s 应报错并点名：%d %s", name, code, errOut.String())
		}
		if now, _ := os.ReadFile(filepath.Join(scenes, "scene-001", "scene.json")); string(now) != string(original) {
			t.Fatalf("%s 校验失败时不应写任何镜头", name)
		}
	}
}

// 没确认网格之前，cues 不能用草稿切镜头。
func TestBeatCuesRequireConfirmedGrid(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	var out, errOut bytes.Buffer
	Execute([]string{"beat", "init", "--project-root", root}, &out, &errOut)
	if code := Execute([]string{"beat", "cues", filepath.Join(root, "scenes"), "--project-root", root}, &out, &errOut); code != 1 {
		t.Fatalf("没有正式网格时应失败：%s", errOut.String())
	}
}

func TestInitBeatDelivery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	var out, errOut bytes.Buffer
	if code := Execute([]string{"init", root, "--canvas", "vertical-3x4", "--skip-hyperframes", "--delivery", "beat"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if _, err := beat.LoadConfig(root); err != nil {
		t.Fatalf("应写出可用的 beat.yaml：%v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	if code := Execute([]string{"init", bad, "--canvas", "vertical-3x4", "--skip-hyperframes", "--delivery", "beat", "--narration", "cast"}, &out, &errOut); code == 0 {
		t.Fatal("卡点与多角色对话不应同时启用")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("拒绝时不应留下半成品目录")
	}
}

// 审查发现的两条旁路：抄另一个镜头的 beat 块、把 grid 指到别的项目。
// 都必须在单镜头校验（渲染前那一道）就被拦下，而不只靠 run-all 的整体比对。
func TestVerifyBeatInputsRejectsBorrowedOrForeignCues(t *testing.T) {
	root := beatProject(t)
	scenes := filepath.Join(root, "scenes")
	makeSceneDir(t, scenes, "scene-001", 1, "testsrc=size=320x240:rate=30:duration=1")
	makeSceneDir(t, scenes, "scene-002", 1, "testsrc=size=320x240:rate=30:duration=1")
	writeBeatPlan(t, root, `{"schema":1,"scenes":[{"id":"scene-001","bars":2},{"id":"scene-002","bars":1}]}`)
	var out, errOut bytes.Buffer
	if code := Execute([]string{"beat", "cues", scenes, "--project-root", root}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	load := func(id string) scene.Scene {
		t.Helper()
		s, err := scene.Load(filepath.Join(scenes, id))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, second := load("scene-001"), load("scene-002")
	if err := scene.VerifyBeatInputs(first); err != nil {
		t.Fatalf("正常镜头应通过：%v", err)
	}

	borrowed := second
	borrowed.Beat = first.Beat
	borrowed.DurationSeconds = first.DurationSeconds
	if err := scene.VerifyBeatInputs(borrowed); err == nil || !strings.Contains(err.Error(), "属于镜头") {
		t.Fatalf("抄来的 beat 块应被拦下：%v", err)
	}

	foreign := first
	cue := *first.Beat
	cue.Grid = "../../../other/" + beat.GridFile
	foreign.Beat = &cue
	if err := scene.VerifyBeatInputs(foreign); err == nil || !strings.Contains(err.Error(), "beat.grid") {
		t.Fatalf("指向别的项目的网格应被拦下：%v", err)
	}
}
