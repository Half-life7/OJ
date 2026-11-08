package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		{"/static/app.css", "--green", "text/css"},
		{"/static/app.js", "/api/auth/register", "text/javascript"},
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
	for _, path := range []string{"/unknown", "/api/users/me", "/static/", "/static/missing.js", "/templates/page.html"} {
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
