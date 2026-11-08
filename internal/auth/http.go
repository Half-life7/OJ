package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

func (s *Service) RegisterRoutes(r chi.Router) {
	r.Get("/api/auth/csrf", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, 0, "success", map[string]string{"csrf_token": csrf.Token(r)})
	})
	r.Post("/api/auth/register", s.registerHTTP)
	r.Post("/api/auth/login", s.loginHTTP)
	r.Post("/api/auth/logout", s.logoutHTTP)
	r.Post("/api/auth/password", s.passwordHTTP)
	r.Get("/api/users/me", s.meHTTP)
}

func writeJSON(w http.ResponseWriter, status, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{code, message, data})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		writeJSON(w, 400, 40001, "请求参数不合法", nil)
	case errors.Is(err, ErrCredentials):
		writeJSON(w, 401, 40101, "账号、密码或会话无效", nil)
	case errors.Is(err, ErrConflict):
		writeJSON(w, 409, 40901, "账号或身份信息不可用", nil)
	case errors.Is(err, ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, 42901, "请求过于频繁，请稍后重试", nil)
	default:
		writeJSON(w, 503, 50301, "服务暂时不可用，请稍后重试", nil)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err = decoder.Decode(&raw); err != nil {
		return ErrInvalid
	}
	// encoding/json otherwise replaces malformed UTF-8 with U+FFFD. Reject it
	// before decoding credentials so hashing never silently changes the input.
	if !utf8.Valid(raw) {
		return ErrInvalid
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalid
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err = objectDecoder.Decode(target); err != nil {
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

func (s *Service) registerHTTP(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.limit(r, "register", body.Username); err != nil {
		writeError(w, err)
		return
	}
	user, err := s.Register(r.Context(), body.Username, body.Password, body.Confirmation, body.StudentNo, middleware.GetReqID(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, 0, "success", user)
}

func (s *Service) loginHTTP(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := s.limit(r, "login", body.Username); err != nil {
		writeError(w, err)
		return
	}
	token, user, err := s.Login(r.Context(), body.Username, body.Password, middleware.GetReqID(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	s.setCookie(w, token)
	writeJSON(w, 200, 0, "success", user)
}

func (s *Service) logoutHTTP(w http.ResponseWriter, r *http.Request) {
	// A bodyless logout is valid. When a body is supplied (including an
	// unknown-length/chunked body), enforce the same bounded JSON contract.
	if r.Body != nil && r.Body != http.NoBody {
		var body struct{}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.Logout(r.Context(), rawCookie(r), middleware.GetReqID(r.Context())); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, 0, "success", map[string]any{})
}

func (s *Service) passwordHTTP(w http.ResponseWriter, r *http.Request) {
	var body passwordRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	// Password verification also uses bcrypt; rate-limit it independently from login.
	if err := s.limit(r, "password", hashJTI(rawCookie(r))); err != nil {
		writeError(w, err)
		return
	}
	if err := s.ChangePassword(r.Context(), rawCookie(r), body.Current, body.Password, body.Confirmation, middleware.GetReqID(r.Context())); err != nil {
		writeError(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, 0, "success", map[string]any{})
}

func (s *Service) meHTTP(w http.ResponseWriter, r *http.Request) {
	user, err := s.CurrentUser(r.Context(), rawCookie(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, 0, "success", user)
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
