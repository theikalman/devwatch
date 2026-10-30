package watcher

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"

	"devwatch/internal/config"
)

// Stream runs a long-lived command (stdout+stderr merged) and calls emit per line until it exits.
// ctx cancellation stops the whole process group, so children of `sh -c` die too.
func Stream(ctx context.Context, w config.Watcher, emit func(line string)) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", w.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			emit(sc.Text())
		}
		io.Copy(io.Discard, pr) // keep draining if a line exceeded the buffer, so the command never blocks
	}()
	err := cmd.Run()
	pw.Close()
	<-done
	return err
}
