package watcher

import (
	"context"
	"testing"
	"time"

	"devwatch/internal/config"
)

func TestCommand(t *testing.T) {
	out, err := Check(context.Background(), config.Watcher{Type: "command", Command: "echo hi", Timeout: time.Second})
	if err != nil || out != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := Check(context.Background(), config.Watcher{Type: "command", Command: "exit 3", Timeout: time.Second}); err == nil {
		t.Fatal("expected error on non-zero exit")
	}
}

func TestStream(t *testing.T) {
	var lines []string
	err := Stream(context.Background(), config.Watcher{Command: "echo a; echo b >&2; exit 2"}, func(l string) { lines = append(lines, l) })
	if err == nil || len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("got %v, %v", lines, err)
	}
}

func TestStreamCancelKillsChildren(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- Stream(ctx, config.Watcher{Command: "sleep 30; echo x"}, func(string) {}) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}
}
