package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/preset"
)

func TestHelpListsPublicCommands(t *testing.T) {
	var out bytes.Buffer
	code := Execute([]string{"--help"}, &out, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, command := range []string{"init", "run", "scene", "archive", "validate", "config"} {
		if !strings.Contains(out.String(), command) {
			t.Errorf("help missing %s", command)
		}
	}
}

func TestInitCreatesProjectWithoutNetworkWhenSkipped(t *testing.T) {
	target := filepath.Join(t.TempDir(), "project")
	var out bytes.Buffer
	code := Execute([]string{"init", target, "--canvas", "vertical-3x4", "--skip-hyperframes"}, &out, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(target, "PROMPT.md")); err != nil {
		t.Fatal(err)
	}
}

func TestConfigGetUsesProjectRoot(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	var out bytes.Buffer
	if code := Execute([]string{"config", "get", "RENDERER"}, &out, &out); code != 0 || strings.TrimSpace(out.String()) != "claude" {
		t.Fatalf("code=%d output=%q", code, out.String())
	}
}

func TestUnknownCommandUsesExitOne(t *testing.T) {
	if code := Execute([]string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("got exit %d", code)
	}
}

// initProject 铺一个真实项目供校验类用例使用。
// 素材拆成 assets/shared 与 assets/presets 两棵源树后，仓库根本身不再是一个
// 可校验的项目，必须先 init 才有 frame.md、templates/ 和 assets/。
func initProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "video")
	var out bytes.Buffer
	if code := Execute([]string{"init", root, "--canvas", "vertical-3x4", "--style", preset.DefaultStyle().ID, "--skip-hyperframes"}, &out, &out); code != 0 {
		t.Fatalf("init code=%d output=%s", code, out.String())
	}
	return root
}

