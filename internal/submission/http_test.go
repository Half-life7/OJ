package submission

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"biue/internal/auth"
	"biue/internal/config"
	"biue/internal/httpx"
	"biue/internal/judge"
	"biue/internal/server"
	"biue/internal/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type httpStore struct {
	calls   int
	kind    string
	input   CreateInput
	actor   Actor
	options ListOptions
	err     error
	record  Submission
}

func (f *httpStore) Create(_ context.Context, a Actor, kind string, in CreateInput, _ string) (Submission, error) {
	f.calls++
	f.actor = a
	f.kind = kind
	f.input = in
	return Submission{Summary: Summary{ID: 1, Kind: kind, Status: judge.Pending}}, f.err
}
func (f *httpStore) List(_ context.Context, a Actor, o ListOptions) (Page, error) {
	f.calls++
	f.actor = a
	f.options = o
	return Page{Items: []Summary{}, Page: o.Page, Limit: o.Limit}, f.err
}
func (f *httpStore) Get(_ context.Context, a Actor, _ uint64) (Submission, error) {
	f.calls++
	f.actor = a
	return f.record, f.err
}

type httpAuth struct {
	user auth.User
	err  error
}

func (f httpAuth) CurrentUser(context.Context, string) (auth.User, error) { return f.user, f.err }

func handlerFixture(t *testing.T, account httpAuth) (*Handler, *gin.Engine, *httpStore, *miniredis.Miniredis) {
	t.Helper()
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := &httpStore{}
	h, err := NewHandler(store, account, client)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	h.RegisterRoutes(r.Group(""))
	return h, r, store, rdb
}

func permanent() httpAuth {
	return httpAuth{user: auth.User{ID: 7, AccountType: "permanent", Status: "active", GlobalRole: "user"}}
}
func call(h http.Handler, method, path, body string, login bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "198.51.100.9:20"
	r.Header.Set("Content-Type", "application/json")
	if login {
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "session"})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPAuthenticationAndPermanentCreation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account httpAuth
		login   bool
		status  int
	}{
		{"anonymous", permanent(), false, 401}, {"expired", httpAuth{err: auth.ErrCredentials}, true, 401}, {"disabled", httpAuth{user: auth.User{ID: 7, Status: "disabled"}}, true, 401}, {"temporary", httpAuth{user: auth.User{ID: 7, Status: "active", AccountType: "contest"}}, true, 403}, {"database", httpAuth{err: auth.ErrUnavailable}, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r, store, _ := handlerFixture(t, tc.account)
			for _, path := range []string{"/api/submissions", "/api/runs"} {
				w := call(r, "POST", path, `{"problem_id":1,"language":"go","source_code":"code"}`, tc.login)
				if w.Code != tc.status || store.calls != 0 {
					t.Fatalf("status=%d calls=%d body=%s", w.Code, store.calls, w.Body)
				}
			}
		})
	}
}

