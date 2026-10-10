//go:build darwin || linux

package scene

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/config"
	"github.com/chouheiwa/articale-to-motion/internal/determinism"
	"github.com/chouheiwa/articale-to-motion/internal/envutil"
	"github.com/chouheiwa/articale-to-motion/internal/mediaprobe"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

const terminateGrace = 500 * time.Millisecond

func resolveBinary(name, pathValue string) (string, error) {
	return envutil.LookPath(name, pathValue)
}

func environmentList(values map[string]string) []string {
	return envutil.EnvList(values)
}

func Run(ctx context.Context, s Scene, cfg config.Config, unsafe bool, baseEnv map[string]string, userOutput io.Writer, tolerance float64) (err error) {
	if !isFinite(tolerance) || tolerance < 0 {
		return fmt.Errorf("tolerance 必须是有限且非负的数字")
	}
	renderer := s.Renderer
	if renderer == "" {
		renderer = cfg.Renderer
	}
	resolvedSkills, err := ResolveAllSkills(renderer, s.Directory, SkillsEnvironment(baseEnv, cfg.Overlay))
	if err != nil {
		return err
	}
	if resolvedSkills[AnimationSkillName] == "" {
		fmt.Fprintf(userOutput, "警告：未能为渲染工具 %s 定位 %s 技能目录，动效要求改用技能名兜底；可运行 am init 安装，或用 %s 显式指定\n", renderer, AnimationSkillName, SkillsDirEnv)
	}
	prompt, err := BuildPrompt(s, resolvedSkills)
	if err != nil {
		return err
	}
	argv, err := tools.RendererInvocation(renderer, prompt, unsafe, RendererAccess(resolvedSkills))
	if err != nil {
		return err
	}
	binary, err := resolveBinary(argv[0], baseEnv["PATH"])
	if err != nil {
		return err
	}
	argv[0] = binary
	inputs, err := Fingerprint(s, renderer, resolvedSkills)
	if err != nil {
		return err
	}
	// 先挪走上一次的现场再截断日志；旧记录随即作废，本次通过校验后再写新的。
	ArchiveAttempt(s)
	if err := RemoveRecord(s); err != nil {
		return err
	}
	// 局部时间数据在渲染器启动前写好：提示词让渲染器直接加载它，
	// 不再自己从全片字幕里做减法。
	if err := WriteSceneData(s); err != nil {
		return err
	}
	started := time.Now()
	attempt := Attempt{SceneID: s.ID, Renderer: renderer, Fingerprint: inputs.Fingerprint, StartedAt: timestamp(started), Retry: s.RetryNote != ""}
	var usage tools.Usage
	defer func() {
		ended := time.Now()
		attempt.EndedAt = timestamp(ended)
		attempt.Seconds = ended.Sub(started).Seconds()
		if !usage.Empty() {
			attempt.Usage = &usage
		}
		var verification *VerificationError
		switch {
		case err == nil:
			attempt.Outcome = OutcomeSucceeded
		case errors.As(err, &verification):
			attempt.Outcome = OutcomeVerificationFailed
			attempt.Problems = verification.Problems
		case ctx.Err() != nil:
			attempt.Outcome = OutcomeInterrupted
		default:
			attempt.Outcome = OutcomeFailed
		}
		if err != nil {
			attempt.Error = err.Error()
		}
		if writeErr := writeAttempt(s, attempt); writeErr != nil && err == nil {
			err = writeErr
		}
	}()
	rawFile, err := os.Create(s.StreamLog())
	if err != nil {
		return err
	}
	defer rawFile.Close()
	stderrFile, err := os.Create(s.StderrLog())
	if err != nil {
		return err
	}
	defer stderrFile.Close()
	userFile, err := os.Create(s.UserLog())
	if err != nil {
		return err
	}
	defer userFile.Close()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = s.Directory
	passthrough := envutil.ParsePassthrough(baseEnv["AM_PASSTHROUGH_ENV"])
	cmd.Env = environmentList(cfg.ChildEnvironment(baseEnv, unsafe, passthrough))
	cmd.Stdin = nil
	cmd.Stderr = stderrFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动渲染器：%w", err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(terminateGrace)
			select {
			case <-done:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(rawFile, line)
		tools.AccumulateUsage(renderer, line, &usage)
		for _, message := range tools.ExtractUserMessages(renderer, line) {
			fmt.Fprintln(userFile, message)
			fmt.Fprintln(userOutput, message)
		}
	}
	waitErr := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return fmt.Errorf("已中断：渲染器进程组已终止")
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取渲染器输出：%w", err)
	}
	if waitErr != nil {
		if exit, ok := waitErr.(*exec.ExitError); ok {
			code := exit.ExitCode()
			attempt.ExitCode = &code
			return fmt.Errorf("渲染器退出码 %d", code)
		}
		return waitErr
	}
	zero := 0
	attempt.ExitCode = &zero
	if err := VerifyOutput(s, baseEnv, tolerance); err != nil {
		return err
	}
	return WriteRecord(s, inputs)
}

