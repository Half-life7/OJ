package judge_runner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"biue/internal/judge"
	"biue/internal/sandbox"
)

var ErrSandbox = errors.New("sandbox operation failed")
var identifier = regexp.MustCompile(`^[a-f0-9]{64}$`)

var languageImages = map[string]string{"cpp17": "biue-lang-cpp17:0.1.0", "go": "biue-lang-go:0.1.0", "python3": "biue-lang-python3:0.1.0", "java": "biue-lang-java:0.1.0", "javascript": "biue-lang-javascript:0.1.0"}

type Docker struct {
	client      *http.Client
	dial        func(context.Context) (net.Conn, error)
	owner       string
	mu          sync.RWMutex
	images      map[string]string
	cleanupDebt map[string]bool
}

func NewDocker(socket, owner string) (*Docker, error) {
	if runtime.GOOS != "linux" || !filepath.IsAbs(socket) || len(owner) != 32 {
		return nil, ErrSandbox
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }, ResponseHeaderTimeout: 5 * time.Second, MaxConnsPerHost: 16, IdleConnTimeout: time.Minute, DisableCompression: true}
	return &Docker{client: &http.Client{Transport: transport}, dial: dial, owner: owner, images: map[string]string{}}, nil
}
func (d *Docker) Close() { d.client.CloseIdleConnections() }

func (d *Docker) call(ctx context.Context, method, path string, body io.Reader, content string) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.46"+path, body)
	if err != nil {
		return nil, ErrSandbox
	}
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	response, err := d.client.Do(r)
	if err != nil {
		slog.Error("docker request failed", "method", method, "operation", dockerOperation(path))
		return nil, ErrSandbox
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		slog.Error("docker request rejected", "method", method, "operation", dockerOperation(path), "status", response.StatusCode)
		response.Body.Close()
		return nil, ErrSandbox
	}
	return response, nil
}

func dockerOperation(path string) string {
	switch {
	case strings.HasPrefix(path, "/images/"):
		return "image_inspect"
	case strings.HasPrefix(path, "/containers/create"):
		return "container_create"
	case strings.Contains(path, "/archive"):
		return "container_archive"
	case strings.Contains(path, "/logs"):
		return "container_logs"
	case strings.HasPrefix(path, "/events"):
		return "oom_events"
	case path == "/info":
		return "engine_info"
	default:
		return "container_control"
	}
}
func (d *Docker) json(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return ErrSandbox
		}
		body = bytes.NewReader(raw)
	}
	r, err := d.call(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if output != nil && json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(output) != nil {
		return ErrSandbox
	}
	return nil
}

