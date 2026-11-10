// Package webui serves the embedded, server-rendered account and problem pages.
package webui

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"

	"biue/internal/httpx"
)

//go:embed templates/*.html static/*
var files embed.FS

type page struct {
	Name       string
	Title      string
	StyleNonce string
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
		"/learning": {Name:"community",Title:"学习社区"},
		"/rankings": {Name:"rankings",Title:"全局排行榜"},
		"/contests":         {Name: "contests", Title: "比赛中心"},
		"/admin/identities": {Name: "identities", Title: "身份管理"},
		"/{$}":              {Name: "home", Title: "首页"},
		"/register":         {Name: "register", Title: "创建账号"},
		"/login":            {Name: "login", Title: "登录"},
		"/me":               {Name: "me", Title: "个人中心"},
		"/problems":         {Name: "problems", Title: "题库"},
		"/problems/{id}":    {Name: "problem", Title: "题目详情"},
		"/admin/problems":   {Name: "admin-problems", Title: "题目管理"},
		"/submissions":      {Name: "submissions", Title: "我的提交"},
		"/submissions/{id}": {Name: "submission", Title: "提交详情"},
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if p.Name == "problem" || p.Name == "submission" {
				id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
				if err != nil || id == 0 || strconv.FormatUint(id, 10) != r.PathValue("id") {
					http.NotFound(w, r)
					return
				}
			}
			var output bytes.Buffer
			requestPage := p
			requestPage.StyleNonce = httpx.StyleNonce(r.Context())
			if err := templates.ExecuteTemplate(&output, "page.html", requestPage); err != nil {
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
		switch r.URL.Path {
		case "community.js", "/community.js", "contests.js", "/contests.js", "app.css", "app.js", "problems.js", "submissions.js", "editor.js", "/app.css", "/app.js", "/problems.js", "/submissions.js", "/editor.js":
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		assetServer.ServeHTTP(w, r)
	})))
	return mux, nil
}
