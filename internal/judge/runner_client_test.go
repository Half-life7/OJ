package judge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunnerClientRejectsInvalidStreams(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong content type", "missing result", "wrong task", "result before running", "duplicate phase", "two results", "unknown field", "huge event", "invalid UTF8", "busy"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 32) {
					t.Error("missing runner credential")
				}
				if scenario == "busy" {
					w.WriteHeader(503)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				if scenario == "wrong content type" {
					w.Header().Set("Content-Type", "application/json")
				}
				encoder := json.NewEncoder(w)
				if scenario == "unknown field" {
					w.Write([]byte("{\"phase\":\"Running\",\"private\":1}\n"))
					return
				}
				if scenario == "huge event" {
					w.Write([]byte(strings.Repeat("x", 600<<10)))
					return
				}
				if scenario == "invalid UTF8" {
					w.Write([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}', '\n'})
					return
				}
				if scenario != "result before running" {
					encoder.Encode(Event{Phase: Running})
				}
				if scenario == "duplicate phase" {
					encoder.Encode(Event{Phase: Running})
				}
				if scenario == "missing result" {
					return
				}
				result := acceptedRun()
				if scenario == "wrong task" {
					result.Task.Attempt = 2
				}
				encoder.Encode(Event{Result: &result})
				if scenario == "two results" {
					encoder.Encode(Event{Result: &result})
				}
			}))
			defer server.Close()
			client, err := NewRunnerClient(server.URL, strings.Repeat("a", 32), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result, err := client.Execute(context.Background(), RunnerJob{Request: runRequest(), LeaseToken: ownedLease().Token}, func(Status) error { return nil })
			want := error(ErrRunnerProtocol)
			if scenario == "valid" {
				want = nil
			}
			if scenario == "busy" {
				want = ErrRunnerBusy
			}
			if err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
			if want != nil && result.Task != (TaskEnvelope{}) {
				t.Fatal("invalid partial result exposed")
			}
		})
	}
}
func TestRunnerClientDoesNotRedirectOrSwallowCallbackError(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer redirect.Close()
	client, _ := NewRunnerClient(redirect.URL, strings.Repeat("a", 32), nil)
	defer client.Close()
	if client.Ready(context.Background()) != ErrRunnerUnavailable || called {
		t.Fatal("runner credential followed redirect")
	}
	want := errors.New("database CAS failed")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		json.NewEncoder(w).Encode(Event{Phase: Running})
	}))
	defer server.Close()
	direct, _ := NewRunnerClient(server.URL, strings.Repeat("a", 32), nil)
	defer direct.Close()
	if _, err := direct.Execute(context.Background(), RunnerJob{Request: runRequest(), LeaseToken: ownedLease().Token}, func(Status) error { return want }); err != want {
		t.Fatal("callback error reclassified as Runner failure")
	}
}