func (d *Docker) Ready(ctx context.Context) error {
	d.mu.RLock()
	blocked := len(d.cleanupDebt) > 0
	d.mu.RUnlock()
	if blocked {
		return ErrSandbox
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var info struct {
		OSType          string
		SecurityOptions []string
	}
	if d.json(ctx, "GET", "/info", nil, &info) != nil || info.OSType != "linux" {
		return ErrSandbox
	}
	seccomp := false
	for _, option := range info.SecurityOptions {
		if strings.HasPrefix(option, "name=seccomp") {
			seccomp = true
		}
	}
	if !seccomp {
		return ErrSandbox
	}
	images := map[string]string{}
	for language, name := range languageImages {
		var image struct {
			ID     string `json:"Id"`
			Config struct {
				Labels  map[string]string
				Volumes map[string]any
			}
		}
		if d.json(ctx, "GET", "/images/"+url.PathEscape(name)+"/json", nil, &image) != nil || !strings.HasPrefix(image.ID, "sha256:") || !identifier.MatchString(strings.TrimPrefix(image.ID, "sha256:")) || image.Config.Labels["biue.judge.language"] != language || len(image.Config.Volumes) != 0 {
			return ErrSandbox
		}
		images[language] = image.ID
	}
	d.mu.Lock()
	for language, id := range d.images {
		if images[language] != id {
			d.mu.Unlock()
			return ErrSandbox
		}
	}
	d.images = images
	d.mu.Unlock()
	return nil
}

func taskKey(t judge.TaskEnvelope) string {
	return fmt.Sprintf("%d-%d-%d-%d", t.TaskID, t.SubmissionID, t.Generation, t.Attempt)
}

func (d *Docker) remove(name string) (result error) {
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if result != nil {
			if d.cleanupDebt == nil {
				d.cleanupDebt = map[string]bool{}
			}
			d.cleanupDebt[name] = true
		} else {
			delete(d.cleanupDebt, name)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "DELETE", "http://docker/v1.46/containers/"+name+"?force=true&v=true", nil)
	if err != nil {
		return ErrSandbox
	}
	response, err := d.client.Do(r)
	if err != nil {
		return ErrSandbox
	}
	defer response.Body.Close()
	if response.StatusCode != 204 && response.StatusCode != 404 {
		return ErrSandbox
	}
	return nil
}

// Cleanup can only select this journal owner's labeled containers. On process
// startup all are orphaned; the periodic pass removes only expired sandboxes.
func (d *Docker) Cleanup(ctx context.Context, all bool) error {
	d.mu.RLock()
	pending := make([]string, 0, len(d.cleanupDebt))
	for name := range d.cleanupDebt {
		pending = append(pending, name)
	}
	d.mu.RUnlock()
	for _, name := range pending {
		if d.remove(name) != nil {
			return ErrSandbox
		}
	}
	filters, _ := json.Marshal(map[string][]string{"label": {"biue.managed=true", "biue.owner=" + d.owner}})
	var containers []struct {
		ID     string `json:"Id"`
		Labels map[string]string
	}
	if d.json(ctx, "GET", "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &containers) != nil {
		return ErrSandbox
	}
	for _, container := range containers {
		if !identifier.MatchString(container.ID) || container.Labels["biue.owner"] != d.owner || container.Labels["biue.managed"] != "true" {
			return ErrSandbox
		}
		expires, err := strconv.ParseInt(container.Labels["biue.expires"], 10, 64)
		if all || err != nil || expires <= time.Now().Unix() {
			if d.remove(container.ID) != nil {
				return ErrSandbox
			}
		}
	}
	return nil
}

// sandbox starts only the immutable supervisor in an isolated, bounded tmpfs.
// No user-selected Docker parameters, host paths, commands or images are used.
func (d *Docker) runSandbox(ctx context.Context, request judge.Request, compile bool, files map[string][]byte, input []byte) (out sandbox.Outcome, artifacts map[string][]byte, err error) {
	started := time.Now().Add(-time.Second)
	d.mu.RLock()
	image := d.images[request.Language]
	d.mu.RUnlock()
	if image == "" {
		return out, nil, ErrSandbox
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return out, nil, ErrSandbox
	}
	name := "biue-" + d.owner[:8] + "-" + hex.EncodeToString(random[:8])
	secret := hex.EncodeToString(random[:])
	defer func() {
		if d.remove(name) != nil {
			err = ErrSandbox
		}
	}()
	phase := "run"
	memory, work, tmp, limit, output, nofile := request.MemoryLimitMB, int64(64), int64(32), request.TimeLimitMS, judge.MaxCaseBytes, 64
	if request.Kind == judge.Run {
		output = judge.MaxRunBytes
	}
	if compile {
		phase = "compile"
		memory, work, tmp, limit, output, nofile = 1024, 128, 512, 30000, judge.MaxRunBytes, 1024
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(limit)*time.Millisecond+30*time.Second)
	defer cancel()
	config := map[string]any{
		"Image": image, "User": "10001:10001", "WorkingDir": "/work", "Entrypoint": []string{"/usr/local/bin/oj-sandbox"}, "Cmd": []string{}, "NetworkDisabled": true, "AttachStdout": false, "AttachStderr": false, "OpenStdin": true, "StdinOnce": true, "Tty": false,
		"Env":        []string{"OJ_SANDBOX_LANGUAGE=" + request.Language, "OJ_SANDBOX_PHASE=" + phase, "OJ_SANDBOX_TIME_MS=" + strconv.FormatInt(limit, 10), "OJ_SANDBOX_OUTPUT_BYTES=" + strconv.Itoa(output), "OJ_SANDBOX_SECRET=" + secret},
		"Labels":     map[string]string{"biue.managed": "true", "biue.owner": d.owner, "biue.task": taskKey(request.Task), "biue.expires": strconv.FormatInt(time.Now().Add(time.Duration(limit)*time.Millisecond+45*time.Second).Unix(), 10)},
		"HostConfig": sandboxPolicy(memory, work, tmp, nofile),
	}
	var created struct {
		ID       string `json:"Id"`
		Warnings []string
	}
	if d.json(ctx, "POST", "/containers/create?name="+name, config, &created) != nil || !identifier.MatchString(created.ID) || len(created.Warnings) > 0 {
		slog.Error("sandbox creation failed", "policy_warning", len(created.Warnings) > 0)
		return out, nil, ErrSandbox
	}
	stream, err := d.attach(ctx, created.ID)
	if err != nil {
		return out, nil, err
	}
	defer stream.Close()
	if d.json(ctx, "POST", "/containers/"+created.ID+"/start", nil, nil) != nil {
		return out, nil, ErrSandbox
	}
	writeDone := make(chan error, 1)
	go func() {
		err := json.NewEncoder(stream.conn).Encode(sandbox.Input{Files: files, Stdin: input})
		if half, ok := stream.conn.(interface{ CloseWrite() error }); ok && err == nil {
			err = half.CloseWrite()
		} else if !ok && err == nil {
			err = ErrSandbox
		}
		writeDone <- err
	}()
	var signed sandbox.Signed
	err = json.NewDecoder(io.LimitReader(&logReader{source: stream.reader}, 32<<20)).Decode(&signed)
	if err != nil {
		stream.Close()
		slog.Error("sandbox result decode failed")
		var inspect struct{ State struct{ OOMKilled bool } }
		if d.json(ctx, "GET", "/containers/"+created.ID+"/json", nil, &inspect) == nil && inspect.State.OOMKilled {
			return sandbox.Outcome{ExitCode: 137, MemoryExceeded: true}, nil, nil
		}
		return out, nil, ErrSandbox
	}
	if <-writeDone != nil {
		return out, nil, ErrSandbox
	}
	if !sandbox.Verify(signed, random[:]) || signed.Outcome.InternalError || signed.Outcome.TimeMS < 0 || signed.Outcome.MemoryKB < 0 || len(signed.Outcome.Stdout) > output || len(signed.Outcome.Stderr) > judge.MaxRunBytes {
		slog.Error("sandbox result rejected", "signature_valid", sandbox.Verify(signed, random[:]), "supervisor_error", signed.Outcome.InternalError)
		return out, nil, ErrSandbox
	}
	out = signed.Outcome
	if out.ExitCode == 137 && !out.TimedOut && !out.OutputExceeded {
		// The daemon's OOM notification can trail the child's wait status.
		for attempt := 0; attempt < 5; attempt++ {
			out.MemoryExceeded, err = d.oomEvent(ctx, created.ID, started)
			if err != nil {
				return out, nil, err
			}
			if out.MemoryExceeded || attempt == 4 {
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return out, nil, ErrSandbox
			case <-timer.C:
			}
		}
	}
	if request.Language == "java" && out.ExitCode != 0 && bytes.Contains(out.Stderr, []byte("java.lang.OutOfMemoryError")) {
		out.MemoryExceeded = true
	}
	if compile && out.ExitCode == 0 && !out.TimedOut && !out.OutputExceeded {
		artifacts = out.Artifacts
		if validateArtifacts(artifacts, request.Language) != nil {
			return out, nil, ErrArtifact
		}
	} else if !compile && len(out.Artifacts) != 0 {
		return out, nil, ErrSandbox
	}
	out.Artifacts = nil
	return out, artifacts, nil
}

func sandboxPolicy(memory, work, tmp int64, nofile int) map[string]any {
	return map[string]any{
		"NetworkMode": "none", "ReadonlyRootfs": true, "Privileged": false,
		"CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"},
		"PidsLimit": 128, "Memory": memory << 20, "MemorySwap": memory << 20, "NanoCpus": int64(1000000000),
		"OomKillDisable": false, "ShmSize": int64(16 << 20), "IpcMode": "private", "CgroupnsMode": "private",
		"Mounts": []map[string]any{
			{"Type": "tmpfs", "Target": "/work", "ReadOnly": false, "TmpfsOptions": map[string]any{"SizeBytes": work << 20, "Mode": 0o1777, "Options": [][]string{{"exec"}}}},
			{"Type": "tmpfs", "Target": "/tmp", "ReadOnly": false, "TmpfsOptions": map[string]any{"SizeBytes": tmp << 20, "Mode": 0o1777, "Options": [][]string{{"noexec"}}}},
		},
		"Ulimits": []map[string]any{{"Name": "nofile", "Soft": nofile, "Hard": nofile}, {"Name": "fsize", "Soft": int64(32 << 20), "Hard": int64(32 << 20)}, {"Name": "core", "Soft": 0, "Hard": 0}},
		// Signed output is consumed on the attached pipe, not persisted to logs.
		"LogConfig": map[string]any{"Type": "none"},
	}
}

