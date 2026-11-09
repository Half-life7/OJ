package problem

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"biue/internal/auth"
	"biue/internal/config"
	"biue/internal/server"
	"biue/internal/storage"
	"biue/internal/testcase"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/csrf"
)

type httpOperations struct {
	calls     int
	err       error
	actor     Actor
	input     Input
	options   ListOptions
	dataset   testcase.Dataset
	revision  uint64
	requestID string
}

func (f *httpOperations) List(_ context.Context, a Actor, o ListOptions) (Page, error) {
	f.calls++
	f.actor = a
	f.options = o
	return Page{Items: []Problem{}, Page: o.Page, Limit: o.Limit}, f.err
}
func (f *httpOperations) Get(_ context.Context, a Actor, _ uint64) (Problem, error) {
	f.calls++
	f.actor = a
	return Problem{ID: 1}, f.err
}
func (f *httpOperations) Create(_ context.Context, a Actor, in Input, id string) (Problem, error) {
	f.calls++
	f.actor = a
	f.input = in
	f.requestID = id
	return Problem{ID: 1, Input: in}, f.err
}
func (f *httpOperations) Update(_ context.Context, a Actor, _ uint64, in Input, id string) (Problem, error) {
	return f.Create(context.Background(), a, in, id)
}
func (f *httpOperations) Import(_ context.Context, a Actor, _ uint64, rev uint64, d testcase.Dataset, id string) (DataVersion, error) {
	f.calls++
	f.actor = a
	f.revision = rev
	f.dataset = d
	f.requestID = id
	return DataVersion{Version: 2, Revision: rev + 1, CaseCount: len(d.Cases), TotalScore: d.TotalScore}, f.err
}
func (f *httpOperations) ListDataVersions(_ context.Context, a Actor, _ uint64, page, limit int) (VersionPage, error) {
	f.calls++
	f.actor = a
	return VersionPage{Items: []DataVersion{}, Page: page, Limit: limit}, f.err
}
func (f *httpOperations) GetDataVersion(_ context.Context, a Actor, _, version uint64) (DataVersion, error) {
	f.calls++
	f.actor = a
	return DataVersion{Version: version, Cases: []CaseInfo{{ID: "1", Score: 100, InputBytes: 3, OutputBytes: 4}}}, f.err
}

type httpAuth struct {
	user  auth.User
	err   error
	calls int
}

func (f *httpAuth) CurrentUser(context.Context, string) (auth.User, error) {
	f.calls++
	return f.user, f.err
}

func adminAuth() *httpAuth {
	return &httpAuth{user: auth.User{ID: 9, Status: "active", GlobalRole: "super_admin"}}
}

func httpHandler(t *testing.T, ops *httpOperations, a *httpAuth) (*Handler, *gin.Engine) {
	t.Helper()
	h, err := NewHandler(ops, a, 1)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	h.RegisterRoutes(r.Group(""))
	return h, r
}

func requestTo(h http.Handler, method, path, body, contentType string, login bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if login {
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "session"})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPManagementAuthorizationPrecedesReading(t *testing.T) {
	for _, tc := range []struct {
		name   string
		login  bool
		auth   *httpAuth
		status int
	}{
		{"anonymous", false, adminAuth(), 401},
		{"ordinary", true, &httpAuth{user: auth.User{ID: 2, Status: "active", GlobalRole: "user"}}, 403},
		{"expired", true, &httpAuth{err: auth.ErrCredentials}, 401},
		{"database unavailable", true, &httpAuth{err: auth.ErrUnavailable}, 503},
		{"disabled", true, &httpAuth{user: auth.User{ID: 9, Status: "disabled", GlobalRole: "super_admin"}}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &httpOperations{}
			_, r := httpHandler(t, ops, tc.auth)
			for _, methodPath := range [][2]string{{"GET", "/api/admin/problems"}, {"POST", "/api/admin/problems"}, {"PUT", "/api/admin/problems/1"}, {"POST", "/api/admin/problems/1/data-versions"}, {"GET", "/api/admin/problems/1/data-versions/1"}} {
				req := httptest.NewRequest(methodPath[0], methodPath[1], nil)
				req.Body = panicBody{}
				if tc.login {
					req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "session"})
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != tc.status || ops.calls != 0 {
					t.Fatalf("%v status=%d servicecalls=%d", methodPath, w.Code, ops.calls)
				}
			}
		})
	}
}

type panicBody struct{}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func (panicBody) Read([]byte) (int, error) { panic("body read before authorization") }
func (panicBody) Close() error             { return nil }

