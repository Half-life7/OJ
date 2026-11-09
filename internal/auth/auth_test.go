package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"biue/internal/config"
	"biue/internal/server"
	"biue/internal/storage"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct-password"

type fixture struct {
	s     *Service
	mock  sqlmock.Sqlmock
	redis *miniredis.Miniredis
}

func setup(t *testing.T) fixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond})
	s, err := New(sqlx.NewDb(db, "sqlmock"), client, Config{JWTSecret: []byte(strings.Repeat("s", 32)), Issuer: "biue-test", Audience: "biue-browser", SessionTTL: 12 * time.Hour, BcryptCost: bcrypt.MinCost, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = client.Close()
		_ = db.Close()
	})
	return fixture{s: s, mock: mock, redis: mr}
}

func userRows(t *testing.T, version uint64, status string) *sqlmock.Rows {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return sqlmock.NewRows([]string{"id", "username", "password_hash", "account_type", "global_role", "status", "token_version", "display_name", "student_no", "student_no_status"}).AddRow(7, "alice", string(hash), "permanent", "user", status, version, "Alice", nil, "none")
}

func expectAudit(mock sqlmock.Sqlmock, action string) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO audit_logs")).WithArgs(uint64(7), action, uint64(7), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
}

func expectCreate(mock sqlmock.Sqlmock, student *string) {
	mock.ExpectBegin()
	state := "none"
	var value any
	if student != nil {
		state = "pending"
		value = *student
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO persons")).WithArgs("alice", value, state, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO users")).WithArgs("alice", sqlmock.AnyArg(), "user", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO person_accounts")).WithArgs(int64(7), int64(4), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	if student != nil {
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO student_no_history")).WithArgs(int64(4), *student, int64(7), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	}
	expectAudit(mock, "account.register")
	mock.ExpectCommit()
}

func issueToken(t *testing.T, s *Service) (string, *claims) {
	t.Helper()
	now := s.now().UTC()
	c := &claims{Version: 1, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.cfg.Issuer, Audience: jwt.ClaimStrings{s.cfg.Audience}, Subject: "7", ID: "test-session-id", IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	return raw, c
}

func expectAuthenticated(mock sqlmock.Sqlmock, t *testing.T, c *claims, version uint64, status string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+accountColumns+accountJoins+" JOIN auth_sessions")).WithArgs(c.Subject, hashJTI(c.ID), sqlmock.AnyArg()).WillReturnRows(userRows(t, version, status))
}

