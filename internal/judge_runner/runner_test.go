package judge_runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"biue/internal/judge"
)

const testToken = "d8!L0mA2qC4vE6xG8zJ1nP3rT5wY7bK9"

type fakeBackend struct {
	calls atomic.Int32
	run   func(context.Context, judge.Request, func(judge.Status) error) (judge.Result, error)
}

func (*fakeBackend) Ready(context.Context) error         { return nil }
func (*fakeBackend) Cleanup(context.Context, bool) error { return nil }
func (b *fakeBackend) Judge(ctx context.Context, r judge.Request, phase func(judge.Status) error) (judge.Result, error) {
	b.calls.Add(1)
	if b.run != nil {
		return b.run(ctx, r, phase)
	}
	if err := phase(judge.Running); err != nil {
		return judge.Result{}, err
	}
	return judge.Result{Task: r.Task, Status: judge.Accepted, PassedCases: 1, TotalCases: 1, Output: "3\n"}, nil
}
func testRequest() judge.Request {
	return judge.Request{Task: judge.TaskEnvelope{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 1}, Kind: judge.Run, Language: "python3", SourceCode: "print(3)", TimeLimitMS: 1000, MemoryLimitMB: 128}
}
func fixture(t *testing.T, b *fakeBackend) (*Manager, *Journal) {
	t.Helper()
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	m, err := NewManager(j, b, 2)
	if err != nil {
		t.Fatal(err)
	}
	if m.Initialize(context.Background()) != nil {
		t.Fatal("initialize")
	}
	return m, j
}

func TestAuthenticatedRunnerReplayAndConflict(t *testing.T) {
	b := &fakeBackend{}
	manager, journal := fixture(t, b)
	server := httptest.NewServer(Handler(manager, testToken))
	defer server.Close()
	client, err := judge.NewRunnerClient(server.URL, testToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	job := judge.RunnerJob{Request: testRequest(), LeaseToken: strings.Repeat("a", 32)}
	for i := 0; i < 2; i++ {
		job.LeaseToken = strings.Repeat(string(rune('a'+i)), 32)
		var phases []judge.Status
		result, err := client.Execute(context.Background(), job, func(s judge.Status) error { phases = append(phases, s); return nil })
		if err != nil || result.Status != judge.Accepted || result.Output != "3\n" || len(phases) != 1 || phases[0] != judge.Running {
			t.Fatalf("result=%s phases=%v error=%v", result.Status, phases, err)
		}
	}
	if b.calls.Load() != 1 {
		t.Fatal("duplicate execution")
	}
	// A new Manager reuses the durable journal without invoking the backend.
	replacement, _ := NewManager(journal, b, 2)
	replacement.Initialize(context.Background())
	if _, err := replacement.Judge(context.Background(), testRequest(), strings.Repeat("c", 32), func(judge.Status) error { return nil }); err != nil || b.calls.Load() != 1 {
		t.Fatalf("restart replay error=%v", err)
	}
	changed := testRequest()
	changed.SourceCode = "print('private')"
	if _, err := manager.Judge(context.Background(), changed, strings.Repeat("a", 32), func(judge.Status) error { return nil }); err != ErrConflict {
		t.Fatal("modified request reused task identity")
	}
	for _, auth := range []string{"", testToken, "Bearer wrong"} {
		r, _ := http.NewRequest("GET", server.URL+"/ready", nil)
		r.Header.Set("Authorization", auth)
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatal("unauthenticated readiness")
		}
	}
}

func TestLeaseTakeoverCancelsExistingExecution(t *testing.T) {
	started := make(chan struct{})
	b := &fakeBackend{run: func(ctx context.Context, r judge.Request, phase func(judge.Status) error) (judge.Result, error) {
		phase(judge.Running)
		close(started)
		<-ctx.Done()
		return judge.Result{}, ctx.Err()
	}}
	m, _ := fixture(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first := make(chan judge.Result, 1)
	go func() {
		r, _ := m.Judge(ctx, testRequest(), strings.Repeat("a", 32), func(judge.Status) error { return nil })
		first <- r
	}()
	<-started
	second, err := m.Judge(ctx, testRequest(), strings.Repeat("b", 32), func(judge.Status) error { return nil })
	if err != nil || second.Status != judge.SystemError {
		t.Fatalf("takeover result=%s error=%v", second.Status, err)
	}
	if r := <-first; r.Status != judge.SystemError || b.calls.Load() != 1 {
		t.Fatal("takeover started another sandbox for same attempt")
	}
}

func TestInterruptedJournalNeverRerunsAttempt(t *testing.T) {
	b := &fakeBackend{}
	m, j := fixture(t, b)
	r := testRequest()
	// Persist the same digest that a crash immediately after admission leaves.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Judge(ctx, r, strings.Repeat("a", 32), func(judge.Status) error { return context.Canceled }); err != nil {
		t.Fatal(err)
	}
	record, err := j.read(r.Task)
	if err != nil {
		t.Fatal(err)
	}
	record.State = "started"
	record.Result = nil
	record.Phases = nil
	if j.write(r.Task, record) != nil {
		t.Fatal("journal write")
	}
	result, err := m.Judge(context.Background(), r, strings.Repeat("b", 32), func(judge.Status) error { return nil })
	if err != nil || result.Status != judge.SystemError || b.calls.Load() != 1 {
		t.Fatalf("crashed attempt rerun: %v", err)
	}
	content, err := os.ReadFile(j.root.Name() + string(os.PathSeparator) + taskKey(r.Task) + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), r.SourceCode) || strings.Contains(string(content), "lease_token") {
		t.Fatal("journal retained source/lease")
	}
}

func TestHTTPRejectsBadJobsBeforeExecution(t *testing.T) {
	b := &fakeBackend{}
	m, _ := fixture(t, b)
	h := Handler(m, testToken)
	job := judge.RunnerJob{Request: testRequest(), LeaseToken: strings.Repeat("a", 32)}
	valid, _ := json.Marshal(job)
	for _, body := range []string{`{}`, string(valid) + `{}`, `{"request":{},"lease_token":"bad","unknown":1}`} {
		r := httptest.NewRequest("POST", "/v1/judge", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("status=%d", w.Code)
		}
	}
	if b.calls.Load() != 0 {
		t.Fatal("bad job executed")
	}
}

func TestTLSAndCapacityReadiness(t *testing.T) {
	m, _ := fixture(t, &fakeBackend{})
	server := httptest.NewTLSServer(Handler(m, testToken))
	defer server.Close()
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	client, err := judge.NewRunnerClient(server.URL, testToken, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Ready(context.Background()) != nil {
		t.Fatal("trusted TLS runner unavailable")
	}
	m.slots <- struct{}{}
	m.slots <- struct{}{}
	if client.Ready(context.Background()) == nil {
		t.Fatal("capacity overload admitted work")
	}
	if client.BackendReady(context.Background()) != nil {
		t.Fatal("normal busy state made backend unhealthy")
	}
	<-m.slots
	<-m.slots
}
