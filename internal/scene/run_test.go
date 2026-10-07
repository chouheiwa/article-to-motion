package scene

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
)

func executable(t *testing.T, dir, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-only release target")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// probeReply 描述假 ffprobe 要汇报的规格。零值就是「合规的默认画幅镜头产物」：
// 1080x1440、30fps、无音轨，正是 defaultCanvas 期望的样子。
type probeReply struct {
	widthPx  int
	heightPx int
	fpsNum   int
	duration string
	audio    bool
	// frames 是逐帧解码统计（-count_frames）汇报的帧数；0 表示按时长 × 帧率推出。
	frames int
}

// fakeFfprobe 装一个按 mediaprobe 的调用方式输出 JSON 的假 ffprobe。
//
// 这里必须输出真 JSON 而不是一个数字：VerifyOutput 现在走 mediaprobe 的解析路径，
// 假工具的输出格式偏离真实 ffprobe 的话，测试就只是在验证测试自己。
func fakeFfprobe(t *testing.T, dir string, reply probeReply) {
	t.Helper()
	if reply.widthPx == 0 {
		reply.widthPx = 1080
	}
	if reply.heightPx == 0 {
		reply.heightPx = 1440
	}
	if reply.fpsNum == 0 {
		reply.fpsNum = 30
	}
	if reply.duration == "" {
		reply.duration = "1.000"
	}
	if reply.frames == 0 {
		seconds, _ := strconv.ParseFloat(reply.duration, 64)
		reply.frames = int(math.Round(seconds * float64(reply.fpsNum)))
	}
	audio := ""
	if reply.audio {
		audio = `,{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2}`
	}
	// 只能用 shell 内建：安全模式下子进程的 PATH 就是这个临时目录，
	// cat / echo 之类的外部命令都不在里面。
	json := fmt.Sprintf(`{"streams":[{"codec_type":"video","codec_name":"h264",`+
		`"width":%d,"height":%d,"pix_fmt":"yuv420p","r_frame_rate":"%d/1",`+
		`"nb_frames":"30","nb_read_frames":"%d","duration":"%s"}%s],"format":{"duration":"%s"}}`,
		reply.widthPx, reply.heightPx, reply.fpsNum, reply.frames, reply.duration, audio, reply.duration)
	executable(t, dir, "ffprobe", "printf '%s\\n' '"+json+"'\n")
}

func TestRunWritesLogsAndVerifiesDuration(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	executable(t, bin, "codex", `
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"[[USER_MESSAGE]]代码已完成，开始渲染"}}'
printf video > out.mp4
`)
	fakeFfprobe(t, bin, probeReply{})
	cfg := config.Config{Orchestrator: "claude", Renderer: "codex", TTSProvider: "minimax"}
	var user bytes.Buffer
	base := map[string]string{"PATH": bin, "HOME": t.TempDir()}
	if err := Run(context.Background(), s, cfg, false, base, &user, 0.15); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(user.String(), "代码已完成") {
		t.Fatalf("missing user output: %s", user.String())
	}
	for _, path := range []string{s.StreamLog(), s.StderrLog(), s.UserLog()} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing log %s", path)
		}
	}
}

func TestRunCancellationTerminatesProcessGroup(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	executable(t, bin, "codex", `/bin/sleep 300`)
	cfg := config.Config{Orchestrator: "claude", Renderer: "codex", TTSProvider: "minimax"}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Run(ctx, s, cfg, false, map[string]string{"PATH": bin, "HOME": t.TempDir()}, &bytes.Buffer{}, 0.15)
	if err == nil || !strings.Contains(err.Error(), "中断") {
		t.Fatalf("expected interruption, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation took too long")
	}
}

func TestVerifyOutputRejectsInvalidArtifacts(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	fakeFfprobe(t, bin, probeReply{duration: "2.000"})
	env := map[string]string{"PATH": bin, "HOME": t.TempDir()}
	if err := VerifyOutput(s, env, 0.15); err == nil {
		t.Fatal("missing output should fail")
	}
	os.WriteFile(s.OutputPath(), nil, 0o644)
	if err := VerifyOutput(s, env, 0.15); err == nil {
		t.Fatal("empty output should fail")
	}
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
	if err := VerifyOutput(s, env, 0.15); err == nil || !strings.Contains(err.Error(), "时长不符") {
		t.Fatalf("expected duration failure, got %v", err)
	}
	if err := VerifyOutput(s, env, -1); err == nil {
		t.Fatal("negative tolerance should fail")
	}
}

// TestVerifyOutputRejectsFrameCountWithinDurationTolerance 钉住逐帧计数：
// 差 3 帧（0.1 秒）落在默认 0.15 秒的时长容差内，只看时长会放行，
// 要等拼接后整片复查才发现。
func TestVerifyOutputRejectsFrameCountWithinDurationTolerance(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
	bin := t.TempDir()
	fakeFfprobe(t, bin, probeReply{duration: "1.000", frames: 33})
	env := map[string]string{"PATH": bin, "HOME": t.TempDir()}
	err := VerifyOutput(s, env, 0.15)
	var verification *VerificationError
	if !errors.As(err, &verification) || !strings.Contains(err.Error(), "期望 30 帧") || !strings.Contains(err.Error(), "实测 33 帧") {
		t.Fatalf("expected frame count VerificationError, got %v", err)
	}
}

