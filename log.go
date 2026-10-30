package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func openLog(dir, name string) (*os.File, error) {
	return os.OpenFile(filepath.Join(dir, unsafeName.ReplaceAllString(name, "_")+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// appendLog appends one timestamped section to <dir>/<watcher name>.log.
// shortcut: reopens the file per write, fine at seconds-level intervals.
func appendLog(dir, name string, at time.Time, out string, checkErr error) error {
	f, err := openLog(dir, name)
	if err != nil {
		return err
	}
	defer f.Close()
	body := out
	if checkErr != nil {
		body = "ERROR: " + checkErr.Error() + "\n" + out
	}
	_, err = fmt.Fprintf(f, "=== %s ===\n%s\n\n", at.Format("2006-01-02 15:04:05"), body)
	return err
}
