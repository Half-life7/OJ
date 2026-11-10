// Package webui serves the embedded, server-rendered account and problem pages.
package webui

import (
	"bytes"
	"crypto/sha256"
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
	entries, err := files.ReadDir("static")
	if err != nil {
		return nil, fmt.Errorf("list embedded assets: %w", err)
	}
	urls := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := files.ReadFile("static/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded asset: %w", err)
		}
		name := "/static/" + entry.Name()
		urls[name] = fmt.Sprintf("%s?v=%x", name, sha256.Sum256(data))
	}
	templates, err := template.New("").Funcs(template.FuncMap{"asset": func(name string) string { return urls[name] }}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse page templates: %w", err)
	}
	assets, err := fs.Sub(files, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded assets: %w", err)
	}
	mux := http.NewServeMux()
	for path, p := range map[string]page{
		"/admin":            {Name: "operations", Title: "管理与监控"},
		"/learning":         {Name: "community", Title: "学习社区"},
		"/rankings":         {Name: "rankings", Title: "全局排行榜"},
		"/contests":         {Name: "contests", Title: "比赛中心"},
		"/contests/{id}":    {Name: "contests", Title: "比赛详情"},
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
			if rawID := r.PathValue("id"); rawID != "" {
				id, err := strconv.ParseUint(rawID, 10, 64)
				if err != nil || id == 0 || strconv.FormatUint(id, 10) != rawID {
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
		case "operations.js", "/operations.js", "community.js", "/community.js", "contests.js", "/contests.js", "app.css", "app.js", "problems.js", "submissions.js", "editor.js", "/app.css", "/app.js", "/problems.js", "/submissions.js", "/editor.js":
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
