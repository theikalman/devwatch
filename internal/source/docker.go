package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

// Sockets tried in order when neither socket: nor DOCKER_HOST is set: Docker's default (also where Podman's
// macOS helper and Colima's docker context link to), Docker Desktop, Colima, Podman on macOS, Podman on Linux.
func socketCandidates() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/var/run/docker.sock",
		filepath.Join(home, ".docker/run/docker.sock"),
		filepath.Join(home, ".colima/default/docker.sock"),
		filepath.Join(home, ".local/share/containers/podman/machine/podman.sock"),
		filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "podman/podman.sock"),
	}
}

func findSocket(w config.Watcher) (string, error) {
	if w.Socket != "" {
		return w.Socket, nil
	}
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		if p, ok := strings.CutPrefix(h, "unix://"); ok {
			return p, nil
		}
		return "", fmt.Errorf("DOCKER_HOST=%s: only unix:// sockets work; set socket: instead", h)
	}
	for _, p := range socketCandidates() {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return p, nil
		}
	}
	return "", errors.New("no Docker or Podman socket found; start Docker, Colima or Podman, or set socket:")
}

var (
	dockerMu      sync.Mutex
	dockerClients = map[string]*http.Client{}
)

// dockerClient talks to the Docker Engine API over a unix socket; Podman serves the same API.
func dockerClient(socket string) *http.Client {
	dockerMu.Lock()
	defer dockerMu.Unlock()
	if c, ok := dockerClients[socket]; ok {
		return c
	}
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	dockerClients[socket] = c
	return c
}

func dockerGet(ctx context.Context, c *http.Client, socket, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("docker at %s: %s", socket, reason(ctx, err, 0))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Message string }
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(b, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("docker: %s: %s", resp.Status, e.Message)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// runDocker lists the containers w.Docker picks, with each one's state, health and restart count.
func runDocker(ctx context.Context, w config.Watcher) (value.Value, error) {
	socket, err := findSocket(w)
	if err != nil {
		return value.Value{}, err
	}
	c := dockerClient(socket)
	d := w.Docker
	filter := map[string][]string{}
	switch {
	case d.Container != "":
		filter["name"] = []string{d.Container} // a substring match; exact names are kept below
	case d.Compose != "":
		filter["label"] = []string{"com.docker.compose.project=" + d.Compose}
	default:
		filter["label"] = []string{d.Label}
	}
	f, _ := json.Marshal(filter)
	var list []struct {
		ID     string `json:"Id"`
		Names  []string
		Image  string
		Labels map[string]string
	}
	if err := dockerGet(ctx, c, socket, "/containers/json?all=1&filters="+url.QueryEscape(string(f)), &list); err != nil {
		return value.Value{}, err
	}
	cs := []value.ContainerData{}
	for _, item := range list {
		name := strings.TrimPrefix(firstOr(item.Names, item.ID), "/")
		if d.Container != "" && name != d.Container {
			continue
		}
		if svc := item.Labels["com.docker.compose.service"]; d.Compose != "" && svc != "" {
			name = svc
		}
		var in struct {
			RestartCount int
			State        struct {
				Status     string
				ExitCode   int
				StartedAt  time.Time
				FinishedAt time.Time
				Health     *struct{ Status string }
			}
		}
		if err := dockerGet(ctx, c, socket, "/containers/"+item.ID+"/json", &in); err != nil {
			return value.Value{}, err
		}
		cd := value.ContainerData{Name: name, Image: item.Image, State: in.State.Status, Restarts: in.RestartCount, Started: unixMS(in.State.StartedAt)}
		if in.State.Health != nil {
			cd.Health = in.State.Health.Status
		}
		if cd.State != "running" {
			cd.ExitCode, cd.Finished = in.State.ExitCode, unixMS(in.State.FinishedAt)
		}
		cs = append(cs, cd)
	}
	slices.SortFunc(cs, func(a, b value.ContainerData) int { return strings.Compare(a.Name, b.Name) })
	return value.NewContainers(cs), nil
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

// unixMS is t in unix ms, or 0 for the zero time Docker reports for "never".
func unixMS(t time.Time) int64 {
	if t.Year() <= 1 {
		return 0
	}
	return t.UnixMilli()
}
