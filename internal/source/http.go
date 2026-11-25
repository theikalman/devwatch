package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

const maxBody = 256 << 10 // bytes of a response body kept

var httpClient = &http.Client{}

// runHTTP requests w.HTTP. Without as, value or match it is a probe: up when the status matches w.Status.
// Otherwise the body is parsed like command output, and an unexpected status is an error.
func runHTTP(ctx context.Context, w config.Watcher) (value.Value, error) {
	var body io.Reader
	if w.Body != "" {
		body = strings.NewReader(w.Body)
	}
	req, err := http.NewRequestWithContext(ctx, w.Method, w.HTTP, body)
	if err != nil {
		return value.Value{}, err
	}
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}
	probe := w.As == "" && w.Value == "" && w.Match == ""
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		if probe {
			return value.NewProbe(value.ProbeData{Reason: reason(ctx, err, w.Timeout)}, nil), nil
		}
		return value.Value{}, fmt.Errorf("%s %s: %s", w.Method, w.HTTP, reason(ctx, err, w.Timeout))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	latency := msSince(start)
	ok := config.StatusOK(w.Status, resp.StatusCode)
	if probe {
		p := value.ProbeData{Up: ok, Code: resp.StatusCode}
		if !ok {
			return value.NewProbe(value.ProbeData{Code: p.Code, Reason: "status " + strconv.Itoa(p.Code)}, nil), nil
		}
		return value.NewProbe(p, latency), nil
	}
	if !ok {
		msg := "HTTP " + resp.Status
		if line := lastLine(string(b)); line != "" {
			msg += ": " + line
		}
		return value.Value{}, fmt.Errorf("%s", msg)
	}
	if err != nil {
		return value.Value{}, fmt.Errorf("reading the response: %w", err)
	}
	return value.Parse(string(b), w.As, w.Value, w.MatchRe)
}
