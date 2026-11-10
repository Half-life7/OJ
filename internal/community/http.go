package community

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
	s *Service
	a Authenticator
}

func NewHandler(s *Service, a Authenticator) *Handler { return &Handler{s, a} }
func response(c *gin.Context, v any, e error) {
	status, msg := 200, "success"
	if e != nil {
		v = nil
		status = 503
		msg = "服务暂时不可用"
		switch {
		case errors.Is(e, ErrInvalid), errors.Is(e, httpx.ErrInvalidJSON):
			status = 400
			msg = "参数不合法"
		case errors.Is(e, auth.ErrCredentials):
			status = 401
			msg = "请先登录"
		case errors.Is(e, ErrForbidden):
			status = 403
			msg = "没有权限或尚未通过本题"
		case errors.Is(e, ErrNotFound):
			status = 404
			msg = "内容不存在或不可见"
		case errors.Is(e, ErrConflict):
			status = 409
			msg = "内容已更新，请重新加载"
		case errors.Is(e, ErrRateLimited):
			status = 429
			msg = "操作过于频繁，请稍后再试"
			c.Header("Retry-After", "60")
		}
		c.Abort()
	}
	code := 0
	if status != 200 {
		code = status*100 + 1
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"code": code, "message": msg, "data": v})
}
func actor(c *gin.Context) auth.User {
	v, _ := c.Get("community-user")
	a, _ := v.(auth.User)
	return a
}
func parseID(raw string, zero bool) (uint64, error) {
	n, e := strconv.ParseUint(raw, 10, 63)
	if e != nil || !zero && n == 0 {
		return 0, ErrInvalid
	}
	return n, nil
}
func binding(c *gin.Context, v any) bool {
	if e := c.ShouldBindWith(v, httpx.JSONBinding{MaxBytes: 128 << 10}); e != nil {
		response(c, nil, ErrInvalid)
		return false
	}
	return true
}
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("")
	g.Use(func(c *gin.Context) {
		a := auth.User{}
		cookie, e := c.Request.Cookie(auth.CookieName)
		if e == nil {
			a, e = h.a.CurrentUser(c.Request.Context(), cookie.Value)
			if e != nil {
				response(c, nil, e)
				return
			}
		}
		if c.Request.Method != "GET" && (a.ID == 0 || a.Status != "active") {
			response(c, nil, auth.ErrCredentials)
			return
		}
		c.Set("community-user", a)
		c.Next()
	})
	g.GET("/api/community", func(c *gin.Context) {
		f := Filter{Kind: c.DefaultQuery("kind", "list"), Mine: c.Query("mine") == "1", Favorites: c.Query("favorites") == "1", Review: c.Query("review") == "1"}
		for _, p := range []struct {
			key  string
			dest *uint64
		}{{"after", &f.After}, {"problem_id", &f.ProblemID}, {"contest_id", &f.ContestID}, {"parent_id", &f.ParentID}} {
			if raw, ok := c.GetQuery(p.key); ok {
				n, e := parseID(raw, p.key == "after")
				if e != nil {
					response(c, nil, e)
					return
				}
				*p.dest = n
			}
		}
		v, e := h.s.List(c.Request.Context(), actor(c), f)
		response(c, v, e)
	})
	g.GET("/api/community/:id", func(c *gin.Context) {
		id, e := parseID(c.Param("id"), false)
		if e != nil {
			response(c, nil, e)
			return
		}
		v, e := h.s.Get(c.Request.Context(), actor(c), id)
		response(c, v, e)
	})
	g.POST("/api/community", func(c *gin.Context) {
		var in Input
		if !binding(c, &in) {
			return
		}
		id, e := h.s.Save(c.Request.Context(), actor(c), 0, in)
		response(c, gin.H{"id": id}, e)
	})
	g.PUT("/api/community/:id", func(c *gin.Context) {
		id, e := parseID(c.Param("id"), false)
		if e != nil {
			response(c, nil, e)
			return
		}
		var in Input
		if !binding(c, &in) {
			return
		}
		id, e = h.s.Save(c.Request.Context(), actor(c), id, in)
		response(c, gin.H{"id": id}, e)
	})
	g.POST("/api/community/:id/actions", func(c *gin.Context) {
		id, e := parseID(c.Param("id"), false)
		if e != nil {
			response(c, nil, e)
			return
		}
		var in struct {
			Action   string `json:"action"`
			Revision uint64 `json:"revision"`
		}
		if !binding(c, &in) {
			return
		}
		e = h.s.Action(c.Request.Context(), actor(c), id, in.Revision, in.Action)
		response(c, nil, e)
	})
	g.GET("/api/practice/problems/:id", func(c *gin.Context) {
		id, e := parseID(c.Param("id"), false)
		if e != nil {
			response(c, nil, e)
			return
		}
		v, e := h.s.Stats(c.Request.Context(), actor(c).ID, id)
		response(c, v, e)
	})
	g.GET("/api/leaderboards/:kind", func(c *gin.Context) {
		page, e := strconv.Atoi(c.DefaultQuery("page", "1"))
		if e != nil {
			response(c, nil, ErrInvalid)
			return
		}
		v, e := h.s.BoardPeriod(c.Request.Context(), c.Param("kind"), c.DefaultQuery("period", "all"), page)
		response(c, v, e)
	})
}
