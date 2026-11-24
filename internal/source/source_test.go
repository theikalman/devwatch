package source

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

func cmd(c string) config.Watcher { return config.Watcher{Cmd: c, Timeout: 5 * time.Second} }

func TestCmd(t *testing.T) {
	v, err := Fetch(context.Background(), cmd("echo 42"))
	if err != nil || v.Kind != value.Number || v.String() != "42" {
		t.Fatalf("got %s %q, %v", v.Kind, v.String(), err)
	}

	dir := t.TempDir()
	w := cmd(`echo "$FOO $(basename "$PWD")"`)
	w.Env, w.Dir = map[string]string{"FOO": "bar"}, dir
	if v, err := Fetch(context.Background(), w); err != nil || v.String() != "bar "+filepath.Base(dir) {
		t.Fatalf("env and dir: got %q, %v", v.String(), err)
	}

	if _, err := Fetch(context.Background(), cmd("echo first; echo boom >&2; exit 3")); err == nil || err.Error() != "exit status 3: boom" {
		t.Fatalf("exit error = %v", err)
	}

	v, err = Fetch(context.Background(), cmd("head -c 100000 /dev/zero | tr '\\0' a"))
	if err != nil || len(v.Data.(string)) != maxOutput {
		t.Fatalf("output cap: %d bytes, %v", len(v.String()), err)
	}
}

func TestCmdTimeoutKillsChildren(t *testing.T) {
	dir := t.TempDir()
	w := cmd("sleep 30 & echo $! > pid; wait")
	w.Dir, w.Timeout = dir, 300*time.Millisecond
	start := time.Now()
	_, err := Fetch(context.Background(), w)
	if err == nil || err.Error() != "command timed out after 300ms" {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %s", d)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d still running after timeout", pid)
		}
	}
}

func TestPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w := config.Watcher{Port: ln.Addr().String(), Timeout: time.Second}
	v, err := Fetch(context.Background(), w)
	if p := v.Data.(value.ProbeData); err != nil || !p.Up || v.Num == nil {
		t.Fatalf("up: %+v, %v", v, err)
	}
	ln.Close()
	v, err = Fetch(context.Background(), w)
	if p := v.Data.(value.ProbeData); err != nil || p.Up || !strings.Contains(p.Reason, "refused") {
		t.Fatalf("down: %+v, %v", v, err)
	}
}
