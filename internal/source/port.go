package source

import (
	"context"
	"errors"
	"math"
	"net"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

// probePort dials w.Port over TCP: up with the connect latency, or down with the reason.
func probePort(ctx context.Context, w config.Watcher) value.Value {
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", w.Port)
	if err != nil {
		return value.NewProbe(value.ProbeData{Reason: reason(ctx, err, w.Timeout)}, nil)
	}
	conn.Close()
	return value.NewProbe(value.ProbeData{Up: true}, msSince(start))
}

// reason says in a few words why a connection failed: "timed out after 5s", "connect: connection refused".
func reason(ctx context.Context, err error, timeout time.Duration) string {
	var oe *net.OpError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out after " + timeout.String()
	case errors.As(err, &oe):
		return oe.Err.Error() // without the "dial tcp host:port" prefix
	}
	return err.Error()
}

// msSince is the time since start in milliseconds, to 0.01 ms.
func msSince(start time.Time) *float64 {
	ms := math.Round(float64(time.Since(start).Microseconds())/10) / 100
	return &ms
}
