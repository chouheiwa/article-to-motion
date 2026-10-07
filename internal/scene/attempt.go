package scene

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chouheiwa/articale-to-motion/internal/fsutil"
	"github.com/chouheiwa/articale-to-motion/internal/tools"
)

// AttemptLog 是本次渲染尝试的记录文件，与三份日志放在一起、一起归档。
func (s Scene) AttemptLog() string {
	return filepath.Join(s.Directory, "render-"+s.ID+".attempt.json")
}

// Attempt 是一次渲染尝试的机器可读记录。
//
// 日志回答「渲染器说了什么」，这份记录回答「这次花了多久、用了多少、为什么
// 失败、输入是哪一套」：跨多次运行对比镜头耗时、找出反复失败的镜头，都靠它，
// 而这些信息过去只活在当次 run-all 的终端输出里。
type Attempt struct {
	Schema      int          `json:"schema"`
	SceneID     string       `json:"scene_id"`
	Renderer    string       `json:"renderer"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	StartedAt   string       `json:"started_at"`
	EndedAt     string       `json:"ended_at"`
	Seconds     float64      `json:"seconds"`
	Outcome     string       `json:"outcome"`
	ExitCode    *int         `json:"exit_code,omitempty"`
	Error       string       `json:"error,omitempty"`
	Problems    []string     `json:"problems,omitempty"`
	Retry       bool         `json:"retry"`
	Usage       *tools.Usage `json:"usage,omitempty"`
}

// 尝试结果。verification_failed 单列：它是确定性失败，不该与渲染器崩溃混在一起统计。
const (
	OutcomeSucceeded          = "succeeded"
	OutcomeFailed             = "failed"
	OutcomeVerificationFailed = "verification_failed"
	OutcomeInterrupted        = "interrupted"
)

func writeAttempt(s Scene, attempt Attempt) error {
	attempt.Schema = 1
	body, err := json.MarshalIndent(attempt, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.AttemptLog(), append(body, '\n'), 0o644)
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ArchiveAttempt 把上一次尝试留下的产物、日志与尝试记录移进 attempts/attempt-NN，
// 返回该目录；没有任何可归档的文件时返回空字符串，不建空目录。
//
// 渲染开始前与调度层重试前都走这里：日志以截断方式重新打开，不先挪走的话，
// 手动重跑一次镜头就抹掉了上一次的全部现场。
func ArchiveAttempt(s Scene) string {
	var present []string
	for _, path := range []string{s.OutputPath(), s.StreamLog(), s.StderrLog(), s.UserLog(), s.AttemptLog()} {
		if _, err := os.Lstat(path); err == nil {
			present = append(present, path)
		}
	}
	if len(present) == 0 {
		return ""
	}
	attemptsRoot := filepath.Join(s.Directory, "attempts")
	_ = os.MkdirAll(attemptsRoot, 0o755)
	entries, _ := os.ReadDir(attemptsRoot)
	number := len(entries) + 1
	destination := filepath.Join(attemptsRoot, fmt.Sprintf("attempt-%02d", number))
	for {
		if _, err := os.Lstat(destination); os.IsNotExist(err) {
			break
		}
		number++
		destination = filepath.Join(attemptsRoot, fmt.Sprintf("attempt-%02d", number))
	}
	_ = os.MkdirAll(destination, 0o755)
	for _, path := range present {
		_ = os.Rename(path, filepath.Join(destination, filepath.Base(path)))
	}
	return destination
}
