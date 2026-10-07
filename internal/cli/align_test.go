package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/align"
	"github.com/chouheiwa/articale-to-motion/internal/srt"
)

func runAlign(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newAlignCommand(&out)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return out.String(), err
}

func writeAlignFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// alignEvidence 把朗读文本逐单元写成规范形状的对齐结果，每个单元 0.1 秒。
func alignEvidence(t *testing.T, path, spoken string, start float64) {
	t.Helper()
	type entry struct {
		Text  string  `json:"text"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	}
	var entries []entry
	for _, tok := range align.Tokenize(spoken) {
		entries = append(entries, entry{tok.Text, start, start + 0.1})
		start += 0.1
	}
	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeAlignFile(t, path, string(body))
}

func TestAlignTextAndBuild(t *testing.T) {
	root := t.TempDir()
	writeAlignFile(t, filepath.Join(root, "production/script-final.md"),
		"# 标题\n\n@{year}<2024年|二零二四年>@{/year}，我们发布了@{api}<API|A P I>@{/api}。\n")
	if _, err := runAlign(t, "text", "--project-root", root); err != nil {
		t.Fatal(err)
	}
	spoken, err := os.ReadFile(filepath.Join(root, "production/audio/tts-text.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(spoken) != "二零二四年，我们发布了A P I。\n" {
		t.Fatalf("朗读文本不符：%q", spoken)
	}
	alignEvidence(t, filepath.Join(root, "production/audio/align.json"), "二零二四年我们发布了API", 0.5)
	// 已有时间轴里手填的 visible_event_ids 要保留。
	writeAlignFile(t, filepath.Join(root, "production/phrase-timeline.json"),
		`{"schema":1,"fps":30,"phrases":[{"phrase_id":"api","reviewed":true,"visible_event_ids":["logo"]}]}`)

	out, err := runAlign(t, "build", "--project-root", root, "--fps", "30")
	if err != nil {
		t.Fatalf("构建失败：%v\n%s", err, out)
	}
	cues, err := srt.ReadCues(filepath.Join(root, "production/audio/voice.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0].Text != "2024年，我们发布了API。" || cues[0].StartSeconds != 0.5 {
		t.Fatalf("字幕不符：%+v", cues)
	}
	var timeline align.Timeline
	body, _ := os.ReadFile(filepath.Join(root, "production/phrase-timeline.json"))
	if err := json.Unmarshal(body, &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Phrases) != 2 || timeline.Phrases[1].VisibleEventIDs[0] != "logo" || !timeline.Phrases[1].Reviewed {
		t.Fatalf("时间轴不符：%+v", timeline)
	}
	if _, err := os.Stat(filepath.Join(root, "production/audio/align-report.json")); err != nil {
		t.Fatalf("应写出报告：%v", err)
	}
}

func TestAlignBuildProblemsExitNonZeroButWriteOutputs(t *testing.T) {
	root := t.TempDir()
	writeAlignFile(t, filepath.Join(root, "production/script-final.md"), "我们@{k}今天下午@{/k}出发去北京看看风景和朋友。\n")
	seg := filepath.Join(root, "seg.json")
	alignEvidence(t, seg, "我们出发去北京看看风景和朋友", 0)
	report := filepath.Join(root, "report.json")
	_, err := runAlign(t, "build", "--project-root", root, "--fps", "25", "--align", seg+"@1.5", "--report-json", report)
	var coded *exitError
	if !errors.As(err, &coded) || coded.code != 1 || !strings.Contains(err.Error(), "短语 k") {
		t.Fatalf("漏读受保护短语应以退出码 1 失败：%v", err)
	}
	var parsed align.Report
	body, readErr := os.ReadFile(report)
	if readErr != nil || json.Unmarshal(body, &parsed) != nil || parsed.OK || parsed.FPS != 25 {
		t.Fatalf("问题时仍应写出报告：%v %+v", readErr, parsed)
	}
	if _, err := os.Stat(filepath.Join(root, "production/audio/voice.srt")); err != nil {
		t.Fatalf("问题时仍应写出字幕：%v", err)
	}
}

func TestAlignBuildRejectsBadInput(t *testing.T) {
	root := t.TempDir()
	writeAlignFile(t, filepath.Join(root, "production/script-final.md"), "你好<世界\n")
	if _, err := runAlign(t, "text", "--project-root", root); err == nil || !strings.Contains(err.Error(), "第 1 行第 3 列") {
		t.Fatalf("标记错误应报出位置：%v", err)
	}
	if _, err := runAlign(t, "build", "--project-root", root); err == nil || !strings.Contains(err.Error(), "--fps") {
		t.Fatalf("缺 --fps 应报错：%v", err)
	}
	writeAlignFile(t, filepath.Join(root, "production/script-final.md"), "你好。\n")
	writeAlignFile(t, filepath.Join(root, "production/audio/align.json"), `{"result":{"words":[{"w":"你"}]}}`)
	if _, err := runAlign(t, "build", "--project-root", root, "--fps", "30"); err == nil || !strings.Contains(err.Error(), "键：result") {
		t.Fatalf("认不出的对齐结果应报出顶层结构：%v", err)
	}
	alignEvidence(t, filepath.Join(root, "production/audio/align.json"), "你好", 0)
	writeAlignFile(t, filepath.Join(root, "production/phrase-adjust.json"), `{"schema":1,"adjustments":[{"phrase_id":"nope","edge":"start","frame":0,"reason":"x"}]}`)
	if _, err := runAlign(t, "build", "--project-root", root, "--fps", "30"); err == nil || !strings.Contains(err.Error(), "没有这个短语") {
		t.Fatalf("修正层引用未知短语应报错：%v", err)
	}
}

func TestAlignHelpIsComplete(t *testing.T) {
	cmd := newAlignCommand(&bytes.Buffer{})
	for _, sub := range cmd.Commands() {
		if strings.TrimSpace(sub.Long) == "" || !strings.Contains(sub.Example, "am align "+sub.Name()) {
			t.Errorf("am align %s 缺少 Long 或 Example", sub.Name())
		}
	}
	build, _, err := cmd.Find([]string{"build"})
	if err != nil || !strings.Contains(build.Long, `"start"`) || !strings.Contains(build.Long, "timestamp") {
		t.Errorf("build 的帮助应写明规范形状与 funasr 形状")
	}
}