// VerificationError 表示产物规格校验失败。
//
// 单独立一个类型是为了让调度层能把它和「渲染器崩了」区分开：规格不符是确定性的，
// 同样的提示词和渲染器重跑只会得到同样不合规的产物，而每次重试都是一次完整的
// AI CLI 调用。调用方用 errors.As 认出它之后应当交给 schedule.Fatal 停止重试。
type VerificationError struct {
	Output   string
	Problems []string
}

func (e *VerificationError) Error() string {
	if len(e.Problems) == 1 {
		return fmt.Sprintf("产物不符合规格：%s —— %s", e.Output, e.Problems[0])
	}
	return fmt.Sprintf("产物不符合规格：%s\n  - %s", e.Output, strings.Join(e.Problems, "\n  - "))
}

// VerifyOutput 校验镜头产物是否符合执行契约。
//
// 检查画幅、帧率、时长和「必须静音无音轨」四项，缺一项都会让错误在成片阶段才暴露。
// 另外静态扫描合成源码的确定性（internal/determinism）：读时钟、未播种随机数、
// requestAnimationFrame 这类写法渲染照样成功，要到重渲染对不上时才暴露。
//
//   - 画幅：style_guide 声明了画布，渲染成别的尺寸无法靠后期规范化补救——
//     把 720p 拉伸到 1080 是画质损失而不是格式统一。
//   - 静音：镜头产物按契约就该无音轨，混音在后面的阶段统一做。
//
// 刻意不查编码和像素格式：PROMPT 第八阶段明确允许镜头产物规格不一致，
// 由拼接前的规范化副本统一，在这里拦下会和那条既定流程打架。
func VerifyOutput(s Scene, baseEnv map[string]string, tolerance float64) error {
	if math.IsNaN(tolerance) || math.IsInf(tolerance, 0) || tolerance < 0 {
		return fmt.Errorf("tolerance 必须是有限且非负的数字")
	}
	path := s.OutputPath()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("渲染结束但产物不存在：%s", s.Output)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("渲染产物不是非空普通文件：%s", s.Output)
	}
	root, _ := filepath.EvalSymlinks(s.Directory)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("产物不得链接到镜头目录外：%s", s.Output)
	}
	canvas, err := CanvasOf(s.Directory, s.StyleGuide)
	if err != nil {
		return err
	}
	toolchain, err := mediaprobe.New(baseEnv)
	if err != nil {
		return err
	}
	media, err := toolchain.Probe(path)
	if err != nil {
		return err
	}
	spec := mediaprobe.Spec{
		WidthPx:         canvas.WidthPx,
		HeightPx:        canvas.HeightPx,
		FPS:             canvas.FPS,
		DurationSeconds: s.DurationSeconds,
		Tolerance:       tolerance,
		Audio:           mediaprobe.AudioAbsent,
	}
	problems := spec.Check(media)
	// 帧数逐帧解码实测，不靠时长容差兜：默认 0.15 秒的容差在 30fps 下能放过
	// 差 4 帧的产物，要等拼接后整片复查才暴露，那时整批镜头都已渲染完。
	// VerifyFrameAlignment 已保证时长是整数帧，这里的期望值没有取整歧义。
	// 没有视频流时 spec.Check 已经报过，不再重复解码。
	if media.Video != nil {
		expected := int(math.Round(s.DurationSeconds * float64(canvas.FPS)))
		counted, err := toolchain.CountFrames(path)
		if err != nil {
			problems = append(problems, err.Error())
		} else if counted != expected {
			problems = append(problems, fmt.Sprintf("总帧数不符：期望 %d 帧（%.6f 秒 × %dfps），逐帧解码实测 %d 帧",
				expected, s.DurationSeconds, canvas.FPS, counted))
		}
	}
	// 源码扫描与规格检查一起报：两类问题常同时出现，分两轮发现就要多烧一次渲染。
	scan, err := determinism.Scan(s.Directory)
	if err != nil {
		return err
	}
	problems = append(problems, scan.Problems()...)
	if len(problems) > 0 {
		return &VerificationError{Output: s.Output, Problems: problems}
	}
	return nil
}
