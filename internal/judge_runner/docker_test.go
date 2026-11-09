package judge_runner

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"biue/internal/judge"
	"biue/internal/sandbox"
)

type rewriteTransport struct {
	base http.RoundTripper
	host string
}

func (r rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.URL.Scheme = "http"
	request.URL.Host = r.host
	return r.base.RoundTrip(request)
}

type engineFixture struct {
	mu                 sync.Mutex
	next               int
	containers         map[string]map[string]any
	configs            []map[string]any
	tamper, failDelete bool
	compileError       bool
	exitCode           int
	oom                bool
}

func fakeDocker(t *testing.T) (*Docker, *engineFixture) {
	t.Helper()
	f := &engineFixture{containers: map[string]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/v1.46")
		switch {
		case path == "/info":
			json.NewEncoder(w).Encode(map[string]any{"OSType": "linux", "SecurityOptions": []string{"name=seccomp,profile=builtin"}})
		case strings.HasPrefix(path, "/images/"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
			language := ""
			for id, image := range languageImages {
				if image == name {
					language = id
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + strings.Repeat("f", 64), "Config": map[string]any{"Labels": map[string]string{"biue.judge.language": language}}})
		case path == "/containers/create":
			var config map[string]any
			if json.NewDecoder(r.Body).Decode(&config) != nil {
				t.Error("create payload")
			}
			f.configs = append(f.configs, config)
			f.next++
			id := fmt.Sprintf("%064x", f.next)
			f.containers[id] = config
			f.containers[r.URL.Query().Get("name")] = config
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"Id": id})
		case path == "/containers/json":
			json.NewEncoder(w).Encode([]any{})
		case path == "/events":
			if !regexp.MustCompile(`^[0-9]+\.[0-9]{9}$`).MatchString(r.URL.Query().Get("since")) || !regexp.MustCompile(`^[0-9]+\.[0-9]{9}$`).MatchString(r.URL.Query().Get("until")) {
				t.Error("Docker event times must be numeric Unix seconds/nanoseconds")
				w.WriteHeader(400)
				return
			}
			if f.oom {
				var filters map[string][]string
				json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters)
				json.NewEncoder(w).Encode(map[string]any{"Action": "oom", "Actor": map[string]string{"ID": filters["container"][0]}})
			}
		case strings.HasPrefix(path, "/containers/"):
			parts := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
			id := parts[0]
			if r.Method == "DELETE" {
				if f.failDelete {
					w.WriteHeader(500)
				} else {
					delete(f.containers, id)
					w.WriteHeader(204)
				}
				return
			}
			config := f.containers[id]
			if config == nil {
				t.Error("unknown container")
				w.WriteHeader(404)
				return
			}
			switch parts[1] {
			case "start":
				w.WriteHeader(204)
			case "attach":
				env := map[string]string{}
				for _, v := range config["Env"].([]any) {
					pair := strings.SplitN(v.(string), "=", 2)
					env[pair[0]] = pair[1]
				}
				out := sandbox.Outcome{ExitCode: f.exitCode, TimeMS: 1, MemoryKB: 16, MemoryMeasured: true, Stdout: []byte("3\n")}
				if env["OJ_SANDBOX_PHASE"] == "compile" {
					out.Stdout = nil
					out.Artifacts = map[string][]byte{"main": []byte("binary")}
					if env["OJ_SANDBOX_LANGUAGE"] == "java" {
						out.Artifacts = map[string][]byte{"Main.class": []byte("class")}
					}
					if f.compileError {
						out.ExitCode = 1
						out.Stderr = []byte("syntax diagnostic")
					}
				}
				key, _ := hex.DecodeString(env["OJ_SANDBOX_SECRET"])
				signed := sandbox.Sign(out, key)
				if f.tamper {
					signed.Outcome.TimeMS = 0
				}
				data, _ := json.Marshal(signed)
				data = append(data, '\n')
				var frame [8]byte
				frame[0] = 1
				binary.BigEndian.PutUint32(frame[4:], uint32(len(data)))
				connection, buffer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
				buffer.Flush()
				go func() {
					defer connection.Close()
					var input sandbox.Input
					if json.NewDecoder(buffer).Decode(&input) != nil {
						t.Error("sandbox input packet")
						return
					}
					for _, value := range input.Files {
						if bytes.Contains(value, []byte("PRIVATE-EXPECTED-ANSWER")) {
							t.Error("hidden answer entered sandbox")
						}
					}
					connection.Write(frame[:])
					connection.Write(data)
				}()
			default:
				t.Errorf("unexpected Docker operation %s", path)
				w.WriteHeader(500)
			}
		default:
			t.Errorf("unexpected Docker path %s", path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	origin, _ := url.Parse(server.URL)
	d := &Docker{client: &http.Client{Transport: rewriteTransport{base: http.DefaultTransport, host: origin.Host}}, dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Host)
	}, owner: strings.Repeat("a", 32), images: map[string]string{}}
	if err := d.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d, f
}

