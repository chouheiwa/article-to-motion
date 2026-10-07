package scene

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chouheiwa/articale-to-motion/internal/config"
)

// runFakeCodex 跑一次假渲染：吐出一条用量事件、写出产物，返回 Run 的结果。
func runFakeCodex(t *testing.T, s Scene, exitCode string) error {
	t.Helper()
	bin := t.TempDir()
	executable(t, bin, "codex", `
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":2,"output_tokens":5}}'
printf video > out.mp4
exit `+exitCode+`
`)
	fakeFfprobe(t, bin, probeReply{})
	cfg := config.Config{Orchestrator: "claude", Renderer: "codex", TTSProvider: "minimax"}
	return Run(context.Background(), s, cfg, false, map[string]string{"PATH": bin, "HOME": t.TempDir()}, &bytes.Buffer{}, 0.15)
}

func readAttempt(t *testing.T, path string) Attempt {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("缺少尝试记录：%v", err)
	}
	var attempt Attempt
	if err := json.Unmarshal(body, &attempt); err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestRunWritesRecordAndAttemptOnSuccess(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	if err := runFakeCodex(t, s, "0"); err != nil {
		t.Fatal(err)
	}
	record, ok := LoadRecord(s)
	if !ok || record.Fingerprint == "" || record.Parts["file:scene.json"] == "" || record.Parts["prompt"] == "" {
		t.Fatalf("通过校验后应写下渲染记录：%+v %v", record, ok)
	}
	attempt := readAttempt(t, s.AttemptLog())
	if attempt.Outcome != OutcomeSucceeded || attempt.Fingerprint != record.Fingerprint || attempt.ExitCode == nil || *attempt.ExitCode != 0 {
		t.Fatalf("尝试记录不对：%+v", attempt)
	}
	if attempt.Usage == nil || attempt.Usage.InputTokens != 12 || attempt.Usage.OutputTokens != 5 {
		t.Fatalf("应从输出流读到用量：%+v", attempt.Usage)
	}
}

// TestRunArchivesPreviousAttempt：手动重跑镜头不能抹掉上一次的日志与产物。
func TestRunArchivesPreviousAttempt(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	if err := runFakeCodex(t, s, "0"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.StreamLog(), []byte("第一次的现场\n"), 0o644)
	err := runFakeCodex(t, s, "3")
	if err == nil || !strings.Contains(err.Error(), "退出码 3") {
		t.Fatalf("应报渲染器退出码：%v", err)
	}
	archived, readErr := os.ReadFile(filepath.Join(dir, "attempts", "attempt-01", filepath.Base(s.StreamLog())))
	if readErr != nil || string(archived) != "第一次的现场\n" {
		t.Fatalf("上一次的日志应归档：%q %v", archived, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "attempts", "attempt-01", "out.mp4")); statErr != nil {
		t.Fatalf("上一次的产物应一起归档：%v", statErr)
	}
	if _, ok := LoadRecord(s); ok {
		t.Fatal("失败的重渲染不应留下可用的渲染记录")
	}
	attempt := readAttempt(t, s.AttemptLog())
	if attempt.Outcome != OutcomeFailed || attempt.ExitCode == nil || *attempt.ExitCode != 3 {
		t.Fatalf("失败尝试记录不对：%+v", attempt)
	}
}

func TestRunRecordsVerificationFailure(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":2,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	if err := runFakeCodex(t, s, "0"); err == nil {
		t.Fatal("时长不符应当失败")
	}
	attempt := readAttempt(t, s.AttemptLog())
	if attempt.Outcome != OutcomeVerificationFailed || len(attempt.Problems) == 0 {
		t.Fatalf("校验失败应单独记录：%+v", attempt)
	}
}

func TestFingerprintTracksDeclaredInputs(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	skills := t.TempDir()
	os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("v1"), 0o644)
	resolved := map[string]string{AnimationSkillName: skills}
	base, err := Fingerprint(s, "codex", resolved)
	if err != nil {
		t.Fatal(err)
	}
	// 重试说明与 worker 数是调度层临时填的，不算输入。
	again, _ := Fingerprint(s.WithRetryNote("上次失败").WithRenderWorkers(4), "codex", resolved)
	if again.Fingerprint != base.Fingerprint {
		t.Fatalf("重试说明不应改变指纹：%v", again.Changed(base))
	}
	// 渲染器在镜头目录里写的合成代码不是输入。
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0o644)
	if after, _ := Fingerprint(s, "codex", resolved); after.Fingerprint != base.Fingerprint {
		t.Fatalf("渲染器自己的产出不应改变指纹：%v", after.Changed(base))
	}
	os.WriteFile(filepath.Join(dir, "transcript.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\n改过\n"), 0o644)
	changed, _ := Fingerprint(s, "codex", resolved)
	if got := changed.Changed(base); !slices.Equal(got, []string{"file:transcript.srt"}) {
		t.Fatalf("应只报字幕变了，实际 %v", got)
	}
	if other, _ := Fingerprint(s, "claude", resolved); !slices.Contains(other.Changed(base), "renderer") {
		t.Fatalf("换渲染工具应改变指纹：%v", other.Changed(base))
	}
	// 挪动技能目录不算变化，改技能内容才算。
	moved := t.TempDir()
	os.WriteFile(filepath.Join(moved, "SKILL.md"), []byte("v1"), 0o644)
	if relocated, _ := Fingerprint(s, "codex", map[string]string{AnimationSkillName: moved}); slices.Contains(relocated.Changed(changed), "prompt") || relocated.Fingerprint != changed.Fingerprint {
		t.Fatalf("技能目录换位置不应改变指纹：%v", relocated.Changed(changed))
	}
	edited := t.TempDir()
	os.WriteFile(filepath.Join(edited, "SKILL.md"), []byte("v2"), 0o644)
	if updated, _ := Fingerprint(s, "codex", map[string]string{AnimationSkillName: edited}); !slices.Equal(updated.Changed(changed), []string{"skill:" + AnimationSkillName}) {
		t.Fatalf("技能内容变化应单独报出：%v", updated.Changed(changed))
	}
}

// TestLoadRecordRejectsReplacedOutput：记录描述的是那一个文件，产物被换掉后不能再替新文件作保。
func TestLoadRecordRejectsReplacedOutput(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
	if err := WriteRecord(s, Inputs{Fingerprint: "abc", Parts: map[string]string{"prompt": "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadRecord(s); !ok {
		t.Fatal("刚写下的记录应当可用")
	}
	os.WriteFile(s.OutputPath(), []byte("other"), 0o644)
	if _, ok := LoadRecord(s); ok {
		t.Fatal("产物被替换后记录不应再适用")
	}
}
