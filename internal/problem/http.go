package problem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"biue/internal/auth"
	"biue/internal/httpx"
	"biue/internal/markdownsafe"
	"biue/internal/server"
	"biue/internal/testcase"

	"github.com/gin-gonic/gin"
)

type Operations interface {
	List(context.Context, Actor, ListOptions) (Page, error)
	Get(context.Context, Actor, uint64) (Problem, error)
	Create(context.Context, Actor, Input, string) (Problem, error)
	Update(context.Context, Actor, uint64, Input, string) (Problem, error)
	ImportPackage(context.Context, Actor, ProblemPackage, string) (Problem, error)
	Import(context.Context, Actor, uint64, uint64, testcase.Dataset, string, string) (DataVersion, error)
	ListDataVersions(context.Context, Actor, uint64, int, int) (VersionPage, error)
	GetDataVersion(context.Context, Actor, uint64, uint64) (DataVersion, error)
}

type Authenticator interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type Handler struct {
	service Operations
	auth    Authenticator
	imports chan struct{}
	samples string
}

func NewHandler(service Operations, authenticator Authenticator, concurrency int) (*Handler, error) {
	if service == nil || authenticator == nil || concurrency < 1 || concurrency > 8 {
		return nil, errors.New("invalid problem HTTP dependencies")
	}
	return &Handler{service: service, auth: authenticator, imports: make(chan struct{}, concurrency)}, nil
}

func (h *Handler) SetSampleDirectory(directory string) {
	h.samples = directory
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/api/problems", h.list(false))
	r.GET("/api/problems/:id", h.get())
	admin := r.Group("/api/admin/problems", h.requireAdmin)
	admin.GET("", h.list(true))
	admin.POST("", h.create)
	admin.GET("/:id", h.get())
	admin.PUT("/:id", h.update)
	admin.GET("/:id/data-versions", h.versions)
	admin.POST("/:id/data-versions", h.importData)
	admin.GET("/:id/data-versions/:version", h.version)
	admin.POST("/markdown-preview", h.previewMarkdown)
	admin.POST("/package-preview", h.previewPackage)
	admin.POST("/package-import", h.importPackage)
}

const actorKey = "problem-admin-actor"

func (h *Handler) requireAdmin(c *gin.Context) {
	cookie, err := c.Request.Cookie(auth.CookieName)
	if err != nil || cookie.Value == "" {
		respond(c, 401, 40101, "请先登录", nil)
		c.Abort()
		return
	}
	user, err := h.auth.CurrentUser(c.Request.Context(), cookie.Value)
	if err != nil {
		problemError(c, err)
		return
	}
	if user.ID == 0 || user.GlobalRole != "super_admin" || user.Status != "active" {
		problemError(c, ErrForbidden)
		return
	}
	c.Set(actorKey, Actor{ID: user.ID, Role: user.GlobalRole})
	c.Next()
}

func currentActor(c *gin.Context) Actor {
	value, _ := c.Get(actorKey)
	actor, _ := value.(Actor)
	return actor
}

func respond(c *gin.Context, status, code int, message string, data any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{code, message, data})
}

func problemError(c *gin.Context, err error) {
	c.Abort()
	switch {
	case errors.Is(err, ErrInvalidPackage):
		respond(c, 400, 40001, "题包格式或测试数据不合法；请检查 problem.yaml、Markdown 和 testdata/config.yaml", nil)
	case errors.Is(err, testcase.ErrInvalidPath):
		respond(c, 400, 40001, "测试文件名不合法；只接受根目录下由字母、数字、下划线或连字符组成的 .in/.out 文件", nil)
	case errors.Is(err, testcase.ErrPairing):
		respond(c, 400, 40001, "测试文件未成对、存在重名或大小写冲突", nil)
	case errors.Is(err, testcase.ErrScore):
		respond(c, 400, 40001, "测试点分值不完整或总分无效", nil)
	case errors.Is(err, testcase.ErrInvalidArchive):
		respond(c, 400, 40001, "测试数据 ZIP 无效或已损坏", nil)
	case errors.Is(err, ErrInvalid), errors.Is(err, httpx.ErrInvalidJSON):
		respond(c, 400, 40001, "请求参数或测试数据不合法", nil)
	case errors.Is(err, testcase.ErrLimitExceeded):
		respond(c, 413, 41301, "上传内容超过限制", nil)
	case errors.Is(err, auth.ErrCredentials):
		respond(c, 401, 40101, "会话无效，请重新登录", nil)
	case errors.Is(err, ErrForbidden):
		respond(c, 403, 40302, "需要超级管理员权限", nil)
	case errors.Is(err, ErrNotFound):
		respond(c, 404, 40401, "题目或版本不存在", nil)
	case errors.Is(err, ErrConflict):
		respond(c, 409, 40901, "题目已修改，请刷新后重试", nil)
	default:
		respond(c, 503, 50301, "服务暂时不可用，请稍后重试", nil)
	}
}

