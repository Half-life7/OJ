package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestWorkerAndRunnerConfiguration(t *testing.T) {
	base, err := fromMap(validEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"RUNNER_URL": "http://runner:8083", "RUNNER_TOKEN": "d8!L0mA2qC4vE6xG8zJ1nP3rT5wY7bK9"}
	get := func(k string) string { return env[k] }
	w, err := WorkerFrom(get, base)
	if err != nil || !strings.HasPrefix(w.Consumer, "worker-") {
		t.Fatal(err)
	}
	r, err := RunnerFrom(get)
	if err != nil || r.Concurrency != 2 {
		t.Fatal(err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("config", "worker", w, "runner", r)
	if strings.Contains(log.String(), env["RUNNER_TOKEN"]) || strings.Contains(fmt.Sprintf("%#v %#v", w, r), env["RUNNER_TOKEN"]) {
		t.Fatal("runner credential logged")
	}
	for _, c := range []struct{ k, v string }{{"RUNNER_TOKEN", env["RUNNER_TOKEN"] + "\n"}, {"RUNNER_TOKEN", string(base.JWTSecret)}, {"WORKER_CONSUMER", "bad name"}, {"WORKER_HEALTH_ADDR", ":0"}, {"RUNNER_URL", "http://runner:8083/path"}} {
		saved := env[c.k]
		env[c.k] = c.v
		if _, err := WorkerFrom(get, base); err == nil {
			t.Fatalf("accepted %s", c.k)
		}
		env[c.k] = saved
	}
	base.Env = "production"
	if _, err := WorkerFrom(get, base); err == nil {
		t.Fatal("production plaintext Runner")
	}
	env["APP_ENV"] = "production"
	if _, err := RunnerFrom(get); err == nil {
		t.Fatal("production Runner without TLS")
	}
}
