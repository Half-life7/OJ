package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"biue/internal/httpx"
	"biue/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/csrf"
)

type Dependencies struct {
	Ready       func(context.Context) storage.Readiness
	RegisterAPI func(*gin.RouterGroup)
	UI          http.Handler
	Logger      *slog.Logger
}

func New(cfg config.Config, deps Dependencies) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	r := gin.New()
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.HandleMethodNotAllowed = true
	r.UseRawPath = true
	r.UnescapePathValues = false
	// Normalize trusted proxy chains once below; Gin must not reinterpret headers.
	_ = r.SetTrustedProxies(nil)
	r.ForwardedByClientIP = false
	r.RemoteIPHeaders = nil
	r.Use(observe(logger))
	r.Use(trustedClientIP(cfg.TrustedProxyCIDRs))
	health := func(c *gin.Context) {
		write(c.Writer, http.StatusOK, 0, "success", map[string]string{"status": "alive"})
	}
	r.GET("/health", health)
	r.HEAD("/health", health)
	readiness := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		result := deps.Ready(ctx)
		if !result.Ready() {
			write(c.Writer, 503, 50301, "service not ready", result)
			return
		}
		write(c.Writer, 200, 0, "success", result)
	}
	r.GET("/ready", readiness)
	r.HEAD("/ready", readiness)
	api := r.Group("")
	api.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") && c.Request.URL.Path != "/api/auth/csrf" && c.Request.URL.Path != "/api/languages" {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
			ready := deps.Ready(ctx).Ready()
			cancel()
			if !ready {
				write(c.Writer, 503, 50301, "service temporarily unavailable", nil)
				c.Abort()
				return
			}
		}
		c.Next()
	})
	api.Use(originCheck(cfg.PublicURL), csrfProtection(cfg))
	if deps.RegisterAPI != nil {
		deps.RegisterAPI(api)
	}
	// A UI fallback rather than a wildcard mount preserves API method matching.
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || c.Request.URL.Path == "/api" {
			write(c.Writer, 404, 40401, "API route not found", nil)
			return
		}
		if deps.UI != nil {
			// Gin presets 404 before NoRoute; the UI controls its own status.
			c.Status(http.StatusOK)
			deps.UI.ServeHTTP(c.Writer, c.Request)
			return
		}
		write(c.Writer, 404, 40401, "route not found", nil)
	})
	r.NoMethod(func(c *gin.Context) { write(c.Writer, 405, 40501, "method not allowed", nil) })
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

type requestIDKey struct{}

// RequestID returns the server-generated ID used by HTTP and audit logs.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func observe(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		w := c.Writer
		var id [32]byte
		if _, err := rand.Read(id[:]); err != nil {
			write(w, 500, 50001, "internal server error", nil)
			c.Abort()
			return
		}
		requestID := hex.EncodeToString(id[:16])
		styleNonce := base64.RawStdEncoding.EncodeToString(id[16:])
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+styleNonce+"'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		start := time.Now()
		defer func() {
			if recover() != nil {
				c.Abort()
				logger.Error("request panic", "request_id", requestID)
				if !w.Written() {
					write(w, 500, 50001, "internal server error", nil)
				}
			}
			status := w.Status()
			logger.Info("http request", "request_id", requestID, "method", r.Method,
				"path", r.URL.Path, "status", status, "duration_ms", time.Since(start).Milliseconds())
		}()
		// Use only server-generated IDs in audit logs; never trust inbound IDs.
		r.Header.Set("X-Request-ID", requestID)
		requestContext := context.WithValue(r.Context(), requestIDKey{}, requestID)
		requestContext = httpx.WithStyleNonce(requestContext, styleNonce)
		ctx, cancel := context.WithTimeout(requestContext, 15*time.Second)
		defer cancel()
		c.Request = r.WithContext(ctx)
		c.Next()
	}
}

func csrfProtection(cfg config.Config) gin.HandlerFunc {
	secure := strings.HasPrefix(cfg.PublicURL, "https://")
	protect := csrf.Protect(cfg.CSRFSecret,
		csrf.Secure(secure), csrf.HttpOnly(true), csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
		csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			write(w, 403, 40301, "invalid CSRF token", nil)
		})),
	)
	return func(c *gin.Context) {
		// APIs require a header token. Reject before gorilla/csrf can fall back
		// to parsing a large multipart body ahead of authentication/size limits.
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions && c.Request.Header.Get("X-CSRF-Token") == "" {
			write(c.Writer, 403, 40301, "invalid CSRF token", nil)
			c.Abort()
			return
		}
		passed := false
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			passed = true
			c.Request = r
			c.Next()
		})
		request := c.Request
		if !secure {
			request = csrf.PlaintextHTTPRequest(request)
		}
		protect(next).ServeHTTP(c.Writer, request)
		if !passed {
			c.Abort()
		}
	}
}

func originCheck(publicURL string) gin.HandlerFunc {
	expected, _ := url.Parse(publicURL)
	return func(c *gin.Context) {
		r := c.Request
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if raw := r.Header.Get("Origin"); raw != "" {
				u, err := url.Parse(raw)
				if err != nil || u.User != nil || u.Scheme != expected.Scheme || !strings.EqualFold(u.Host, expected.Host) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
					write(c.Writer, 403, 40301, "untrusted request origin", nil)
					c.Abort()
					return
				}
			}
		}
		c.Next()
	}
}

func trustedClientIP(prefixes []netip.Prefix) gin.HandlerFunc {
	trusted := func(ip netip.Addr) bool {
		for _, p := range prefixes {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	return func(c *gin.Context) {
		r := c.Request
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
		c.Next()
	}
}