func positive(raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 63)
	if err != nil || value == 0 {
		return 0, ErrInvalid
	}
	return value, nil
}

func parsePagination(c *gin.Context) (int, int, error) {
	page, limit := 1, 20
	for _, entry := range []struct {
		key    string
		target *int
		max    int
	}{{"page", &page, 100000}, {"limit", &limit, 100}} {
		if raw, ok := c.GetQuery(entry.key); ok {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > entry.max {
				return 0, 0, ErrInvalid
			}
			*entry.target = v
		}
	}
	return page, limit, nil
}

func (h *Handler) list(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, limit, err := parsePagination(c)
		if err != nil {
			problemError(c, err)
			return
		}
		opts := ListOptions{Page: page, Limit: limit, Query: c.Query("q"), Difficulty: c.Query("difficulty"), Tag: c.Query("tag")}
		opts.Category = c.Query("category")
		if admin {
			opts.Status, opts.Visibility = c.Query("status"), c.Query("visibility")
		}
		result, err := h.service.List(c.Request.Context(), currentActor(c), opts)
		if err != nil {
			problemError(c, err)
			return
		}
		respond(c, 200, 0, "success", result)
	}
}

func (h *Handler) get() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := positive(c.Param("id"))
		if err != nil {
			problemError(c, err)
			return
		}
		result, err := h.service.Get(c.Request.Context(), currentActor(c), id)
		if err != nil {
			problemError(c, err)
			return
		}
		respond(c, 200, 0, "success", result)
	}
}

func (h *Handler) create(c *gin.Context) {
	var input Input
	if err := c.ShouldBindWith(&input, httpx.JSONBinding{MaxBytes: 256 << 10}); err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.Create(c.Request.Context(), currentActor(c), input, server.RequestID(c.Request.Context()))
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 201, 0, "success", result)
}

func (h *Handler) update(c *gin.Context) {
	id, err := positive(c.Param("id"))
	if err != nil {
		problemError(c, err)
		return
	}
	var input Input
	if err := c.ShouldBindWith(&input, httpx.JSONBinding{MaxBytes: 256 << 10}); err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.Update(c.Request.Context(), currentActor(c), id, input, server.RequestID(c.Request.Context()))
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 200, 0, "success", result)
}

func (h *Handler) versions(c *gin.Context) {
	id, err := positive(c.Param("id"))
	if err != nil {
		problemError(c, err)
		return
	}
	page, limit, err := parsePagination(c)
	if err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.ListDataVersions(c.Request.Context(), currentActor(c), id, page, limit)
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 200, 0, "success", result)
}

func (h *Handler) version(c *gin.Context) {
	id, err := positive(c.Param("id"))
	if err != nil {
		problemError(c, err)
		return
	}
	version, err := positive(c.Param("version"))
	if err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.GetDataVersion(c.Request.Context(), currentActor(c), id, version)
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 200, 0, "success", result)
}

func (h *Handler) previewMarkdown(c *gin.Context) {
	var input struct {
		Markdown string `json:"markdown"`
	}
	if err := c.ShouldBindWith(&input, httpx.JSONBinding{MaxBytes: 128 << 10}); err != nil {
		problemError(c, err)
		return
	}
	rendered, err := markdownsafe.Render(input.Markdown, 64<<10)
	if errors.Is(err, markdownsafe.ErrInvalid) {
		problemError(c, ErrInvalid)
		return
	}
	if err != nil {
		problemError(c, ErrUnavailable)
		return
	}
	respond(c, 200, 0, "success", map[string]string{"html": rendered})
}

