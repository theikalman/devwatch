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
		reason := err.Error()
		var oe *net.OpError
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			reason = "timed out after " + w.Timeout.String()
		case errors.As(err, &oe):
			reason = oe.Err.Error() // "connect: connection refused", without the "dial tcp host:port" prefix
		}
		return value.NewProbe(value.ProbeData{Reason: reason}, nil)
	}
	conn.Close()
	ms := math.Round(float64(time.Since(start).Microseconds())/10) / 100
	return value.NewProbe(value.ProbeData{Up: true}, &ms)
}
