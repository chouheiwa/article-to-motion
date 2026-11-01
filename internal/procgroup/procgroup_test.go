//go:build darwin || linux

package procgroup

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestRunReturnsProcessError(t *testing.T) {
	err := Run(context.Background(), exec.Command("sh", "-c", "exit 4"), DefaultGrace)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("err=%v", err)
	}
	if err := Run(context.Background(), exec.Command("true"), DefaultGrace); err != nil {
		t.Fatal(err)
	}
}

func TestRunEscalatesToSIGKILL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	// 忽略 SIGTERM 的进程只能靠宽限期后的 SIGKILL 结束。
	err := Run(ctx, exec.Command("sh", "-c", "trap '' TERM; sleep 30"), 300*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("没有升级到 SIGKILL：%v", elapsed)
	}
}

func TestStartFailsForMissingBinary(t *testing.T) {
	if _, err := Start(context.Background(), exec.Command("/nonexistent/binary"), DefaultGrace); err == nil {
		t.Fatal("expected start error")
	}
}