func TestHTTPPublicQueriesAndStrictAdminJSON(t *testing.T) {
	ops := &httpOperations{}
	a := adminAuth()
	_, r := httpHandler(t, ops, a)
	w := requestTo(r, "GET", "/api/problems?page=2&limit=10&q=sum&difficulty=easy&tag=math&status=draft", "", "", false)
	if w.Code != 200 || a.calls != 0 || ops.actor.ID != 0 || ops.options.Page != 2 || ops.options.Status != "" || ops.options.Query != "sum" {
		t.Fatalf("public query: %d %+v", w.Code, ops)
	}
	for _, body := range []string{`{"title":"x","unexpected":true}`, `{"title":4}`, `{} {}`, `null`, `[]`, `{"title":"` + string([]byte{0xff}) + `"}`, strings.Repeat(" ", 256<<10) + `{}`} {
		before := ops.calls
		w = requestTo(r, "POST", "/api/admin/problems", body, "application/json", true)
		if w.Code != 400 || ops.calls != before {
			t.Fatalf("malformed body: status=%d", w.Code)
		}
	}
	w = requestTo(r, "POST", "/api/admin/problems", `{"title":"sum","revision":0}`, "application/json", true)
	if w.Code != 201 || ops.actor.ID != 9 || ops.input.Title != "sum" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/api/problems?page=-1", "/api/problems?limit=101", "/api/problems/0", "/api/problems/no", "/api/admin/problems/1/data-versions/0"} {
		if w := requestTo(r, "GET", path, "", "", true); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func archiveBytes(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, entry := range []struct{ name, value string }{{"1.in", "1 2\n"}, {"1.out", "3\n"}} {
		w, err := z.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(entry.value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func uploadRequest(t *testing.T, archive []byte, fields [][2]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if archive != nil {
		part, err := writer.CreateFormFile("archive", "../../untrusted.zip")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(archive)
	}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://oj.test/api/admin/problems/1/data-versions", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "session"})
	return r
}

func TestHTTPImportValidationAndConcurrency(t *testing.T) {
	valid := archiveBytes(t)
	for _, tc := range []struct {
		name    string
		archive []byte
		fields  [][2]string
		status  int
	}{
		{"valid", valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "3"}}, 201},
		{"corrupt", []byte("bad"), [][2]string{{"scores", `{"1":100}`}, {"revision", "3"}}, 400},
		{"missing archive", nil, [][2]string{{"scores", `{"1":100}`}, {"revision", "3"}}, 400},
		{"extra field", valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "3"}, {"unknown", "x"}}, 400},
		{"duplicate scores", valid, [][2]string{{"scores", `{"1":100}`}, {"scores", `{"1":100}`}, {"revision", "3"}}, 400},
		{"duplicate case key", valid, [][2]string{{"scores", `{"1":50,"1":100}`}, {"revision", "3"}}, 400},
		{"null score", valid, [][2]string{{"scores", `{"1":null}`}, {"revision", "3"}}, 400},
		{"string score", valid, [][2]string{{"scores", `{"1":"100"}`}, {"revision", "3"}}, 400},
		{"float score", valid, [][2]string{{"scores", `{"1":1.5}`}, {"revision", "3"}}, 400},
		{"negative score", valid, [][2]string{{"scores", `{"1":-1}`}, {"revision", "3"}}, 400},
		{"missing revision", valid, [][2]string{{"scores", `{"1":100}`}}, 400},
		{"zero revision", valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "0"}}, 400},
		{"extra scores JSON", valid, [][2]string{{"scores", `{"1":100} {}`}, {"revision", "3"}}, 400},
		{"wrong case", valid, [][2]string{{"scores", `{"2":100}`}, {"revision", "3"}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &httpOperations{}
			h, r := httpHandler(t, ops, adminAuth())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, uploadRequest(t, tc.archive, tc.fields))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if tc.status == 201 {
				if ops.calls != 1 || ops.revision != 3 || ops.dataset.TotalScore != 100 || string(ops.dataset.Cases[0].Output) != "3\n" {
					t.Fatalf("import lost data: %+v", ops)
				}
			} else if ops.calls != 0 {
				t.Fatal("invalid import reached service")
			}
			if len(h.imports) != 0 {
				t.Fatal("import slot leaked")
			}
		})
	}
	ops := &httpOperations{}
	h, r := httpHandler(t, ops, adminAuth())
	h.imports <- struct{}{}
	w := httptest.NewRecorder()
	req := uploadRequest(t, valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "1"}})
	req.Body = panicBody{}
	r.ServeHTTP(w, req)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("concurrency: %d", w.Code)
	}
	<-h.imports
	req = uploadRequest(t, valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "1"}})
	req.ContentLength = maxImportRequest + 1
	req.Body = panicBody{}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 413 {
		t.Fatalf("oversized: %d", w.Code)
	}
	// Multipart epilogues count toward the budget, even with no known length.
	req = uploadRequest(t, valid, [][2]string{{"scores", `{"1":100}`}, {"revision", "1"}})
	req.Body = io.NopCloser(io.MultiReader(req.Body, io.LimitReader(zeroReader{}, maxImportRequest)))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 413 || ops.calls != 0 || len(h.imports) != 0 {
		t.Fatalf("chunked oversized epilogue: status=%d calls=%d", w.Code, ops.calls)
	}
}

