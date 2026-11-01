//go:build darwin || linux

// Package procgroup 以独立进程组启动外部 AI CLI，并在取消时整组终止。
//
// AI CLI 会再拉起 node、npx、ffmpeg 这类子进程。只杀直接子进程的话，孙进程会被
// 托管给 init 继续跑，渲染器在后台接着写文件、占 CPU，下一次尝试和它抢同一个目录。
// 所以统一用 Setpgid 开新进程组，取消时对 -pid 发信号。
package procgroup

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// DefaultGrace 是 SIGTERM 之后等进程自行退出的时间，超时改发 SIGKILL。
const DefaultGrace = 500 * time.Millisecond

// Start 以独立进程组启动 cmd。ctx 取消时先对整组发 SIGTERM，grace 内没退出再发 SIGKILL。
//
// 返回的 wait 必须调用且只调用一次，并且要在读完 cmd 的输出管道之后再调用——
// 这与 exec.Cmd.Wait 的约束相同。
func Start(ctx context.Context, cmd *exec.Cmd, grace time.Duration) (wait func() error, err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	return func() error {
		err := cmd.Wait()
		close(done)
		return err
	}, nil
}

// Run 启动 cmd 并等它结束。ctx 取消时返回 ctx.Err()，而不是进程被信号杀掉的错误。
func Run(ctx context.Context, cmd *exec.Cmd, grace time.Duration) error {
	wait, err := Start(ctx, cmd, grace)
	if err != nil {
		return err
	}
	err = wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