func (h *Handler) readPackage(c *gin.Context) (ProblemPackage, string, error) {
	if c.Request.ContentLength > maxImportRequest {
		return ProblemPackage{}, "", testcase.ErrLimitExceeded
	}
	controller := http.NewResponseController(c.Writer)
	deadline := time.Now().Add(importBudget)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportRequest)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return ProblemPackage{}, "", ErrInvalidPackage
	}
	var archive []byte
	sample := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ProblemPackage{}, "", ErrInvalidPackage
		}
		switch part.FormName() {
		case "archive":
			if part.FileName() == "" || archive != nil {
				return ProblemPackage{}, "", ErrInvalidPackage
			}
			archive, err = io.ReadAll(io.LimitReader(part, maxPackageArchiveBytes+1))
			if err != nil || len(archive) > maxPackageArchiveBytes {
				return ProblemPackage{}, "", ErrInvalidPackage
			}
		case "sample":
			value, readErr := io.ReadAll(io.LimitReader(part, 8))
			if readErr != nil || part.FileName() != "" || string(bytes.TrimSpace(value)) != "1" || sample {
				return ProblemPackage{}, "", ErrInvalidPackage
			}
			sample = true
		default:
			return ProblemPackage{}, "", ErrInvalidPackage
		}
		_ = part.Close()
	}
	if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
		return ProblemPackage{}, "", ErrInvalidPackage
	}
	if sample == (archive != nil) {
		return ProblemPackage{}, "", ErrInvalidPackage
	}
	if sample {
		if strings.TrimSpace(h.samples) == "" {
			return ProblemPackage{}, "", ErrInvalidPackage
		}
		pkg, err := ParsePackageDirectory(c.Request.Context(), h.samples)
		return pkg, "builtin", err
	}
	pkg, err := ParsePackageZIP(c.Request.Context(), archive)
	return pkg, "upload", err
}

func (h *Handler) previewPackage(c *gin.Context) {
	select {
	case h.imports <- struct{}{}:
		defer func() { <-h.imports }()
	default:
		c.Header("Retry-After", "5")
		respond(c, 429, 42901, "正在处理其他导入，请稍后重试", nil)
		return
	}
	pkg, source, err := h.readPackage(c)
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 200, 0, "success", map[string]any{"preview": pkg.Preview, "source": source})
}

func (h *Handler) importPackage(c *gin.Context) {
	select {
	case h.imports <- struct{}{}:
		defer func() { <-h.imports }()
	default:
		c.Header("Retry-After", "5")
		respond(c, 429, 42901, "正在处理其他导入，请稍后重试", nil)
		return
	}
	pkg, _, err := h.readPackage(c)
	if err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.ImportPackage(c.Request.Context(), currentActor(c), pkg, server.RequestID(c.Request.Context()))
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 201, 0, "success", result)
}

const (
	maxImportRequest = 66 << 20
	importBudget     = 2 * time.Minute
)

