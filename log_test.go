package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendLog(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 30, 19, 1, 7, 0, time.UTC)
	if err := appendLog(dir, "git status/1", at, "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := appendLog(dir, "git status/1", at.Add(2*time.Second), "b", errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "git_status_1.log"))
	want := "=== 2026-10-30 19:01:07 ===\na\n\n=== 2026-10-30 19:01:09 ===\nERROR: boom\nb\n\n"
	if err != nil || string(b) != want {
		t.Fatalf("got %q, %v", b, err)
	}
}
