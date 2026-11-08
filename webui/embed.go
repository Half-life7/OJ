// Package webui serves the embedded, server-rendered account pages.
package webui

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html static/*
var files embed.FS

type page struct {
	Name  string
	Title string
}

// Handler returns the MPA pages and their local assets. API routes belong to
// the application router; unknown page paths remain 404.
func Handler() (http.Handler, error) {
	templates, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse page templates: %w", err)
	}
	assets, err := fs.Sub(files, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded assets: %w", err)
	}
	mux := http.NewServeMux()
	for path, p := range map[string]page{
		"/{$}":      {"home", "首页"},
		"/register": {"register", "创建账号"},
		"/login":    {"login", "登录"},
		"/me":       {"me", "个人中心"},
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			var output bytes.Buffer
			if err := templates.ExecuteTemplate(&output, "page.html", p); err != nil {
				http.Error(w, "页面暂时无法加载", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodHead {
				_, _ = w.Write(output.Bytes())
			}
		})
	}
	assetServer := http.FileServer(http.FS(assets))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "app.css" && r.URL.Path != "app.js" && r.URL.Path != "/app.css" && r.URL.Path != "/app.js" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		assetServer.ServeHTTP(w, r)
	})))
	return mux, nil
}
