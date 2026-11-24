package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"devwatch/internal/config"
)

const maxOutput = 64 << 10 // bytes of command output kept

// runCmd runs w.Cmd with sh -c and returns stdout and stderr together.
// A non-zero exit is an error carrying the output's last line.
func runCmd(ctx context.Context, w config.Watcher) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", w.Cmd)
	cmd.Dir = w.Dir
	cmd.Env = os.Environ()
	for k, v := range w.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Own process group, so a timeout also kills what sh -c started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second // a child that escaped the group can't hold the output pipe open forever
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("command timed out after %s", w.Timeout)
	}
	if err != nil {
		if msg := lastLine(out.b.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return out.b.String(), nil
}

// capped keeps the first maxOutput bytes and drops the rest, so a chatty command can't grow memory.
type capped struct{ b bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOutput - c.b.Len(); room > 0 {
		c.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}
