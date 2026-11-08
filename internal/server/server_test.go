package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"biue/internal/config"
	"biue/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
)

func testConfig() config.Config {
	return config.Config{PublicURL: "http://example.com", CSRFSecret: []byte(strings.Repeat("c", 32))}
}

func TestHealthAndDependencyRecovery(t *testing.T) {
	ready := false
	called := false
	h := New(testConfig(), Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: ready, Redis: ready, Schema: ready}
		},
		RegisterAPI: func(r chi.Router) {
			r.Get("/api/test", func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) })
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/health", 200}, {"/ready", 503}, {"/api/test", 503}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	if called {
		t.Fatal("business handler called while unavailable")
	}
	ready = true
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/test", nil))
	if w.Code != 204 || !called {
		t.Fatal("did not recover")
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	h := New(testConfig(), Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: func(r chi.Router) {
			r.Get("/api/auth/csrf", func(w http.ResponseWriter, r *http.Request) {
				write(w, 200, 0, "success", map[string]string{"csrf_token": csrf.Token(r)})
			})
			r.Post("/api/change", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com/api/auth/csrf", nil))
	var body struct {
		Data struct {
			Token string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.Token == "" {
		t.Fatal("missing CSRF token")
	}
	cookies := w.Result().Cookies()
	for _, tc := range []struct {
		name, origin, token string
		status              int
	}{
		{"valid", "http://example.com", body.Data.Token, 204},
		{"missing", "http://example.com", "", 403},
		{"forged", "http://example.com", "fake", 403},
		{"cross-site", "https://attacker.invalid", body.Data.Token, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://example.com/api/change", nil)
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestForwardedIPOnlyFromTrustedPeers(t *testing.T) {
	for _, tc := range []struct{ peer, forwarded, want string }{
		{"198.51.100.7:20", "1.2.3.4", "198.51.100.7:20"},
		{"10.0.0.1:20", "1.2.3.4, 198.51.100.8", "198.51.100.8:0"},
		{"10.0.0.1:20", "198.51.100.8, 10.0.0.2", "198.51.100.8:0"},
		{"10.0.0.1:20", "fake", "10.0.0.1:0"},
	} {
		t.Run(tc.peer+tc.forwarded, func(t *testing.T) {
			h := trustedClientIP([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.RemoteAddr != tc.want {
					t.Fatalf("IP=%s want=%s", r.RemoteAddr, tc.want)
				}
			}))
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			h.ServeHTTP(httptest.NewRecorder(), r)
		})
	}
}

func TestPanicAndLogsDoNotEchoQuerySecrets(t *testing.T) {
	var logs strings.Builder
	h := New(testConfig(), Dependencies{Ready: func(context.Context) storage.Readiness { return storage.Readiness{} },
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)), UI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("password=secret") })})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?password=secret", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") || strings.Contains(logs.String(), "secret") {
		t.Fatal("panic or query leaked")
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("request ID missing")
	}
}

func TestAPIErrorsRemainJSONWithUIFallback(t *testing.T) {
	h := New(testConfig(), Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: func(r chi.Router) {
			r.Get("/api/test", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		},
		UI:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("page")) }),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/missing", 404}, {"PUT", "/api/test", 405},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