func TestRegistrationIdentityAndConflictRollback(t *testing.T) {
	for _, student := range []string{"", " 001234 "} {
		t.Run("student="+student, func(t *testing.T) {
			f := setup(t)
			var normalized *string
			if student != "" {
				v := strings.TrimSpace(student)
				normalized = &v
			}
			expectCreate(f.mock, normalized)
			u, err := f.s.Register(context.Background(), " ALICE ", testPassword, testPassword, student, "req-1")
			if err != nil {
				t.Fatal(err)
			}
			if u.ID != 7 || u.Username != "alice" || u.GlobalRole != "user" || u.AccountType != "permanent" {
				t.Fatalf("unexpected user: %+v", u)
			}
			if student == "" && (u.StudentNo != nil || u.StudentNoStatus != "none") {
				t.Fatal("empty student number must stay nullable")
			}
			if student != "" && (u.StudentNo == nil || *u.StudentNo != "001234" || u.StudentNoStatus != "pending") {
				t.Fatal("student claim should be pending and retain leading zeroes")
			}
		})
	}
	t.Run("duplicate student", func(t *testing.T) {
		f := setup(t)
		f.mock.ExpectBegin()
		f.mock.ExpectExec("INSERT INTO persons").WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate identity"})
		f.mock.ExpectRollback()
		_, err := f.s.Register(context.Background(), "alice", testPassword, testPassword, "001234", "req")
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate username rolls back person", func(t *testing.T) {
		f := setup(t)
		f.mock.ExpectBegin()
		f.mock.ExpectExec("INSERT INTO persons").WillReturnResult(sqlmock.NewResult(4, 1))
		f.mock.ExpectExec("INSERT INTO users").WillReturnError(&mysql.MySQLError{Number: 1062})
		f.mock.ExpectRollback()
		_, err := f.s.Register(context.Background(), "alice", testPassword, testPassword, "", "req")
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("audit failure rolls back account", func(t *testing.T) {
		f := setup(t)
		f.mock.ExpectBegin()
		f.mock.ExpectExec("INSERT INTO persons").WillReturnResult(sqlmock.NewResult(4, 1))
		f.mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(7, 1))
		f.mock.ExpectExec("INSERT INTO person_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
		f.mock.ExpectExec("INSERT INTO audit_logs").WillReturnError(sql.ErrConnDone)
		f.mock.ExpectRollback()
		_, err := f.s.Register(context.Background(), "alice", testPassword, testPassword, "", "req")
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestInputBoundaries(t *testing.T) {
	f := setup(t)
	for _, name := range []string{"ab", "admin", "ROOT", "bad-name", "你好你好", strings.Repeat("a", 33)} {
		if _, err := f.s.Register(context.Background(), name, testPassword, testPassword, "", ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q accepted: %v", name, err)
		}
	}
	for _, password := range []string{"short", strings.Repeat("x", 73), strings.Repeat("密", 25)} {
		if _, err := f.s.Register(context.Background(), "alice", password, password, "", ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("password byte length %d accepted: %v", len(password), err)
		}
	}
	if !validPassword(strings.Repeat("密", 24)) {
		t.Fatal("72-byte UTF-8 password rejected")
	}
	if _, err := f.s.Register(context.Background(), "alice", testPassword, "different", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("password confirmation ignored")
	}
	if _, err := normalizeStudent("123\n456"); !errors.Is(err, ErrInvalid) {
		t.Fatal("control character accepted")
	}
}

func TestJWTValidationAndCurrentAccountState(t *testing.T) {
	f := setup(t)
	raw, c := issueToken(t, f.s)
	if _, err := f.s.parseToken(raw); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*claims){
		func(c *claims) { c.Issuer = "attacker" }, func(c *claims) { c.Audience = jwt.ClaimStrings{"other"} },
		func(c *claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour)) }, func(c *claims) { c.ExpiresAt = nil },
		func(c *claims) { c.ID = "" }, func(c *claims) { c.Version = 0 }, func(c *claims) { c.Subject = "0" },
	} {
		_, changed := issueToken(t, f.s)
		mutate(changed)
		bad, err := jwt.NewWithClaims(jwt.SigningMethodHS256, changed).SignedString(f.s.cfg.JWTSecret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.parseToken(bad); !errors.Is(err, ErrCredentials) {
			t.Errorf("claims accepted: %+v", changed)
		}
	}
	wrongAlg, _ := jwt.NewWithClaims(jwt.SigningMethodHS384, c).SignedString(f.s.cfg.JWTSecret)
	if _, err := f.s.parseToken(wrongAlg); !errors.Is(err, ErrCredentials) {
		t.Fatal("wrong algorithm accepted")
	}
	tampered := raw[:len(raw)/2] + "x" + raw[len(raw)/2+1:]
	if _, err := f.s.parseToken(tampered); !errors.Is(err, ErrCredentials) {
		t.Fatal("tampered JWT accepted")
	}
	expectAuthenticated(f.mock, t, c, 2, "active")
	if _, err := f.s.CurrentUser(context.Background(), raw); !errors.Is(err, ErrCredentials) {
		t.Fatal("old token version accepted")
	}
	expectAuthenticated(f.mock, t, c, 1, "disabled")
	if _, err := f.s.CurrentUser(context.Background(), raw); !errors.Is(err, ErrCredentials) {
		t.Fatal("disabled account accepted")
	}
	f.mock.ExpectQuery("SELECT .*JOIN auth_sessions").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, err := f.s.CurrentUser(context.Background(), raw); !errors.Is(err, ErrCredentials) {
		t.Fatal("revoked or absent session accepted")
	}
}

func TestLoginConcurrencyVersionCheck(t *testing.T) {
	f := setup(t)
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT " + accountColumns + accountJoins + " WHERE u.username = ?")).WithArgs("alice").WillReturnRows(userRows(t, 1, "active"))
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT token_version, status FROM users").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"token_version", "status"}).AddRow(2, "active"))
	f.mock.ExpectRollback()
	if _, _, err := f.s.Login(context.Background(), "alice", testPassword, "req"); !errors.Is(err, ErrCredentials) {
		t.Fatalf("concurrent password change accepted old version: %v", err)
	}
}

func TestLoginWrongPasswordUnknownAndDisabled(t *testing.T) {
	for _, scenario := range []string{"wrong password", "unknown account", "disabled account"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			query := f.mock.ExpectQuery("SELECT .*WHERE u.username").WithArgs("alice")
			password := testPassword
			switch scenario {
			case "unknown account":
				query.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			case "disabled account":
				query.WillReturnRows(userRows(t, 1, "disabled"))
			default:
				query.WillReturnRows(userRows(t, 1, "active"))
				password = "wrong-password"
			}
			if _, _, err := f.s.Login(context.Background(), "alice", password, "req"); !errors.Is(err, ErrCredentials) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestPasswordChangeAtomicAndOldTokenInvalid(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("failure=", fail), func(t *testing.T) {
			f := setup(t)
			raw, c := issueToken(t, f.s)
			expectAuthenticated(f.mock, t, c, 1, "active")
			f.mock.ExpectBegin()
			f.mock.ExpectQuery("SELECT token_version, status FROM users").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"token_version", "status"}).AddRow(1, "active"))
			f.mock.ExpectQuery("SELECT id FROM auth_sessions").WithArgs(uint64(7), hashJTI(c.ID), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
			if fail {
				f.mock.ExpectExec("UPDATE users SET password_hash").WillReturnError(sql.ErrConnDone)
				f.mock.ExpectRollback()
			} else {
				f.mock.ExpectExec("UPDATE users SET password_hash").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
				f.mock.ExpectExec("UPDATE auth_sessions SET revoked_at").WithArgs(sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
				expectAudit(f.mock, "account.password_change")
				f.mock.ExpectCommit()
			}
			err := f.s.ChangePassword(context.Background(), raw, testPassword, "new-password", "new-password", "req")
			if fail {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expectAuthenticated(f.mock, t, c, 2, "active")
			if _, err = f.s.CurrentUser(context.Background(), raw); !errors.Is(err, ErrCredentials) {
				t.Fatal("pre-change JWT accepted")
			}
		})
	}
}

func TestRateLimitBothDimensionsAndRedisFailure(t *testing.T) {
	f := setup(t)
	for i := 0; i < 10; i++ {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:42", i+1)
		if err := f.s.limit(r, "login", "Alice"); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "192.0.2.100:42"
	if err := f.s.limit(r, "login", " ALICE "); !errors.Is(err, ErrRateLimited) {
		t.Fatal("account limit did not span source IPs")
	}
	f.redis.FastForward(16 * time.Minute)
	if err := f.s.limit(r, "login", "alice"); err != nil {
		t.Fatal("limit did not expire", err)
	}
	for i := 0; i < 30; i++ {
		r.RemoteAddr = "192.0.2.200:42"
		if err := f.s.limit(r, "login", fmt.Sprint("user", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.limit(r, "login", "fresh-user"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("IP limit did not span account IDs")
	}
	f.redis.Close()
	if err := f.s.limit(r, "login", "alice"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("redis failure bypassed limits", err)
	}
}

func TestAdminInitNeverOverwritesOrPromotes(t *testing.T) {
	for _, role := range []string{"super_admin", "user"} {
		t.Run(role, func(t *testing.T) {
			f := setup(t)
			f.mock.ExpectQuery("SELECT global_role, status FROM users WHERE username").WithArgs("admin").WillReturnRows(sqlmock.NewRows([]string{"global_role", "status"}).AddRow(role, "active"))
			created, err := f.s.InitAdmin(context.Background(), "admin", testPassword)
			if created {
				t.Fatal("existing account recreated")
			}
			if role == "super_admin" && err != nil {
				t.Fatal(err)
			}
			if role == "user" && !errors.Is(err, ErrConflict) {
				t.Fatal("ordinary account silently promoted")
			}
		})
	}
}

func TestHTTPAuthenticationLifecycleAndCSRF(t *testing.T) {
	f := setup(t)
	handler := server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: f.s.RegisterRoutes, Logger: f.s.logger,
	})
	cookies := map[string]*http.Cookie{}
	csrfToken := ""
	call := func(method, path, body string, withToken bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://oj.test"+path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.1:4567"
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if withToken {
			r.Header.Set("X-CSRF-Token", csrfToken)
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		for _, cookie := range w.Result().Cookies() {
			if cookie.MaxAge < 0 {
				delete(cookies, cookie.Name)
			} else {
				cookies[cookie.Name] = cookie
			}
		}
		return w
	}
	w := call("GET", "/api/auth/csrf", "", false, "")
	var envelope struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	csrfToken = envelope.Data.CSRF
	if csrfToken == "" {
		t.Fatal("missing CSRF token")
	}
	if w = call("POST", "/api/auth/register", `{"username":"alice"}`, false, "http://oj.test"); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	if w = call("POST", "/api/auth/register", `{"username":"alice"}`, true, "http://evil.test"); w.Code != 403 {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w = call("POST", "/api/auth/register", `{"username":"alice","global_role":"super_admin"}`, true, "http://oj.test"); w.Code != 400 {
		t.Fatalf("role injection: %d %s", w.Code, w.Body)
	}
	expectCreate(f.mock, nil)
	w = call("POST", "/api/auth/register", `{"username":"alice","password":"correct-password","password_confirmation":"correct-password"}`, true, "http://oj.test")
	if w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "password_hash") || cookies[CookieName] != nil {
		t.Fatal("registration leaked credentials or logged in automatically")
	}
	f.mock.ExpectQuery("SELECT .*WHERE u.username").WithArgs("alice").WillReturnRows(userRows(t, 1, "active"))
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT token_version, status FROM users").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"token_version", "status"}).AddRow(1, "active"))
	f.mock.ExpectExec("INSERT INTO auth_sessions").WithArgs(sqlmock.AnyArg(), uint64(7), uint64(1), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(9, 1))
	expectAudit(f.mock, "account.login")
	f.mock.ExpectCommit()
	w = call("POST", "/api/auth/login", `{"username":"alice","password":"correct-password"}`, true, "http://oj.test")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookie := cookies[CookieName]
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge != 43200 {
		t.Fatalf("unsafe auth cookie: %+v", cookie)
	}
	raw := cookie.Value
	c, err := f.s.parseToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	expectAuthenticated(f.mock, t, c, 1, "active")
	w = call("GET", "/api/users/me", "", false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"alice"`) {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT id FROM users WHERE id").WithArgs("7").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	f.mock.ExpectExec("UPDATE auth_sessions SET revoked_at").WithArgs(sqlmock.AnyArg(), hashJTI(c.ID), "7").WillReturnResult(sqlmock.NewResult(0, 1))
	expectAudit(f.mock, "account.logout")
	f.mock.ExpectCommit()
	w = call("POST", "/api/auth/logout", `{}`, true, "http://oj.test")
	if w.Code != 200 || cookies[CookieName] != nil {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	// Replay the old signed JWT after durable session revocation.
	cookies[CookieName] = &http.Cookie{Name: CookieName, Value: raw}
	f.mock.ExpectQuery("SELECT .*JOIN auth_sessions").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if w = call("GET", "/api/users/me", "", false, ""); w.Code != 401 {
		t.Fatalf("revoked token replay: %d", w.Code)
	}
	delete(cookies, CookieName)
	if w = call("POST", "/api/auth/logout", `{}`, true, "http://oj.test"); w.Code != 200 {
		t.Fatal("logout not idempotent")
	}
}

// Focus binding tests on the registered Gin handlers; server integration tests
// above and below exercise the CSRF/readiness middleware as well.
func authHTTPHandler(s *Service) http.Handler {
	r := gin.New()
	s.RegisterRoutes(r.Group(""))
	return r
}

func TestHTTPMalformedInputAndCookieSecure(t *testing.T) {
	f := setup(t)
	handler := authHTTPHandler(f.s)
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"null", `null`, "application/json", 400},
		{"array", `[]`, "application/json", 400},
		{"JSON string", `"value"`, "application/json", 400},
		{"empty document", ``, "application/json", 400},
		{"empty object", `{}`, "application/json", 401},
		{"two documents", `{"username":"a"} {}`, "application/json", 400},
		{"unknown field", `{"global_role":"super_admin"}`, "application/json", 400},
		{"wrong type", `{"username":7}`, "application/json", 400},
		{"wrong content type", `{}`, "text/plain", 400},
		{"JSON charset", `{}`, "application/json; charset=utf-8", 401},
		{"at size limit", `{}` + strings.Repeat(" ", (16<<10)-2), "application/json", 401},
		{"oversized leading whitespace", strings.Repeat(" ", 16<<10) + `{}`, "application/json", 400},
		{"oversized trailing whitespace", `{}` + strings.Repeat(" ", (16<<10)-1), "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			var envelope struct {
				Code int `json:"code"`
				Data any `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Code != tc.status*100+1 || envelope.Data != nil {
				t.Fatalf("binding changed error envelope: %s (%v)", w.Body, err)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("binding errors must not be cached")
			}
		})
	}
	f.s.cfg.CookieSecure = true
	w := httptest.NewRecorder()
	f.s.setCookie(w, "value")
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly {
		t.Fatal("HTTPS cookie flags missing")
	}
}

func TestHTTPRejectsMalformedUTF8BeforeCredentialDecoding(t *testing.T) {
	f := setup(t)
	// No SQL expectations: malformed raw UTF-8 must be rejected before the
	// JSON decoder can replace it and turn it into a different password.
	body := "{\"username\":\"alice\",\"password\":\"password-" + string([]byte{0xff}) + "\"}"
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authHTTPHandler(f.s).ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed UTF-8 returned %d: %s", w.Code, w.Body)
	}
}

func TestHTTPPasswordChangeClearsCookieAndInvalidatesOldJWT(t *testing.T) {
	f := setup(t)
	raw, c := issueToken(t, f.s)
	handler := server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: f.s.RegisterRoutes, Logger: f.s.logger,
	})
	csrfResponse := httptest.NewRecorder()
	handler.ServeHTTP(csrfResponse, httptest.NewRequest("GET", "http://oj.test/api/auth/csrf", nil))
	var envelope struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(csrfResponse.Body.Bytes(), &envelope); err != nil || envelope.Data.CSRF == "" {
		t.Fatal("missing CSRF token", err)
	}

	expectAuthenticated(f.mock, t, c, 1, "active")
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT token_version, status FROM users").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"token_version", "status"}).AddRow(1, "active"))
	f.mock.ExpectQuery("SELECT id FROM auth_sessions").WithArgs(uint64(7), hashJTI(c.ID), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	f.mock.ExpectExec("UPDATE users SET password_hash").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectExec("UPDATE auth_sessions SET revoked_at").WithArgs(sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
	expectAudit(f.mock, "account.password_change")
	f.mock.ExpectCommit()

	r := httptest.NewRequest("POST", "http://oj.test/api/auth/password", strings.NewReader(`{"current_password":"correct-password","password":"new-password","password_confirmation":"new-password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://oj.test")
	r.Header.Set("X-CSRF-Token", envelope.Data.CSRF)
	for _, cookie := range csrfResponse.Result().Cookies() {
		r.AddCookie(cookie)
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: raw})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("password change: %d %s", w.Code, w.Body)
	}
	cleared := false
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == CookieName && cookie.MaxAge < 0 && cookie.Value == "" && cookie.HttpOnly {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("password change did not clear the authentication Cookie")
	}

	expectAuthenticated(f.mock, t, c, 2, "active")
	replay := httptest.NewRequest("GET", "http://oj.test/api/users/me", nil)
	replay.AddCookie(&http.Cookie{Name: CookieName, Value: raw})
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old JWT replay returned %d: %s", replayResponse.Code, replayResponse.Body)
	}
}

func TestLogoutInputContractWithoutStorageCalls(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		noBody      bool
		contentType string
		chunked     bool
		status      int
	}{
		{name: "bodyless", noBody: true, status: 200},
		{name: "empty JSON object", body: `{}`, contentType: "application/json", status: 200},
		{name: "chunked JSON object", body: `{}`, contentType: "application/json", chunked: true, status: 200},
		{name: "JSON object at size limit", body: `{}` + strings.Repeat(" ", (16<<10)-2), contentType: "application/json", status: 200},
		{name: "null", body: `null`, contentType: "application/json", status: 400},
		{name: "array", body: `[]`, contentType: "application/json", status: 400},
		{name: "unknown field", body: `{"unexpected":true}`, contentType: "application/json", status: 400},
		{name: "two documents", body: `{} {}`, contentType: "application/json", status: 400},
		{name: "wrong content type", body: `{}`, contentType: "text/plain", status: 400},
		{name: "oversized", body: strings.Repeat(" ", (16<<10)+1) + `{}`, contentType: "application/json", status: 400},
		{name: "chunked oversized", body: strings.Repeat(" ", (16<<10)+1) + `{}`, contentType: "application/json", chunked: true, status: 400},
		{name: "oversized trailing whitespace", body: `{}` + strings.Repeat(" ", (16<<10)-1), contentType: "application/json", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			var body io.Reader
			if !tc.noBody {
				body = strings.NewReader(tc.body)
			}
			r := httptest.NewRequest("POST", "/api/auth/logout", body)
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			if tc.chunked {
				r.ContentLength = -1
				r.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			// No SQL expectations: malformed/anonymous logout must never touch storage.
			authHTTPHandler(f.s).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
}
