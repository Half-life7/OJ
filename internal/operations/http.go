package operations

import (
	"biue/internal/auth"
	"biue/internal/httpx"
	"biue/internal/judge"
	"biue/internal/server"
	"biue/internal/submission"
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
func reply(c *gin.Context, v any, e error) {
	status, message := 200, "success"
	if e != nil {
		v = nil
		status = 503
		message = "管理服务暂时不可用"
		switch {
		case errors.Is(e, ErrInvalid), errors.Is(e, httpx.ErrInvalidJSON):
			status = 400
			message = "参数不合法"
		case errors.Is(e, auth.ErrCredentials):
			status = 401
			message = "请先登录"
		case errors.Is(e, ErrForbidden), errors.Is(e, judge.ErrAdminForbidden):
			status = 403
			message = "需要超级管理员权限"
		case errors.Is(e, submission.ErrForbidden):
			status = 403
			message = "没有查看该提交的权限"
		case errors.Is(e, submission.ErrInvalid):
			status = 400
			message = "提交筛选参数不合法"
		case errors.Is(e, submission.ErrNotFound):
			status = 404
			message = "提交记录不存在"
		case errors.Is(e, ErrConflict), errors.Is(e, judge.ErrAdminConflict):
			status = 409
			message = "状态已发生变化，请刷新"
		case errors.Is(e, ErrNotFound):
			status = 404
			message = "测试任务不存在"
		case errors.Is(e, ErrRunnerUnavailable):
			status = 503
			message = "Worker、dispatcher 或 Runner 尚未就绪，无法开始真实评测"
		case errors.Is(e, judge.ErrAdminCapacity):
			status = 503
			message = "队列容量不足，请稍后重试"
		}
		c.Abort()
	}
	code := 0
	if status != 200 {
		code = status*100 + 1
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"code": code, "message": message, "data": v})
}
func actor(c *gin.Context) auth.User { return c.MustGet("operations-user").(auth.User) }
func number(raw string) (uint64, error) {
	v, e := strconv.ParseUint(raw, 10, 63)
	if e != nil {
		return 0, ErrInvalid
	}
	return v, nil
}
func bind(c *gin.Context, v any) bool {
	if e := c.ShouldBindWith(v, httpx.JSONBinding{MaxBytes: 16 << 10}); e != nil {
		reply(c, nil, ErrInvalid)
		return false
	}
	return true
}
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	g := r.Group("/api/admin/operations")
	g.Use(func(c *gin.Context) {
		cookie, e := c.Request.Cookie(auth.CookieName)
		if e != nil {
			reply(c, nil, auth.ErrCredentials)
			return
		}
		a, e := h.a.CurrentUser(c.Request.Context(), cookie.Value)
		if e != nil {
			reply(c, nil, e)
			return
		}
		if e = h.s.authorized(c.Request.Context(), a); e != nil {
			reply(c, nil, e)
			return
		}
		c.Set("operations-user", a)
		c.Next()
	})
	g.GET("/users", func(c *gin.Context) {
		after, e := number(c.DefaultQuery("after", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		v, e := h.s.Users(c.Request.Context(), actor(c), after, c.Query("q"))
		reply(c, v, e)
	})
	g.GET("/submissions", func(c *gin.Context) {
		before, e := number(c.DefaultQuery("before", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		problemID, e := number(c.DefaultQuery("problem_id", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		contestID, e := number(c.DefaultQuery("contest_id", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		limit, e := strconv.Atoi(c.DefaultQuery("limit", "50"))
		if e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		v, e := h.s.AdminSubmissions(c.Request.Context(), actor(c), AdminSubmissionQuery{
			Before: before, ProblemID: problemID, ContestID: contestID, Limit: limit,
			Query: c.Query("q"), Kind: c.Query("kind"), Status: c.Query("status"),
		})
		reply(c, v, e)
	})
	g.GET("/submissions/:id", func(c *gin.Context) {
		id, e := number(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		v, e := h.s.AdminSubmission(c.Request.Context(), actor(c), id)
		reply(c, v, e)
	})
	g.POST("/rejudge", func(c *gin.Context) {
		var in judge.RejudgeInput
		if e := c.ShouldBindWith(&in, httpx.JSONBinding{MaxBytes: 64 << 10}); e != nil {
			reply(c, nil, ErrInvalid)
			return
		}
		in.RequestID = server.RequestID(c.Request.Context())
		v, e := h.s.judge.Rejudge(c.Request.Context(), actor(c).ID, in)
		reply(c, v, e)
	})
	g.POST("/rejudge/contest", func(c *gin.Context) {
		var in ScopeRejudgeInput
		if !bind(c, &in) {
			return
		}
		v, e := h.s.RejudgeContest(c.Request.Context(), actor(c), in)
		reply(c, v, e)
	})
	g.POST("/rejudge/problem", func(c *gin.Context) {
		var in ScopeRejudgeInput
		if !bind(c, &in) {
			return
		}
		v, e := h.s.RejudgeProblem(c.Request.Context(), actor(c), in)
		reply(c, v, e)
	})
	g.POST("/reset", func(c *gin.Context) {
		var in ResetInput
		if !bind(c, &in) {
			return
		}
		v, e := h.s.Reset(c.Request.Context(), actor(c), in, server.RequestID(c.Request.Context()))
		reply(c, v, e)
	})
	g.POST("/simulate", func(c *gin.Context) {
		var in struct{}
		if !bind(c, &in) {
			return
		}
		v, e := h.s.StartSimulation(c.Request.Context(), actor(c))
		reply(c, v, e)
	})
	g.GET("/simulate/:id", func(c *gin.Context) {
		v, e := h.s.SimulationStatus(c.Request.Context(), actor(c), c.Param("id"))
		reply(c, v, e)
	})
	g.POST("/users/:id/status", func(c *gin.Context) {
		id, e := number(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		var in struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if !bind(c, &in) {
			return
		}
		reply(c, nil, h.s.SetStatus(c.Request.Context(), actor(c), id, in.Status, in.Reason))
	})
	g.GET("/users/:id/submissions", func(c *gin.Context) {
		id, e := number(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		after, e := number(c.DefaultQuery("after", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		rows := []submission.Summary{}
		e = h.s.db.SelectContext(c.Request.Context(), &rows, `SELECT id,problem_id,user_id,statement_version,COALESCE(data_version,0) AS data_version,generation,kind,language,status,score,execution_time_ms,memory_usage_kb,passed_cases,total_cases,created_at,updated_at FROM submissions WHERE user_id=? AND id>? ORDER BY id LIMIT 100`, id, after)
		if e != nil {
			e = ErrUnavailable
		}
		reply(c, rows, e)
	})
	g.GET("/metrics", func(c *gin.Context) { v, e := h.s.Metrics(c.Request.Context(), actor(c)); reply(c, v, e) })
	g.GET("/audits", func(c *gin.Context) {
		after, e := number(c.DefaultQuery("after", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		v, e := h.s.Audits(c.Request.Context(), actor(c), after)
		reply(c, v, e)
	})
	g.GET("/quarantine", func(c *gin.Context) {
		after, e := number(c.DefaultQuery("after", "0"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		v, e := h.s.Quarantine(c.Request.Context(), actor(c), after)
		reply(c, v, e)
	})
	g.GET("/dead-letters", func(c *gin.Context) {
		v, e := h.s.judge.ListDead(c.Request.Context(), actor(c).ID, 100)
		reply(c, v, e)
	})
	g.POST("/dead-letters/:id/retry", func(c *gin.Context) {
		id, e := number(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		var in struct {
			Generation uint64 `json:"generation"`
		}
		if !bind(c, &in) {
			return
		}
		v, e := h.s.judge.RetryDead(c.Request.Context(), actor(c).ID, id, in.Generation)
		reply(c, v, e)
	})
}