type attached struct {
	conn   net.Conn
	reader *bufio.Reader
	stop   func() bool
}

func (a *attached) Close() { a.stop(); a.conn.Close() }
func (d *Docker) attach(ctx context.Context, id string) (*attached, error) {
	if d.dial == nil {
		return nil, ErrSandbox
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, ErrSandbox
	}
	stream := &attached{conn: conn, reader: bufio.NewReader(conn)}
	stream.stop = context.AfterFunc(ctx, func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	r, _ := http.NewRequest("POST", "http://docker/v1.46/containers/"+id+"/attach?stream=1&stdin=1&stdout=1&stderr=0", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "tcp")
	if r.Write(conn) != nil {
		stream.Close()
		return nil, ErrSandbox
	}
	response, err := http.ReadResponse(stream.reader, r)
	if err != nil || (response.StatusCode != 101 && response.StatusCode != 200) {
		stream.Close()
		return nil, ErrSandbox
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Time{})
	}
	return stream, nil
}

func validateArtifacts(files map[string][]byte, language string) error {
	if len(files) == 0 || len(files) > judge.MaxArtifactEntries {
		return ErrArtifact
	}
	var total int64
	for name, data := range files {
		if !(name == "main" && (language == "cpp17" || language == "go") || language == "java" && className.MatchString(name)) {
			return ErrArtifact
		}
		total += int64(len(data))
		if total > judge.MaxArtifactBytes {
			return ErrArtifact
		}
	}
	needed := "main"
	if language == "java" {
		needed = "Main.class"
	}
	if _, ok := files[needed]; !ok {
		return ErrArtifact
	}
	return nil
}

func (d *Docker) oomEvent(ctx context.Context, id string, since time.Time) (bool, error) {
	filters, _ := json.Marshal(map[string][]string{"container": {id}, "event": {"oom"}, "type": {"container"}})
	stamp := func(t time.Time) string { return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond()) }
	path := "/events?since=" + stamp(since) + "&until=" + stamp(time.Now()) + "&filters=" + url.QueryEscape(string(filters))
	r, err := d.call(ctx, "GET", path, nil, "")
	if err != nil {
		return false, err
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	for i := 0; i < 64; i++ {
		var event struct {
			Action string
			Actor  struct{ ID string }
		}
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, ErrSandbox
		}
		if event.Action == "oom" && event.Actor.ID == id {
			return true, nil
		}
	}
	return false, ErrSandbox
}
