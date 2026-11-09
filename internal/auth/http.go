package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"biue/internal/httpx"
	"biue/internal/server"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/csrf"
)

type registerRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Confirmation string `json:"password_confirmation"`
	StudentNo    string `json:"student_no"`
}
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type passwordRequest struct {
	Current      string `json:"current_password"`
	Password     string `json:"password"`
	Confirmation string `json:"password_confirmation"`
}

func (s *Service) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/api/auth/csrf", func(c *gin.Context) {
		writeJSON(c, http.StatusOK, 0, "success", map[string]string{"csrf_token": csrf.Token(c.Request)})
	})
	r.POST("/api/auth/register", s.registerHTTP)
	r.POST("/api/auth/login", s.loginHTTP)
	r.POST("/api/auth/logout", s.logoutHTTP)
	r.POST("/api/auth/password", s.passwordHTTP)
	r.GET("/api/users/me", s.meHTTP)
}

func writeJSON(c *gin.Context, status, code int, message string, data any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{code, message, data})
}

func writeError(c *gin.Context, err error) {
	c.Abort()
	switch {
	case errors.Is(err, ErrInvalid):
		writeJSON(c, 400, 40001, "请求参数不合法", nil)
	case errors.Is(err, ErrCredentials):
		writeJSON(c, 401, 40101, "账号、密码或会话无效", nil)
	case errors.Is(err, ErrConflict):
		writeJSON(c, 409, 40901, "账号或身份信息不可用", nil)
	case errors.Is(err, ErrRateLimited):
		c.Header("Retry-After", "60")
		writeJSON(c, 429, 42901, "请求过于频繁，请稍后重试", nil)
	default:
		writeJSON(c, 503, 50301, "服务暂时不可用，请稍后重试", nil)
	}
}

// Keep the auth contract stricter than Gin's default JSON binding without
// changing package-level binding options shared by every router.
type strictJSONBinding struct{}

func (strictJSONBinding) Name() string { return "strict-json" }

func (strictJSONBinding) Bind(r *http.Request, target any) error {
	if err := (httpx.JSONBinding{MaxBytes: 16 << 10}).Bind(r, target); err != nil {
		return ErrInvalid
	}
	return nil
}

func rawCookie(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Service) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.cfg.SessionTTL / time.Second), Expires: s.now().Add(s.cfg.SessionTTL).UTC()})
}

func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

func (s *Service) registerHTTP(c *gin.Context) {
	r := c.Request
	var body registerRequest
	if err := c.ShouldBindWith(&body, strictJSONBinding{}); err != nil {
		writeError(c, err)
		return
	}
	if err := s.limit(r, "register", body.Username); err != nil {
		writeError(c, err)
		return
	}
	user, err := s.Register(r.Context(), body.Username, body.Password, body.Confirmation, body.StudentNo, server.RequestID(r.Context()))
	if err != nil {
		writeError(c, err)
		return
	}
	writeJSON(c, 201, 0, "success", user)
}

func (s *Service) loginHTTP(c *gin.Context) {
	r := c.Request
	var body loginRequest
	if err := c.ShouldBindWith(&body, strictJSONBinding{}); err != nil {
		writeError(c, err)
		return
	}
	if err := s.limit(r, "login", body.Username); err != nil {
		writeError(c, err)
		return
	}
	token, user, err := s.Login(r.Context(), body.Username, body.Password, server.RequestID(r.Context()))
	if err != nil {
		writeError(c, err)
		return
	}
	s.setCookie(c.Writer, token)
	writeJSON(c, 200, 0, "success", user)
}

func (s *Service) logoutHTTP(c *gin.Context) {
	r := c.Request
	// A bodyless logout is valid. When a body is supplied (including an
	// unknown-length/chunked body), enforce the same bounded JSON contract.
	if r.Body != nil && r.Body != http.NoBody {
		var body struct{}
		if err := c.ShouldBindWith(&body, strictJSONBinding{}); err != nil {
			writeError(c, err)
			return
		}
	}
	if err := s.Logout(r.Context(), rawCookie(r), server.RequestID(r.Context())); err != nil {
		writeError(c, err)
		return
	}
	s.clearCookie(c.Writer)
	writeJSON(c, 200, 0, "success", map[string]any{})
}

func (s *Service) passwordHTTP(c *gin.Context) {
	r := c.Request
	var body passwordRequest
	if err := c.ShouldBindWith(&body, strictJSONBinding{}); err != nil {
		writeError(c, err)
		return
	}
	// Password verification also uses bcrypt; rate-limit it independently from login.
	if err := s.limit(r, "password", hashJTI(rawCookie(r))); err != nil {
		writeError(c, err)
		return
	}
	if err := s.ChangePassword(r.Context(), rawCookie(r), body.Current, body.Password, body.Confirmation, server.RequestID(r.Context())); err != nil {
		writeError(c, err)
		return
	}
	s.clearCookie(c.Writer)
	writeJSON(c, 200, 0, "success", map[string]any{})
}

func (s *Service) meHTTP(c *gin.Context) {
	r := c.Request
	user, err := s.CurrentUser(r.Context(), rawCookie(r))
	if err != nil {
		writeError(c, err)
		return
	}
	writeJSON(c, 200, 0, "success", user)
}

// Both counters are advanced atomically. Hashing keeps usernames and IPs out of Redis key names.
const rateScript = `
local a = redis.call('INCR', KEYS[1])
if a == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
local b = redis.call('INCR', KEYS[2])
if b == 1 then redis.call('EXPIRE', KEYS[2], ARGV[1]) end
if a > tonumber(ARGV[2]) or b > tonumber(ARGV[3]) then return 0 end
return 1
`

func (s *Service) limit(r *http.Request, operation, username string) error {
	if s.redis == nil {
		return ErrUnavailable
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ip = strings.Trim(ip, "[]")
	window, ipMax, userMax := 900, 30, 10
	if operation == "register" {
		window, ipMax, userMax = 3600, 10, 5
	}
	keyHash := func(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
	keys := []string{"oj:auth:rate:" + operation + ":ip:" + keyHash(ip), "oj:auth:rate:" + operation + ":account:" + keyHash(strings.ToLower(strings.TrimSpace(username)))}
	allowed, err := s.redis.Eval(r.Context(), rateScript, keys, window, ipMax, userMax).Int()
	if err != nil {
		return s.internal("rate_limit", err)
	}
	if allowed != 1 {
		return ErrRateLimited
	}
	return nil
}
