package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"biue/internal/httpx"
)

func TestPagesAndEmbeddedAssets(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path       string
		want       string
		typePrefix string
	}{
		{"/", "Biue OJ", "text/html"},
		{"/register", "register-form", "text/html"},
		{"/login", "login-form", "text/html"},
		{"/me", "password-form", "text/html"},
		{"/learning", "learning-form", "text/html"},
		{"/rankings", "board-rows", "text/html"},
		{"/static/community.js", "/api/community", "text/javascript"},
		{"/contests", "contest-submit-form", "text/html"},
		{"/admin/identities", "identity-link-form", "text/html"},
		{"/static/contests.js", "/api/contests", "text/javascript"},
		{"/problems", "problem-filter-form", "text/html"},
		{"/problems/7", "problem-description", "text/html"},
		{"/admin/problems", "problem-upload-form", "text/html"},
		{"/submissions", "submission-filter-form", "text/html"},
		{"/submissions/11", "submission-source", "text/html"},
		{"/static/app.css", "--green", "text/css"},
		{"/static/app.js", "/api/auth/register", "text/javascript"},
		{"/static/problems.js", "/api/admin/problems", "text/javascript"},
		{"/static/submissions.js", "/api/runs", "text/javascript"},
		{"/static/editor.js", "BiueEditor", "text/javascript"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("GET %s: status %d, missing required page content %q", test.path, response.Code, test.want)
			}
			if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, test.typePrefix) {
				t.Errorf("Content-Type = %q, want %q", contentType, test.typePrefix)
			}
		})
	}
}

func TestPageBoundaries(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/unknown", "/api/users/me", "/static/", "/static/missing.js", "/templates/page.html", "/problems/0", "/problems/01", "/problems/not-a-number", "/problems/18446744073709551616", "/problems/1/more", "/problems/1%2F2", "/admin/problems/7", "/submissions/0", "/submissions/01", "/submissions/no", "/submissions/1/more", "/submissions/1%2F2"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /login = %d, want 405", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/me", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Errorf("HEAD /me: status %d, body %d bytes", response.Code, response.Body.Len())
	}
}

func TestSubmissionShellAndEditorNonce(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/problems/7", "/submissions", "/submissions/11"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(httpx.WithStyleNonce(r.Context(), "request-specific-nonce"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, `name="csp-nonce" content="request-specific-nonce"`) || !strings.Contains(body, `/static/submissions.js`) {
			t.Fatalf("submission shell/nonce missing at %s", path)
		}
		if path == "/problems/7" && (!strings.Contains(body, `/static/editor.js`) || !strings.Contains(body, `id="submission-fields" disabled`)) {
			t.Fatal("editor asset or disabled submission gate missing")
		}
		head := httptest.NewRecorder()
		handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Fatalf("HEAD %s failed", path)
		}
	}
	// A reused template handler must not retain a prior request's nonce.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/problems/7", nil))
	if strings.Contains(w.Body.String(), "request-specific-nonce") {
		t.Fatal("nonce leaked across requests")
	}
}

func TestProblemPagesKeepDataInAPIAndSupportHEAD(t *testing.T) {
	handler, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/problems", "/problems/7", "/admin/problems"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			body := response.Body.String()
			if !strings.Contains(body, `/static/problems.js`) || !strings.Contains(body, `id="problem-state"`) {
				t.Fatal("problem loading shell or script missing")
			}
			if path == "/admin/problems" && (!strings.Contains(body, `id="problem-admin" hidden`) || !strings.Contains(body, `id="problem-upload-fields" disabled`)) {
				t.Fatal("admin shell must hide controls until permission is verified")
			}
			head := httptest.NewRecorder()
			handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("HEAD: status=%d body=%d headers=%v", head.Code, head.Body.Len(), head.Header())
			}
			wrongMethod := httptest.NewRecorder()
			handler.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, path, nil))
			if wrongMethod.Code != http.StatusMethodNotAllowed {
				t.Fatalf("POST returned %d", wrongMethod.Code)
			}
		})
	}
}