// TestVerifyOutputChecksCanvasFrameRateAndSilence 守住这次补上的三项检查。
//
// 过去 VerifyOutput 只看时长：渲染成 720x1280、25fps 或带一条音轨的产物
// 一律退出码 0，错误要到拼接甚至成片才暴露。画幅尤其不能放过——
// 它无法靠后期规范化补救，把 720p 拉伸到 1080 是画质损失而不是格式统一。
func TestVerifyOutputChecksCanvasFrameRateAndSilence(t *testing.T) {
	cases := []struct {
		name  string
		reply probeReply
		want  string
	}{
		{"画幅不符", probeReply{widthPx: 720, heightPx: 1280}, "分辨率不符"},
		{"帧率不符", probeReply{fpsNum: 25}, "帧率不符"},
		{"混进了音轨", probeReply{audio: true}, "应当没有音轨"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
			s, _ := Load(dir)
			bin := t.TempDir()
			fakeFfprobe(t, bin, tc.reply)
			os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
			err := VerifyOutput(s, map[string]string{"PATH": bin, "HOME": t.TempDir()}, 0.15)
			if err == nil {
				t.Fatalf("%s 应当被拦下", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息里没有 %q：%v", tc.want, err)
			}
		})
	}
}

// TestVerifyOutputFailuresAreVerificationErrors：调度层靠这个类型判定
// 「不要重试」。退回成普通 error 会让每个规格不符的镜头白烧三次 AI CLI 调用。
func TestVerifyOutputFailuresAreVerificationErrors(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	fakeFfprobe(t, bin, probeReply{widthPx: 720, heightPx: 1280, fpsNum: 25, duration: "9.000"})
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
	err := VerifyOutput(s, map[string]string{"PATH": bin, "HOME": t.TempDir()}, 0.15)
	var verification *VerificationError
	if !errors.As(err, &verification) {
		t.Fatalf("应当是 *VerificationError，实际 %T：%v", err, err)
	}
	// 分辨率、帧率、时长、逐帧计数同时不符时要一次全报出来，否则调用方得修一条重跑一次。
	if len(verification.Problems) != 4 {
		t.Errorf("应当报出全部 4 条不符，实际 %d 条：%v", len(verification.Problems), verification.Problems)
	}
	if !strings.Contains(err.Error(), "out.mp4") {
		t.Errorf("错误信息没有点名产物：%v", err)
	}
}

// TestVerifyOutputHonorsStyleGuideCanvas：画幅取自 style_guide 而不是写死的默认值。
func TestVerifyOutputHonorsStyleGuideCanvas(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello","style_guide":"frame.md"}`)
	if err := os.WriteFile(filepath.Join(dir, "frame.md"),
		[]byte("---\ncanvas:\n  width_px: 1080\n  height_px: 1920\n  fps: 30\n---\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)

	bin := t.TempDir()
	// 按默认画幅（1440 高）渲染，但 style_guide 要的是 1920。
	fakeFfprobe(t, bin, probeReply{widthPx: 1080, heightPx: 1440})
	env := map[string]string{"PATH": bin, "HOME": t.TempDir()}
	if err := VerifyOutput(s, env, 0.15); err == nil || !strings.Contains(err.Error(), "1080x1920") {
		t.Fatalf("应当按 style_guide 的 1080x1920 判定，实际：%v", err)
	}

	bin2 := t.TempDir()
	fakeFfprobe(t, bin2, probeReply{widthPx: 1080, heightPx: 1920})
	if err := VerifyOutput(s, map[string]string{"PATH": bin2, "HOME": t.TempDir()}, 0.15); err != nil {
		t.Errorf("符合 style_guide 画幅却失败：%v", err)
	}
}

func TestRunReportsRendererFailure(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	executable(t, bin, "codex", `echo boom >&2; exit 3`)
	cfg := config.Config{Orchestrator: "claude", Renderer: "codex", TTSProvider: "minimax"}
	err := Run(context.Background(), s, cfg, false, map[string]string{"PATH": bin, "HOME": t.TempDir()}, &bytes.Buffer{}, 0.15)
	if err == nil || !strings.Contains(err.Error(), "退出码 3") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestVerifyOutputRejectsNondeterministicSource：规格全对、源码读了时钟的产物
// 渲染时照样退出码 0，只有源码扫描拦得住；它和规格问题一样走 VerificationError，
// 调度层据此不重试。
func TestVerifyOutputRejectsNondeterministicSource(t *testing.T) {
	dir := writeScene(t, `{"id":"scene-001","duration_seconds":1,"output":"out.mp4","transcript":"transcript.srt","text":"hello"}`)
	s, _ := Load(dir)
	bin := t.TempDir()
	fakeFfprobe(t, bin, probeReply{})
	os.WriteFile(s.OutputPath(), []byte("video"), 0o644)
	env := map[string]string{"PATH": bin, "HOME": t.TempDir()}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<script>\nconst seed = Date.now();\n</script>"), 0o644)
	err := VerifyOutput(s, env, 0.15)
	var verification *VerificationError
	if !errors.As(err, &verification) || !strings.Contains(err.Error(), "index.html:2 [clock]") {
		t.Fatalf("读时钟的源码应被判为 VerificationError：%v", err)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<script>const tl = gsap.timeline({ paused: true });</script>"), 0o644)
	if err := VerifyOutput(s, env, 0.15); err != nil {
		t.Fatalf("干净源码不应被拦：%v", err)
	}
}
