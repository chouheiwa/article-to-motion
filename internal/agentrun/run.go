package agentrun

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/chouheiwa/articale-to-motion/internal/procgroup"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// Spec 是一次编排调用。
type Spec struct {
	Tool    string
	Workdir string
	Prompt  string
	Unsafe  bool
	// SessionID 与 Resume 的含义同 tools.OrchestratorOptions。
	SessionID string
	Resume    bool
	Access    tools.OrchestratorAccess
	// Env 是子进程的完整环境（KEY=VALUE），由调用方按安全模式白名单算好。
	Env []string
	// LookPath 解析 argv[0]；为 nil 时用 exec.LookPath。
	LookPath func(string) (string, error)
	// Raw 若非 nil，原样写入每一行输出，供排查格式变化。
	Raw io.Writer
}

// Result 是一次调用结束后的汇总。
type Result struct {
	SessionID string
	Usage     tools.Usage
	ExitCode  int
	// Produced 表示流里出现过正文、思考或工具事件。续接失败时 CLI 往往只打一行
	// 错误就退出，调用方据此决定要不要开新会话重试。
	Produced bool
	// Stderr 是标准错误的末尾，失败时用来给人看原因。
	Stderr string
}

// ExitError 表示编排工具以非零退出码结束。
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("编排工具退出码 %d：%s", e.Code, e.Stderr)
	}
	return fmt.Sprintf("编排工具退出码 %d", e.Code)
}

// NewSessionID 为支持预先指定会话的工具生成一个新 id，其余工具返回空串。
func NewSessionID(tool string) string {
	if !tools.PresetSessionID(tool) {
		return ""
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const stderrTail = 2000

// tailBuffer 只保留最后 n 个字节。
type tailBuffer struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(bytes.ToValidUTF8(t.buf, nil)))
}

// Run 启动编排工具，逐条回调事件，直到进程结束。
//
// emit 在调用方的 goroutine 之外不会被并发调用。最后一定会收到一条 KindDone；
// 用量读得到时在它之前还有一条 KindUsage。ctx 取消时整组终止，返回 ctx.Err()。
func Run(ctx context.Context, spec Spec, emit func(Event)) (Result, error) {
	parser, err := NewParser(spec.Tool)
	if err != nil {
		return Result{}, err
	}
	argv, stdin, err := tools.OrchestratorInvocation(spec.Tool, tools.OrchestratorOptions{
		Workdir: spec.Workdir, Prompt: spec.Prompt, Unsafe: spec.Unsafe,
		SessionID: spec.SessionID, Resume: spec.Resume, Access: spec.Access,
	})
	if err != nil {
		return Result{}, err
	}
	lookPath := spec.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath(argv[0])
	if err != nil {
		return Result{}, fmt.Errorf("缺少必需工具：%s", argv[0])
	}
	cmd := exec.Command(binary, argv[1:]...)
	cmd.Dir = spec.Workdir
	cmd.Env = spec.Env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stderr := &tailBuffer{n: stderrTail}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	wait, err := procgroup.Start(ctx, cmd, procgroup.DefaultGrace)
	if err != nil {
		return Result{}, fmt.Errorf("启动编排工具：%w", err)
	}
	// 预先指定的 id 不等流里回报，先发出去：调用方要立刻把它落盘，进程中途崩了也能续接。
	if spec.SessionID != "" {
		parser.session = spec.SessionID
		emit(Event{Kind: KindSession, SessionID: spec.SessionID})
	}
	result := Result{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if spec.Raw != nil {
			fmt.Fprintln(spec.Raw, line)
		}
		for _, event := range parser.Parse(line) {
			switch event.Kind {
			case KindText, KindThinking, KindToolCall, KindToolResult:
				result.Produced = true
			}
			emit(event)
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		// 读失败后不再读管道，子进程可能卡在写满的管道上，先让它退出再等。
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := wait()
	result.SessionID = parser.SessionID()
	result.Usage = parser.Usage()
	result.Stderr = stderr.String()
	if !result.Usage.Empty() {
		usage := result.Usage
		emit(Event{Kind: KindUsage, Usage: &usage})
	}
	defer emit(Event{Kind: KindDone, SessionID: result.SessionID})
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if scanErr != nil {
		return result, fmt.Errorf("读取编排工具输出：%w", scanErr)
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			result.ExitCode = exit.ExitCode()
			return result, &ExitError{Code: result.ExitCode, Stderr: result.Stderr}
		}
		return result, waitErr
	}
	return result, nil
}
