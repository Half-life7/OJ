package competition

import (
	"biue/internal/auth"
	"biue/internal/httpx"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"strconv"
)

type Authenticator interface {
	CurrentUser(context.Context, string) (auth.User, error)
}
type Handler struct {
	service *Service
	auth    Authenticator
}

func NewHandler(s *Service, a Authenticator) *Handler { return &Handler{s, a} }
func reply(c *gin.Context, data any, err error) {
	status, message := 200, "success"
	if err != nil {
		data = nil
		status = 503
		message = "服务暂时不可用"
		switch {
		case errors.Is(err, ErrInvalid), errors.Is(err, httpx.ErrInvalidJSON):
			status = 400
			message = "参数不合法"
		case errors.Is(err, auth.ErrCredentials):
			status = 401
			message = "请先登录"
		case errors.Is(err, ErrForbidden):
			status = 403
			message = "没有操作权限"
		case errors.Is(err, ErrConflict):
			status = 409
			message = "状态已变化或存在重复记录，请刷新后重试"
		}
		var fields *FieldError
		if errors.As(err, &fields) {
			data = gin.H{"fields": fields.Fields}
			message = fields.Error()
		}
		c.Abort()
	}
	code := 0
	if status != 200 {
		code = status*100 + 1
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"code": code, "message": message, "data": data})
}
func user(c *gin.Context) auth.User { return c.MustGet("competition-user").(auth.User) }
func id(c *gin.Context) uint64      { n, _ := strconv.ParseUint(c.Param("id"), 10, 63); return n }
func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindWith(v, httpx.JSONBinding{MaxBytes: 64 << 10}); err != nil {
		reply(c, nil, ErrInvalid)
		return false
	}
	return true
}
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("")
	g.Use(func(c *gin.Context) {
		cookie, err := c.Request.Cookie(auth.CookieName)
		if err != nil {
			reply(c, nil, auth.ErrCredentials)
			return
		}
		a, err := h.auth.CurrentUser(c.Request.Context(), cookie.Value)
		if err != nil {
			reply(c, nil, err)
			return
		}
		if a.Status != "active" {
			reply(c, nil, auth.ErrCredentials)
			return
		}
		c.Set("competition-user", a)
		c.Next()
	})
	g.POST("/api/contests/:id/csv/preview", func(c *gin.Context) {
		var in struct {
			CSV string `json:"csv"`
		}
		if e := c.ShouldBindWith(&in, httpx.JSONBinding{MaxBytes: 1 << 20}); e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.service.PreviewCSV(c.Request.Context(), user(c), id(c), in.CSV)
		reply(c, v, e)
	})
	g.POST("/api/contests/:id/csv/confirm", func(c *gin.Context) {
		var in struct {
			Token string `json:"token"`
		}
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.service.ConfirmCSV(c.Request.Context(), user(c), id(c), in.Token))
	})
	g.GET("/api/contests/:id/export.csv", func(c *gin.Context) {
		v, e := h.service.ExportCSV(c.Request.Context(), user(c), id(c))
		if e != nil {
			reply(c, nil, e)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Disposition", `attachment; filename="contest.csv"`)
		c.Data(200, "text/csv; charset=utf-8", v)
	})
	g.POST("/api/contests/:id/temporary-accounts", func(c *gin.Context) {
		var in Temporary
		if !bind(c, &in) {
			return
		}
		v, e := h.service.Temporary(c.Request.Context(), user(c), id(c), in)
		reply(c, gin.H{"user_id": v}, e)
	})
	g.GET("/api/contests/:id/submissions", func(c *gin.Context) {
		u, e := strconv.ParseUint(c.DefaultQuery("user_id", strconv.FormatUint(user(c).ID, 10)), 10, 63)
		if e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		after, e := strconv.ParseUint(c.DefaultQuery("after", "0"), 10, 63)
		if e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.service.Submissions(c.Request.Context(), user(c), id(c), u, after)
		reply(c, v, e)
	})
	g.GET("/api/contests", func(c *gin.Context) {
		before, err := strconv.ParseUint(c.DefaultQuery("before", "0"), 10, 63)
		if err != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.service.List(c.Request.Context(), before)
		reply(c, v, e)
	})
	g.POST("/api/contests", func(c *gin.Context) {
		var in Contest
		if !bind(c, &in) {
			return
		}
		if in.ID != 0 {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.service.Save(c.Request.Context(), user(c), in)
		reply(c, gin.H{"id": v}, e)
	})
	g.PUT("/api/contests/:id", func(c *gin.Context) {
		var in Contest
		if !bind(c, &in) {
			return
		}
		if id(c) == 0 {
			reply(c, nil, ErrInvalid)
			return
		}
		in.ID = id(c)
		v, e := h.service.Save(c.Request.Context(), user(c), in)
		reply(c, gin.H{"id": v}, e)
	})
	g.GET("/api/contests/:id", func(c *gin.Context) { v, e := h.service.View(c.Request.Context(), user(c), id(c)); reply(c, v, e) })
	g.GET("/api/contests/:id/manage-options", func(c *gin.Context) {
		v, e := h.service.ManageOptions(c.Request.Context(), user(c), id(c), c.Query("q"))
		reply(c, v, e)
	})
	g.POST("/api/contests/:id/enroll", func(c *gin.Context) {
		var in struct{}
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.service.Enroll(c.Request.Context(), user(c), id(c)))
	})
	g.POST("/api/contests/:id/manage", func(c *gin.Context) {
		var in Manage
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.service.Manage(c.Request.Context(), user(c), id(c), in))
	})
	g.POST("/api/contests/:id/settle", func(c *gin.Context) {
		var in struct{}
		if !bind(c, &in) {
			return
		}
		v, e := h.service.Settle(c.Request.Context(), user(c), id(c))
		reply(c, v, e)
	})
	g.GET("/api/admin/persons", func(c *gin.Context) {
		after, e := strconv.ParseUint(c.DefaultQuery("after", "0"), 10, 63)
		if e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.service.People(c.Request.Context(), user(c), after)
		reply(c, v, e)
	})
	g.PUT("/api/admin/persons/:id", func(c *gin.Context) {
		var in IdentityChange
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.service.Identity(c.Request.Context(), user(c), id(c), in))
	})
	g.POST("/api/admin/identity-links", func(c *gin.Context) {
		var in Link
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.service.Link(c.Request.Context(), user(c), in))
	})
}
