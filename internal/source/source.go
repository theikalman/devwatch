// Package source runs one check for a watcher and returns its typed value.
package source

import (
	"context"
	"errors"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

// Fetch runs one check of w within its timeout. An error means devwatch could not get a value;
// a probe that finds its target down is a value (up: false), not an error.
func Fetch(ctx context.Context, w config.Watcher) (value.Value, error) {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	switch w.Source() {
	case "cmd":
		out, err := runCmd(ctx, w)
		if err != nil {
			return value.Value{}, err
		}
		return value.Parse(out, w.As, w.Value, w.MatchRe)
	case "port":
		return probePort(ctx, w), nil
	}
	return value.Value{}, errors.New("no source")
}
