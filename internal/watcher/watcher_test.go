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