func (h *Handler) importData(c *gin.Context) {
	id, err := positive(c.Param("id"))
	if err != nil {
		problemError(c, err)
		return
	}
	select {
	case h.imports <- struct{}{}:
		defer func() { <-h.imports }()
	default:
		c.Header("Retry-After", "5")
		respond(c, 429, 42901, "正在处理其他导入，请稍后重试", nil)
		return
	}
	if c.Request.ContentLength > maxImportRequest {
		problemError(c, testcase.ErrLimitExceeded)
		return
	}
	controller := http.NewResponseController(c.Writer)
	deadline := time.Now().Add(importBudget)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportRequest)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		problemError(c, ErrInvalid)
		return
	}
	var archive []byte
	var files []testcase.File
	var scores map[string]int64
	var revision uint64
	mode := ImportReplace
	seen := make(map[string]bool)
	totalFileBytes := int64(0)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			importReadError(c, err)
			return
		}
		name := part.FormName()
		if name == "files" {
			_, disposition, dispositionErr := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			rawName := disposition["filename"]
			if dispositionErr != nil || rawName == "" || rawName != part.FileName() {
				problemError(c, testcase.ErrInvalidPath)
				return
			}
			if len(files) >= testcase.DefaultLimits().MaxCases*2 {
				problemError(c, testcase.ErrLimitExceeded)
				return
			}
			data, readErr := io.ReadAll(io.LimitReader(part, testcase.DefaultLimits().MaxFileBytes+1))
			if readErr != nil {
				importReadError(c, readErr)
				return
			}
			if int64(len(data)) > testcase.DefaultLimits().MaxFileBytes || totalFileBytes+int64(len(data)) > testcase.DefaultLimits().MaxTotalBytes {
				problemError(c, testcase.ErrLimitExceeded)
				return
			}
			totalFileBytes += int64(len(data))
			files = append(files, testcase.File{Name: rawName, Data: data})
			if err := part.Close(); err != nil {
				importReadError(c, err)
				return
			}
			continue
		}
		if seen[name] || (name != "archive" && name != "scores" && name != "revision" && name != "mode") {
			problemError(c, ErrInvalid)
			return
		}
		seen[name] = true
		maxSize := int64(64 << 10)
		if name == "archive" {
			if part.FileName() == "" {
				problemError(c, ErrInvalid)
				return
			}
			maxSize = 32 << 20
		} else if part.FileName() != "" {
			problemError(c, ErrInvalid)
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, maxSize+1))
		if err != nil {
			importReadError(c, err)
			return
		}
		if int64(len(data)) > maxSize {
			problemError(c, testcase.ErrLimitExceeded)
			return
		}
		if err := part.Close(); err != nil {
			importReadError(c, err)
			return
		}
		switch name {
		case "archive":
			archive = data
		case "scores":
			if err := decodeScores(data, &scores); err != nil {
				problemError(c, err)
				return
			}
		case "revision":
			revision, err = positive(string(data))
			if err != nil {
				problemError(c, err)
				return
			}
		case "mode":
			mode = string(bytes.TrimSpace(data))
			if mode != ImportReplace && mode != ImportMerge {
				problemError(c, ErrInvalid)
				return
			}
		}
	}
	// MultipartReader stops at the final boundary. Count any epilogue too,
	// including chunked bodies that have no trustworthy Content-Length.
	if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
		importReadError(c, err)
		return
	}
	if !seen["scores"] || !seen["revision"] || (seen["archive"] == (len(files) > 0)) {
		problemError(c, ErrInvalid)
		return
	}
	var dataset testcase.Dataset
	if seen["archive"] {
		dataset, err = testcase.Parse(c.Request.Context(), bytes.NewReader(archive), int64(len(archive)), scores, testcase.DefaultLimits())
	} else {
		dataset, err = testcase.ParseFiles(c.Request.Context(), files, scores, testcase.DefaultLimits())
	}
	if err != nil {
		problemError(c, err)
		return
	}
	result, err := h.service.Import(c.Request.Context(), currentActor(c), id, revision, dataset, mode, server.RequestID(c.Request.Context()))
	if err != nil {
		problemError(c, err)
		return
	}
	respond(c, 201, 0, "success", result)
}

func importReadError(c *gin.Context, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		problemError(c, testcase.ErrLimitExceeded)
		return
	}
	problemError(c, ErrInvalid)
}

func decodeScores(raw []byte, target *map[string]int64) error {
	if !utf8.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if _, err := decoder.Token(); err != nil {
		return ErrInvalid
	}
	result := make(map[string]int64)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		name, ok := key.(string)
		if !ok {
			return ErrInvalid
		}
		if _, exists := result[name]; exists {
			return ErrInvalid
		}
		score, err := decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		number, ok := score.(json.Number)
		if !ok {
			return ErrInvalid
		}
		value, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || value < 0 {
			return ErrInvalid
		}
		result[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return ErrInvalid
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return ErrInvalid
	}
	*target = result
	return nil
}