func TestValidatePublishCommand(t *testing.T) {
	root := initProject(t)
	var out bytes.Buffer
	path := filepath.Join(root, "templates", "publish.md")
	if code := Execute([]string{"validate", "publish", path, "--project-root", root}, &out, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
}

func TestRunStartsConfiguredOrchestrator(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.WriteFile(filepath.Join(root, "PROMPT.md"), []byte("hello orchestrator"), 0o644)
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$AM_EXECUTABLE\" > \"" + filepath.Join(root, "am-executable.txt") + "\"\n" +
		"printf '%s\\n' \"$PATH\" > \"" + filepath.Join(root, "child-path.txt") + "\"\n" +
		"cat > \"" + filepath.Join(root, "received.txt") + "\"\n"
	os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCHESTRATOR", "codex")
	t.Setenv("RENDERER", "claude")
	var out bytes.Buffer
	if code := Execute([]string{"run"}, &out, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "received.txt"))
	if err != nil || string(body) != "hello orchestrator" {
		t.Fatalf("received=%q err=%v", body, err)
	}
	executable, err := os.ReadFile(filepath.Join(root, "am-executable.txt"))
	if err != nil {
		t.Fatal(err)
	}
	executablePath := strings.TrimSpace(string(executable))
	if executablePath == "" || !filepath.IsAbs(executablePath) {
		t.Fatalf("AM_EXECUTABLE should be absolute, got %q", executablePath)
	}
	childPath, err := os.ReadFile(filepath.Join(root, "child-path.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pathEntries := filepath.SplitList(strings.TrimSpace(string(childPath)))
	if len(pathEntries) == 0 || pathEntries[0] != filepath.Dir(executablePath) {
		t.Fatalf("executable directory should lead PATH: executable=%q PATH=%q", executablePath, childPath)
	}
}

func TestStyleAndEmptyRunAllCommands(t *testing.T) {
	root := initProject(t)
	var out bytes.Buffer
	if code := Execute([]string{"validate", "style", "--project-root", root}, &out, &out); code != 0 {
		t.Fatalf("style code=%d output=%s", code, out.String())
	}
	project := t.TempDir()
	os.WriteFile(filepath.Join(project, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.Mkdir(filepath.Join(project, "scenes"), 0o755)
	old, _ := os.Getwd()
	os.Chdir(project)
	defer os.Chdir(old)
	out.Reset()
	if code := Execute([]string{"scene", "run-all", "scenes", "--jobs", "1", "--retries", "0"}, &out, &out); code != 0 {
		t.Fatalf("run-all code=%d output=%s", code, out.String())
	}
}

func TestInitConflictAndSameToolConfigSucceeds(t *testing.T) {
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "PROMPT.md"), []byte("custom"), 0o644)
	if code := Execute([]string{"init", target, "--canvas", "vertical-3x4", "--skip-hyperframes"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("conflict exit=%d", code)
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=codex\n"), 0o644)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	var out bytes.Buffer
	if code := Execute([]string{"config", "get", "RENDERER"}, &out, &out); code != 0 {
		t.Fatalf("same-tool config exit=%d output=%s", code, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "codex" {
		t.Fatalf("renderer=%q want codex", got)
	}
}

func TestArchiveDryRunDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	git("init", "-b", "main")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("base"), 0o644)
	git("add", ".")
	git("commit", "-m", "base")
	git("switch", "-c", "video/test")
	os.WriteFile(filepath.Join(root, "scene.txt"), []byte("scene"), 0o644)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	var out bytes.Buffer
	if code := Execute([]string{"archive", "--dry-run", "--archive-root", filepath.Join(t.TempDir(), "archive")}, &out, &out); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "scene.txt")); err != nil {
		t.Fatal("dry run mutated project")
	}
}

func TestRunAllRejectsNaNToleranceEvenWhenNoScenesExist(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.Mkdir(filepath.Join(root, "scenes"), 0o755)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	if code := Execute([]string{"scene", "run-all", "scenes", "--duration-tolerance", "nan"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("nan tolerance exit=%d", code)
	}
}

func TestValidateStyleRegenerateExamplesReportsMissingImageMagick(t *testing.T) {
	// 先铺项目再清空 PATH：init 本身不需要外部命令，但清空后就没法建了。
	root := initProject(t)
	t.Setenv("PATH", t.TempDir())
	var stderr bytes.Buffer
	if code := Execute([]string{"validate", "style", "--project-root", root, "--regenerate-examples"}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ImageMagick") {
		t.Fatalf("missing actionable error: %s", stderr.String())
	}
}

func TestRunCancellationReturns130AndTerminatesProcessGroup(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	marker := filepath.Join(root, "started")
	os.WriteFile(filepath.Join(root, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	os.WriteFile(filepath.Join(root, "PROMPT.md"), []byte("cancel me"), 0o644)
	script := "#!/bin/sh\ntouch \"" + marker + "\"\nsleep 30 &\nwait\n"
	os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755)
	old, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(old)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- ExecuteContext(ctx, []string{"run"}, &bytes.Buffer{}, &bytes.Buffer{}) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("orchestrator did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("cancel exit=%d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator process group was not terminated")
	}
}

func TestInitRejectsUnknownCanvas(t *testing.T) {
	var out bytes.Buffer
	code := Execute([]string{"init", filepath.Join(t.TempDir(), "video"), "--canvas", "vertical-4x5", "--skip-hyperframes"}, &out, &out)
	if code == 0 {
		t.Fatal("未知画幅应报错")
	}
	if !strings.Contains(out.String(), "vertical-9x16") {
		t.Errorf("错误信息应列出可选画幅，实际：%s", out.String())
	}
}

func TestInitWritesChosenCanvas(t *testing.T) {
	target := filepath.Join(t.TempDir(), "video")
	var out bytes.Buffer
	if code := Execute([]string{"init", target, "--canvas", "vertical-9x16", "--style", preset.DefaultStyle().ID, "--skip-hyperframes"}, &out, &out); code != 0 {
		t.Fatalf("init code=%d output=%s", code, out.String())
	}
	body, err := os.ReadFile(filepath.Join(target, "frame.md"))
	if err != nil {
		t.Fatalf("读取 frame.md: %v", err)
	}
	if !strings.Contains(string(body), "height_px: 1920") {
		t.Error("frame.md 未写入 9:16 画幅")
	}
	if !strings.Contains(string(body), "top_px: 1470") {
		t.Error("frame.md 的字幕安全区未按 9:16 推导")
	}
	// 共享树与预设树都要落地。
	for _, name := range []string{
		filepath.Join("assets", "fonts", "noto-sans-sc-400.woff2"),
		filepath.Join("assets", "style-guide", "examples", "proposition.png"),
		filepath.Join("templates", "project-rules.md"),
	} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("缺少 %s: %v", name, err)
		}
	}
}

// 非交互环境不静默取默认画幅：选错画幅会让整个项目的排版基准和成片规格全错。
func TestInitRequiresExplicitCanvasWhenNotInteractive(t *testing.T) {
	var out bytes.Buffer
	code := Execute([]string{"init", filepath.Join(t.TempDir(), "video"), "--skip-hyperframes"}, &out, &out)
	if code == 0 {
		t.Fatal("非交互且未传 --canvas 时应报错")
	}
	if !strings.Contains(out.String(), "--canvas") {
		t.Errorf("错误信息应提示传 --canvas，实际：%s", out.String())
	}
}

// --skip-hyperframes：本包其余 7 处调用 init 的测试都带这个开关，避免走真实的
// 联网 HyperFrames 安装——这里断言的是 --narration 的行为，与技能安装无关，
// 跟随既有约定同样跳过，让用例在无网环境下也能快速、确定地跑完。
func TestInitNarrationSoloHasNoRoster(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "solo")
	if _, err := runCLI(t, root, "init", target, "--canvas", "vertical-3x4", "--skip-hyperframes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "cast.yaml")); !os.IsNotExist(err) {
		t.Error("单口播项目不该有 cast.yaml")
	}
}

func TestInitNarrationCastWritesRoster(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "story")
	if _, err := runCLI(t, root, "init", target, "--canvas", "vertical-3x4", "--narration", "cast", "--skip-hyperframes"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target, "cast.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// packs: [] 是明确裁定过的设计：am init 刚建的项目本来就还没有角色包，
	// 钉住这个具体值，防止后人顺手给它补一个占位角色。defaults 的三个值
	// 与 am cast add 新建 cast.yaml 时使用的默认值必须一致。
	for _, want := range []string{
		"schema: cast/v1",
		"packs: []",
		"ground_y: 0.78",
		"turn: 240",
		"interject: 100",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("cast.yaml 缺少 %q，实际内容：\n%s", want, body)
		}
	}
	// 附录随 shared 树无条件下发，两种模式都在。
	if _, err := os.Stat(filepath.Join(target, "PROMPT-CAST-ADDENDUM.md")); err != nil {
		t.Errorf("缺附录：%v", err)
	}
}

