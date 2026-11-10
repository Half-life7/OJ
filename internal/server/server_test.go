package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"biue/internal/config"
	"biue/internal/storage"
	"biue/webui"

	"github.com/gin-gonic/gin"
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
		RegisterAPI: func(r *gin.RouterGroup) {
			r.GET("/api/test", func(c *gin.Context) { called = true; c.Status(204) })
			r.GET("/api/auth/csrf", func(c *gin.Context) {
				write(c.Writer, 200, 0, "success", map[string]string{"csrf_token": csrf.Token(c.Request)})
			})
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		path   string
		status int
	}{{"/health", 200}, {"/ready", 503}, {"/api/test", 503}, {"/api/auth/csrf", 200}} {
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
	for _, publicURL := range []string{"http://example.com", "https://example.com"} {
		t.Run(publicURL, func(t *testing.T) {
			testCSRFAndOrigin(t, publicURL)
		})
	}
}

func testCSRFAndOrigin(t *testing.T, publicURL string) {
	t.Helper()
	cfg := testConfig()
	cfg.PublicURL = publicURL
	called := 0
	h := New(cfg, Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: func(r *gin.RouterGroup) {
			r.GET("/api/auth/csrf", func(c *gin.Context) {
				write(c.Writer, 200, 0, "success", map[string]string{"csrf_token": csrf.Token(c.Request)})
			})
			r.POST("/api/change", func(c *gin.Context) { called++; c.Status(204) })
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", publicURL+"/api/auth/csrf", nil))
	var body struct {
		Data struct {
			Token string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.Token == "" || w.Code != 200 {
		t.Fatal("missing CSRF token")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Secure != strings.HasPrefix(publicURL, "https:") {
		t.Fatalf("unexpected CSRF cookie attributes: %v", cookies)
	}
	for _, tc := range []struct {
		name, origin, token string
		withCookie          bool
		status              int
	}{
		{"valid", publicURL, body.Data.Token, true, 204},
		{"missing-token", publicURL, "", true, 403},
		{"missing-cookie", publicURL, body.Data.Token, false, 403},
		{"forged", publicURL, "fake", true, 403},
		{"cross-site", "https://attacker.invalid", body.Data.Token, true, 403},
		{"origin-with-path", publicURL + "/page", body.Data.Token, true, 403},
		{"origin-with-query", publicURL + "?token=secret", body.Data.Token, true, 403},
		{"null-origin", "null", body.Data.Token, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", publicURL+"/api/change", nil)
			if tc.withCookie {
				for _, cookie := range cookies {
					r.AddCookie(cookie)
				}
			}
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, body=%s", w.Code, w.Body.String())
			}
			if tc.name == "cross-site" && (!strings.Contains(w.Body.String(), "请求来源不受信任") || !strings.Contains(w.Body.String(), "request_id")) {
				t.Fatalf("origin error lacks safe guidance or request ID: %s", w.Body.String())
			}
		})
	}
	if called != 1 {
		t.Fatalf("unsafe requests reached handler: called %d times", called)
	}
}

func TestProblemUploadGetsASeparateBoundedContext(t *testing.T) {
	var deadline time.Time
	r := gin.New()
	r.Use(observe(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	r.POST("/api/admin/problems/:id/data-versions", func(c *gin.Context) {
		deadline, _ = c.Request.Context().Deadline()
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/admin/problems/7/data-versions", nil))
	remaining := time.Until(deadline)
	if w.Code != http.StatusNoContent || !deadline.After(time.Now().Add(110*time.Second)) || remaining > 2*time.Minute {
		t.Fatalf("upload deadline=%v remaining=%v status=%d", deadline, remaining, w.Code)
	}
}

func TestForwardedIPOnlyFromTrustedPeers(t *testing.T) {
	for _, tc := range []struct {
		name, peer, forwarded, want string
		trustProxy                  bool
	}{
		{"untrusted-peer", "198.51.100.7:20", "1.2.3.4", "198.51.100.7:20", true},
		{"untrusted-hop", "10.0.0.1:20", "1.2.3.4, 198.51.100.8", "198.51.100.8:0", true},
		{"trusted-chain", "10.0.0.1:20", "198.51.100.8, 10.0.0.2", "198.51.100.8:0", true},
		{"invalid-chain", "10.0.0.1:20", "fake", "10.0.0.1:0", true},
		{"real-ip-only", "10.0.0.1:20", "", "10.0.0.1:0", true},
		{"no-trusted-proxies", "10.0.0.1:20", "1.2.3.4", "10.0.0.1:20", false},
		{"ipv6-peer", "[2001:db8::1]:20", "1.2.3.4", "[2001:db8::1]:20", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			if tc.trustProxy {
				cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
			}
			called := false
			h := New(cfg, Dependencies{
				Ready: func(context.Context) storage.Readiness {
					return storage.Readiness{MySQL: true, Redis: true, Schema: true}
				},
				RegisterAPI: func(r *gin.RouterGroup) {
					r.GET("/api/ip", func(c *gin.Context) {
						called = true
						if c.Request.RemoteAddr != tc.want {
							t.Errorf("RemoteAddr=%s want=%s", c.Request.RemoteAddr, tc.want)
						}
						wantIP, _, _ := net.SplitHostPort(tc.want)
						if c.ClientIP() != wantIP {
							t.Errorf("Gin ClientIP=%s want=%s", c.ClientIP(), wantIP)
						}
						c.Status(http.StatusNoContent)
					})
				},
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			r := httptest.NewRequest("GET", "/api/ip", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			r.Header.Set("X-Real-IP", "203.0.113.99")
			h.ServeHTTP(httptest.NewRecorder(), r)
			if !called {
				t.Fatal("IP handler was not called")
			}
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
		RegisterAPI: func(r *gin.RouterGroup) {
			r.GET("/api/test", func(c *gin.Context) { c.Status(204) })
		},
		UI:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("page")) }),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		method, path string
		status, code int
	}{
		{"GET", "/api/missing", 404, 40401}, {"PUT", "/api/test", 405, 40501},
		{"GET", "/api", 404, 40401}, {"GET", "/api/test/", 404, 40401},
		{"GET", "/api%2Ftest", 404, 40401},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("error response contract changed: headers=%v body=%s", w.Header(), w.Body.String())
		}
	}
}

func TestEmbeddedPagesThroughGinFallback(t *testing.T) {
	ui, err := webui.Handler()
	if err != nil {
		t.Fatal(err)
	}
	h := New(testConfig(), Dependencies{
		Ready: func(context.Context) storage.Readiness { return storage.Readiness{} },
		UI:    ui, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, tc := range []struct {
		method, path, contentType string
		status                    int
	}{
		{"GET", "/", "text/html", 200},
		{"GET", "/register", "text/html", 200},
		{"HEAD", "/me", "text/html", 200},
		{"GET", "/static/app.js", "text/javascript", 200},
		{"HEAD", "/static/app.css", "text/css", 200},
		{"POST", "/login", "text/plain", 405},
		{"GET", "/unknown", "text/plain", 404},
		{"GET", "/static/", "text/plain", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("status=%d type=%q body=%s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
			}
			if tc.method == "HEAD" && w.Body.Len() != 0 {
				t.Fatalf("HEAD returned %d body bytes", w.Body.Len())
			}
		})
	}
}

func TestHealthHEADOnHTTPServer(t *testing.T) {
	h := New(testConfig(), Dependencies{
		Ready:  func(context.Context) storage.Readiness { return storage.Readiness{} },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := httptest.NewServer(h)
	defer s.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/health", 200}, {"/ready", 503}} {
		r, err := s.Client().Head(s.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(r.Body)
		r.Body.Close()
		if readErr != nil || r.StatusCode != tc.status || len(body) != 0 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("HEAD %s: status=%d body=%q readErr=%v", tc.path, r.StatusCode, body, readErr)
		}
	}
}

func TestRequestIDAndTimeoutContext(t *testing.T) {
	var logs strings.Builder
	var requestContext context.Context
	var seenID string
	h := New(testConfig(), Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: func(r *gin.RouterGroup) {
			r.GET("/api/context", func(c *gin.Context) {
				requestContext = c.Request.Context()
				seenID = RequestID(requestContext)
				if c.Request.Header.Get("X-Request-ID") != seenID {
					t.Error("request ID header and context differ")
				}
				deadline, ok := requestContext.Deadline()
				if !ok || time.Until(deadline) > 15*time.Second || time.Until(deadline) < 13*time.Second {
					t.Error("HTTP request timeout was not propagated")
				}
				c.Status(http.StatusNoContent)
			})
		},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	r := httptest.NewRequest("GET", "/api/context?password=query-secret", nil)
	r.Header.Set("X-Request-ID", "client-controlled-id")
	r.Header.Set("Authorization", "Bearer header-secret")
	r.AddCookie(&http.Cookie{Name: "biue_session", Value: "cookie-secret"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	id := w.Header().Get("X-Request-ID")
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 16 || id != seenID {
		t.Fatalf("server-generated request ID missing: response=%q context=%q", id, seenID)
	}
	if requestContext == nil || requestContext.Err() != context.Canceled {
		t.Fatal("request context was not canceled after the response")
	}
	for _, secret := range []string{"query-secret", "header-secret", "cookie-secret", "client-controlled-id"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("request log leaked %q", secret)
		}
	}
	var record struct {
		ID     string `json:"request_id"`
		Path   string `json:"path"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal([]byte(logs.String()), &record); err != nil || record.ID != id || record.Path != "/api/context" || record.Status != http.StatusNoContent {
		t.Fatalf("request log lost correlation or status: %s", logs.String())
	}
}

func TestEarlierDeadlineAndCancellationArePreserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deadline := time.Now().Add(time.Second)
	deadlineContext, deadlineCancel := context.WithDeadline(context.Background(), deadline)
	defer deadlineCancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"canceled", ctx}, {"earlier-deadline", deadlineContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := New(testConfig(), Dependencies{
				Ready: func(context.Context) storage.Readiness {
					return storage.Readiness{MySQL: true, Redis: true, Schema: true}
				},
				RegisterAPI: func(r *gin.RouterGroup) {
					r.GET("/api/context", func(c *gin.Context) {
						called = true
						if tc.name == "canceled" {
							if c.Request.Context().Err() != context.Canceled {
								t.Error("parent cancellation was lost")
							}
						} else if got, ok := c.Request.Context().Deadline(); !ok || !got.Equal(deadline) {
							t.Error("parent deadline was extended")
						}
						c.Status(http.StatusNoContent)
					})
				},
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			r := httptest.NewRequest("GET", "/api/context", nil).WithContext(tc.ctx)
			h.ServeHTTP(httptest.NewRecorder(), r)
			if !called {
				t.Fatal("context handler was not called")
			}
		})
	}
}