func TestDockerLanguagePipelineAndIsolationPolicy(t *testing.T) {
	for _, language := range []string{"cpp17", "go", "python3", "java"} {
		t.Run(language, func(t *testing.T) {
			d, f := fakeDocker(t)
			r := testRequest()
			r.Language = language
			r.Kind = judge.Submit
			r.Cases = []judge.Case{{ID: "a", Input: []byte("1 2"), Expected: []byte("3"), Score: 40}, {ID: "b", Expected: []byte("PRIVATE-EXPECTED-ANSWER"), Score: 60}}
			var phases []judge.Status
			result, err := d.Judge(context.Background(), r, func(s judge.Status) error { phases = append(phases, s); return nil })
			if err != nil || result.Status != judge.WrongAnswer || result.Score != 40 || judge.ValidateResult(r, result) != nil {
				t.Fatalf("status=%s score=%d error=%v", result.Status, result.Score, err)
			}
			want := 3
			if language == "python3" {
				want = 2
			}
			if len(f.configs) != want {
				t.Fatalf("sandbox count=%d", len(f.configs))
			}
			for _, config := range f.configs {
				if config["User"] != "10001:10001" || config["NetworkDisabled"] != true || !strings.HasPrefix(config["Image"].(string), "sha256:") {
					t.Fatal("untrusted identity/image/network policy")
				}
				host := config["HostConfig"].(map[string]any)
				if host["ReadonlyRootfs"] != true || host["Privileged"] != false || host["NetworkMode"] != "none" || host["PidsLimit"] != float64(128) || host["NanoCpus"] != float64(1000000000) || host["Memory"] != host["MemorySwap"] {
					t.Fatal("missing sandbox resource constraints")
				}
				if _, exists := host["Binds"]; exists {
					t.Fatal("host bind allowed")
				}
				mounts := host["Mounts"].([]any)
				if len(mounts) != 2 {
					t.Fatal("missing typed tmpfs mounts")
				}
				for _, raw := range mounts {
					mount := raw.(map[string]any)
					if mount["Type"] != "tmpfs" || mount["Source"] != nil || mount["ReadOnly"] != false {
						t.Fatal("non-tmpfs mount allowed")
					}
				}
				if host["CapDrop"].([]any)[0] != "ALL" || host["SecurityOpt"].([]any)[0] != "no-new-privileges:true" {
					t.Fatal("privilege constraints")
				}
				if host["Tmpfs"] != nil || len(host["Ulimits"].([]any)) != 3 {
					t.Fatal("missing disk/descriptor limits")
				}
				if host["LogConfig"].(map[string]any)["Type"] != "none" {
					t.Fatal("sandbox output persisted into daemon logs")
				}
			}
		})
	}
}

func TestDockerCompileFailureTamperingAndCleanupDebt(t *testing.T) {
	d, f := fakeDocker(t)
	f.compileError = true
	r := testRequest()
	r.Language = "cpp17"
	result, err := d.Judge(context.Background(), r, func(judge.Status) error { return nil })
	if err != nil || result.Status != judge.CompileError || result.ErrorMessage != "syntax diagnostic" {
		t.Fatalf("status=%s error=%v", result.Status, err)
	}
	f.compileError = false
	f.tamper = true
	if _, err := d.Judge(context.Background(), r, func(judge.Status) error { return nil }); err != ErrSandbox {
		t.Fatal("forged signed telemetry accepted")
	}
	f.tamper = false
	f.failDelete = true
	if _, err := d.Judge(context.Background(), r, func(judge.Status) error { return nil }); err != ErrSandbox {
		t.Fatal("cleanup failure ignored")
	}
	if d.Ready(context.Background()) == nil {
		t.Fatal("cleanup debt still admitted work")
	}
	f.failDelete = false
	if d.Cleanup(context.Background(), false) != nil || d.Ready(context.Background()) != nil {
		t.Fatal("cleanup compensation did not recover readiness")
	}
}

func TestArtifactsRejectUnsafeNamesAndMissingEntrypoint(t *testing.T) {
	for _, name := range []string{"../main", "/main", "work/main", "bad\\main", "main.cpp"} {
		if validateArtifacts(map[string][]byte{name: []byte("binary")}, "cpp17") == nil {
			t.Fatal("unsafe artifact name")
		}
	}
	if validateArtifacts(map[string][]byte{"Foo.class": []byte("class")}, "java") == nil {
		t.Fatal("missing Main entrypoint")
	}
}

func TestTokenComparatorAndVerdicts(t *testing.T) {
	if !equalTokens([]byte("1\r\n2 \t"), []byte("1 2\n")) || equalTokens([]byte("12"), []byte("1 2")) {
		t.Fatal("token comparison")
	}
	for _, c := range []struct {
		out  sandbox.Outcome
		want judge.Status
	}{{sandbox.Outcome{ExitCode: 0}, judge.Accepted}, {sandbox.Outcome{ExitCode: 137, MemoryExceeded: true}, judge.MemoryLimitExceeded}, {sandbox.Outcome{ExitCode: 137}, judge.RuntimeError}, {sandbox.Outcome{ExitCode: 139}, judge.RuntimeError}, {sandbox.Outcome{ExitCode: 137, TimedOut: true}, judge.TimeLimitExceeded}, {sandbox.Outcome{ExitCode: 137, OutputExceeded: true}, judge.RuntimeError}} {
		if verdict(c.out) != c.want {
			t.Fatal("wrong resource verdict")
		}
	}
}

func TestOOMRequiresAnEventNotJustSIGKILL(t *testing.T) {
	for _, oom := range []bool{false, true} {
		t.Run(fmt.Sprint(oom), func(t *testing.T) {
			d, f := fakeDocker(t)
			f.exitCode = 137
			f.oom = oom
			result, err := d.Judge(context.Background(), testRequest(), func(judge.Status) error { return nil })
			want := judge.RuntimeError
			if oom {
				want = judge.MemoryLimitExceeded
			}
			if err != nil || result.Status != want {
				t.Fatalf("status=%s want=%s error=%v", result.Status, want, err)
			}
		})
	}
}