func TestHTTPErrorEnvelopeAndVersionMetadata(t *testing.T) {
	for _, tc := range []struct {
		err          error
		status, code int
	}{{ErrNotFound, 404, 40401}, {ErrConflict, 409, 40901}, {ErrUnavailable, 503, 50301}, {errors.New("internal secret"), 503, 50301}} {
		ops := &httpOperations{err: tc.err}
		_, r := httpHandler(t, ops, adminAuth())
		w := requestTo(r, "GET", "/api/problems/1", "", "", false)
		var body struct {
			Code int `json:"code"`
			Data any `json:"data"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &body)
		if err != nil || w.Code != tc.status || body.Code != tc.code || body.Data != nil || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("error envelope: %d %s", w.Code, w.Body)
		}
	}
	ops := &httpOperations{}
	_, r := httpHandler(t, ops, adminAuth())
	w := requestTo(r, "GET", "/api/admin/problems/1/data-versions/2", "", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "storage_key") || strings.Contains(w.Body.String(), `"input"`) || strings.Contains(w.Body.String(), `"output"`) {
		t.Fatalf("version metadata leaked: %s", w.Body)
	}
}

func TestImportThroughCSRFMiddlewareAndAuditID(t *testing.T) {
	ops := &httpOperations{}
	h, err := NewHandler(ops, adminAuth(), 1)
	if err != nil {
		t.Fatal(err)
	}
	r := server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: h.RegisterRoutes, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	// A missing header must fail without CSRF's multipart form fallback reading.
	req := uploadRequest(t, archiveBytes(t), [][2]string{{"scores", `{"1":100}`}, {"revision", "1"}})
	req.Body = panicBody{}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 || ops.calls != 0 {
		t.Fatalf("CSRF gate: %d %s", w.Code, w.Body)
	}
	// Fetch a real token using the existing auth route, then import through Gin.
	r = server.New(config.Config{PublicURL: "http://oj.test", CSRFSecret: []byte(strings.Repeat("c", 32))}, server.Dependencies{
		Ready: func(context.Context) storage.Readiness {
			return storage.Readiness{MySQL: true, Redis: true, Schema: true}
		},
		RegisterAPI: func(group *gin.RouterGroup) {
			h.RegisterRoutes(group)
			group.GET("/api/auth/csrf", func(c *gin.Context) {
				respond(c, 200, 0, "success", map[string]string{"csrf_token": csrf.Token(c.Request)})
			})
		}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	csrfResponse := httptest.NewRecorder()
	r.ServeHTTP(csrfResponse, httptest.NewRequest("GET", "http://oj.test/api/auth/csrf", nil))
	var envelope struct {
		Data struct {
			Token string `json:"csrf_token"`
		} `json:"data"`
	}
	if err = json.Unmarshal(csrfResponse.Body.Bytes(), &envelope); err != nil || envelope.Data.Token == "" {
		t.Fatal("missing CSRF token")
	}
	req = uploadRequest(t, archiveBytes(t), [][2]string{{"scores", `{"1":100}`}, {"revision", "1"}})
	req.Header.Set("Origin", "http://oj.test")
	req.Header.Set("X-CSRF-Token", envelope.Data.Token)
	req.Header.Set("X-Request-ID", "forged")
	for _, cookie := range csrfResponse.Result().Cookies() {
		req.AddCookie(cookie)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 || ops.calls != 1 || ops.requestID == "" || ops.requestID == "forged" || ops.requestID != w.Header().Get("X-Request-ID") {
		t.Fatalf("import through middleware: %d %s id=%q", w.Code, w.Body, ops.requestID)
	}
}