func TestInitRejectsUnknownNarration(t *testing.T) {
	root := t.TempDir()
	if _, err := runCLI(t, root, "init", filepath.Join(root, "x"), "--canvas", "vertical-3x4", "--narration", "duet"); err == nil {
		t.Fatal("期望拒绝未知叙事模式")
	}
}

// 渲染前一次列全所有非整数帧时长的镜头，且不启动任何渲染器：这类错误到
// 拼接时才暴露的话，每一镜都已经烧掉一次完整的 AI CLI 调用。
func TestRunAllRejectsFractionalFrameDurationsBeforeRendering(t *testing.T) {
	project := t.TempDir()
	os.WriteFile(filepath.Join(project, "article-to-motion.conf"), []byte("ORCHESTRATOR=codex\nRENDERER=claude\n"), 0o644)
	for id, duration := range map[string]string{"scene-001": "14.367", "scene-002": "4", "scene-003": "2.01"} {
		dir := filepath.Join(project, "scenes", id)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "transcription.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "scene.json"), []byte(`{"id":"`+id+`","duration_seconds":`+duration+`,"output":"`+id+`.mp4","transcript":"transcription.srt","text":"hi"}`), 0o644)
	}
	// PATH 里放一个一旦被调用就留痕的假 claude，证明渲染前就被拦下。
	bin := t.TempDir()
	marker := filepath.Join(project, "renderer-called")
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old, _ := os.Getwd()
	os.Chdir(project)
	defer os.Chdir(old)
	var out bytes.Buffer
	if code := Execute([]string{"scene", "run-all", "scenes", "--jobs", "1", "--retries", "0"}, &out, &out); code == 0 {
		t.Fatalf("非整数帧时长应在渲染前失败：%s", out.String())
	}
	for _, want := range []string{"未对齐整数帧", "scene-001", "scene-003"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("错误信息缺少 %q：%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "镜头 scene-002") {
		t.Errorf("整数帧的 scene-002 不应被点名：%s", out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("渲染器不应被调用")
	}
}

// 版本号只来自发布时注入的 tag；未注入的本地构建必须自称 dev，
// 不能再出现一个写死、没人记得改的版本号。
func TestVersionFlagReportsInjectedVersion(t *testing.T) {
	var out bytes.Buffer
	if code := Execute([]string{"--version"}, &out, &out); code != 0 {
		t.Fatalf("--version exit=%d：%s", code, out.String())
	}
	if !strings.Contains(out.String(), Version) {
		t.Errorf("--version 应输出 %q，实际：%s", Version, out.String())
	}
	if Version != "dev" {
		t.Errorf("未注入时版本号应为 dev，实际 %q；版本号由发布流程按 tag 注入", Version)
	}
}
