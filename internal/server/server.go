package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"biue/internal/config"
	"biue/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/csrf"
)

type Dependencies struct {
	Ready       func(context.Context) storage.Readiness
	RegisterAPI func(chi.Router)
	UI          http.Handler
	Logger      *slog.Logger
}

func New(cfg config.Config, deps Dependencies) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(observe(logger))
	r.Use(trustedClientIP(cfg.TrustedProxyCIDRs))
	health := func(w http.ResponseWriter, r *http.Request) {
		write(w, http.StatusOK, 0, "success", map[string]string{"status": "alive"})
	}
	r.Get("/health", health)
	r.Head("/health", health)
	readiness := func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		result := deps.Ready(ctx)
		if !result.Ready() {
			write(w, 503, 50301, "service not ready", result)
			return
		}
		write(w, 200, 0, "success", result)
	}
	r.Get("/ready", readiness)
	r.Head("/ready", readiness)
	r.Group(func(api chi.Router) {
		api.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/auth/csrf" {
					ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
					ready := deps.Ready(ctx).Ready()
					cancel()
					if !ready {
						write(w, 503, 50301, "service temporarily unavailable", nil)
						return
					}
				}
				next.ServeHTTP(w, r)
			})
		})
		api.Use(originCheck(cfg.PublicURL))
		secure := strings.HasPrefix(cfg.PublicURL, "https://")
		api.Use(func(next http.Handler) http.Handler {
			protected := csrf.Protect(cfg.CSRFSecret,
				csrf.Secure(secure), csrf.HttpOnly(true), csrf.Path("/"),
				csrf.SameSite(csrf.SameSiteLaxMode),
				csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					write(w, 403, 40301, "invalid CSRF token", nil)
				})),
			)(next)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !secure {
					r = csrf.PlaintextHTTPRequest(r)
				}
				protected.ServeHTTP(w, r)
			})
		})
		if deps.RegisterAPI != nil {
			deps.RegisterAPI(api)
		}
	})
	// A UI fallback rather than a wildcard mount preserves API method matching.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			write(w, 404, 40401, "API route not found", nil)
			return
		}
		if deps.UI != nil {
			deps.UI.ServeHTTP(w, r)
			return
		}
		write(w, 404, 40401, "route not found", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { write(w, 405, 40501, "method not allowed", nil) })
	return r
}

func write(w http.ResponseWriter, status, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{code, message, data})
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *response) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *response) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func observe(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				write(w, 500, 50001, "internal server error", nil)
				return
			}
			requestID := hex.EncodeToString(id[:])
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
			rw := &response{ResponseWriter: w}
			start := time.Now()
			defer func() {
				if recover() != nil {
					logger.Error("request panic", "request_id", requestID)
					if rw.status == 0 {
						write(rw, 500, 50001, "internal server error", nil)
					}
				}
				status := rw.status
				if status == 0 {
					status = http.StatusOK
				}
				logger.Info("http request", "request_id", requestID, "method", r.Method,
					"path", r.URL.Path, "status", status, "duration_ms", time.Since(start).Milliseconds())
			}()
			// Use only server-generated IDs in audit logs; never trust inbound IDs.
			r.Header.Set("X-Request-ID", requestID)
			requestContext := context.WithValue(r.Context(), middleware.RequestIDKey, requestID)
			ctx, cancel := context.WithTimeout(requestContext, 15*time.Second)
			defer cancel()
			next.ServeHTTP(rw, r.WithContext(ctx))
		})
	}
}

func originCheck(publicURL string) func(http.Handler) http.Handler {
	expected, _ := url.Parse(publicURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if raw := r.Header.Get("Origin"); raw != "" {
					u, err := url.Parse(raw)
					if err != nil || u.User != nil || u.Scheme != expected.Scheme || !strings.EqualFold(u.Host, expected.Host) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
						write(w, 403, 40301, "untrusted request origin", nil)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trustedClientIP(prefixes []netip.Prefix) func(http.Handler) http.Handler {
	trusted := func(ip netip.Addr) bool {
		for _, p := range prefixes {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			peer, parseErr := netip.ParseAddr(host)
			if err == nil && parseErr == nil && trusted(peer) {
				chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
				// Walk from the actual peer toward the client, ignoring only trusted hops.
				for i := len(chain) - 1; i >= 0; i-- {
					candidate, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
					if e != nil {
						break
					}
					peer = candidate.Unmap()
					if !trusted(peer) {
						break
					}
				}
				r.RemoteAddr = net.JoinHostPort(peer.String(), "0")
			}
			next.ServeHTTP(w, r)
		})
	}
}