func TestHTTPCreateAndSeparateRateLimits(t *testing.T) {
	_, r, store, rdb := handlerFixture(t, permanent())
	body := `{"problem_id":1,"language":"go","source_code":"package main"}`
	for i := 0; i < 10; i++ {
		if w := call(r, "POST", "/api/submissions", body, true); w.Code != 201 {
			t.Fatalf("submit %d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := call(r, "POST", "/api/submissions", body, true); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("submit rate: %d", w.Code)
	}
	for i := 0; i < 5; i++ {
		if w := call(r, "POST", "/api/runs", body, true); w.Code != 201 {
			t.Fatalf("run %d: %d", i, w.Code)
		}
	}
	if w := call(r, "POST", "/api/runs", body, true); w.Code != 429 {
		t.Fatalf("run rate: %d", w.Code)
	}
	if store.calls != 15 || store.kind != "Run" || store.actor.ID != 7 || store.actor.AccountType != "permanent" {
		t.Fatalf("creation identity/rate: %+v", store)
	}
	for _, key := range rdb.Keys() {
		if strings.Contains(key, "198.51.100.9") {
			t.Fatal("IP leaked in Redis key")
		}
	}
	rdb.FastForward(time.Minute)
	if w := call(r, "POST", "/api/runs", body, true); w.Code != 201 {
		t.Fatalf("rate did not reset: %d", w.Code)
	}
	rdb.Close()
	if w := call(r, "POST", "/api/submissions", body, true); w.Code != 503 {
		t.Fatalf("Redis failure silently allowed: %d", w.Code)
	}
}

func TestHTTPStrictCreateFieldsAndCustomInput(t *testing.T) {
	_, r, store, _ := handlerFixture(t, permanent())
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"contest_id":9}`, `{"user_id":2}`, `{"stdin":"hidden"}`, `{"source_code":"` + string([]byte{0xff}) + `"}`, strings.Repeat(" ", 256<<10) + `{}`} {
		w := call(r, "POST", "/api/submissions", body, true)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("body validation: %d calls=%d", w.Code, store.calls)
		}
	}
	for _, body := range []string{`{"problem_id":1,"language":"python3","source_code":"pass"}`, `{"problem_id":1,"language":"python3","source_code":"pass","stdin":""}`, `{"problem_id":1,"language":"python3","source_code":"pass","stdin":"line\n\u0000"}`} {
		w := call(r, "POST", "/api/runs", body, true)
		if w.Code != 201 {
			t.Fatalf("run input: %d %s", w.Code, w.Body)
		}
	}
	if store.input.Stdin == nil || *store.input.Stdin != "line\n\x00" {
		t.Fatalf("custom input changed: %+v", store.input.Stdin)
	}
}

func TestHTTPHistoryAndHiddenOutputBoundary(t *testing.T) {
	_, r, store, _ := handlerFixture(t, permanent())
	w := call(r, "GET", "/api/submissions?page=2&limit=10&problem_id=4&kind=Submit", "", true)
	if w.Code != 200 || store.options.ProblemID != 4 || store.options.Kind != "Submit" || store.options.Page != 2 || store.actor.ID != 7 {
		t.Fatalf("history: %d %+v", w.Code, store.options)
	}
	for _, path := range []string{"/api/submissions?kind=Run", "/api/submissions?kind=Other", "/api/submissions?limit=101", "/api/submissions?problem_id=0", "/api/submissions/no"} {
		if w := call(r, "GET", path, "", true); w.Code != 400 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	stdin := "hidden input"
	store.record = Submission{Summary: Summary{ID: 1, Kind: "Submit", Status: judge.RuntimeError}, Stdin: &stdin, Output: "hidden output", ErrorMessage: "hidden runtime diagnostic", SourceCode: "owner source"}
	w = call(r, "GET", "/api/submissions/1", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden") || !strings.Contains(w.Body.String(), "owner source") {
		t.Fatalf("hidden data boundary: %s", w.Body)
	}
	store.record.Kind = "Run"
	w = call(r, "GET", "/api/submissions/1", "", true)
	if !strings.Contains(w.Body.String(), "hidden output") {
		t.Fatal("Run output unexpectedly removed")
	}
	for _, tc := range []struct {
		err          error
		status, code int
	}{{ErrNotFound, 404, 40401}, {ErrNoData, 409, 40902}, {ErrQueueFull, 503, 50302}, {ErrUnavailable, 503, 50301}} {
		store.err = tc.err
		w = call(r, "GET", "/api/submissions/1", "", true)
		var result struct {
			Code int `json:"code"`
			Data any `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != tc.status || result.Code != tc.code || result.Data != nil || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("error: %d %s", w.Code, w.Body)
		}
	}
}

func TestLanguagesRemainAvailableWithoutDependencies(t *testing.T) {
	h, _, _, _ := handlerFixture(t, permanent())
	r := server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{Ready: func(context.Context) storage.Readiness { return storage.Readiness{} }, RegisterAPI: h.RegisterRoutes, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := call(r, "GET", "/api/languages", "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Compile") || !strings.Contains(w.Body.String(), "cpp17") {
		t.Fatalf("language registry: %d %s", w.Code, w.Body)
	}
}

func TestStyleNonceRemainsPerRequestAndRestrictsStyles(t *testing.T) {
	var previous string
	r := server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{Ready: func(context.Context) storage.Readiness {
		return storage.Readiness{MySQL: true, Redis: true, Schema: true}
	}, RegisterAPI: func(group *gin.RouterGroup) {
		group.GET("/api/nonce", func(c *gin.Context) { c.String(200, httpx.StyleNonce(c.Request.Context())) })
	}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for range 2 {
		w := call(r, "GET", "/api/nonce", "", false)
		nonce := w.Body.String()
		policy := w.Header().Get("Content-Security-Policy")
		if nonce == "" || nonce == previous || !strings.Contains(policy, "'nonce-"+nonce+"'") || strings.Contains(policy, "unsafe-inline") || nonce == w.Header().Get("X-Request-ID") {
			t.Fatalf("unsafe or missing style nonce: %q %s", nonce, policy)
		}
		previous = nonce
	}
}
