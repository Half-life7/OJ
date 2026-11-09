package submission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"biue/internal/auth"
	"biue/internal/httpx"
	"biue/internal/judge"
	"biue/internal/server"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type Operations interface {
	Create(context.Context, Actor, string, CreateInput, string) (Submission, error)
	List(context.Context, Actor, ListOptions) (Page, error)
	Get(context.Context, Actor, uint64) (Submission, error)
}

type Authenticator interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type Handler struct {
	service Operations
	auth    Authenticator
	redis   *redis.Client
}

func NewHandler(service Operations, authenticator Authenticator, client *redis.Client) (*Handler, error) {
	if service == nil || authenticator == nil || client == nil {
		return nil, errors.New("invalid submission HTTP dependencies")
	}
	return &Handler{service: service, auth: authenticator, redis: client}, nil
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/api/languages", func(c *gin.Context) {
		items := make([]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Mode string `json:"mode"`
		}, 0, 4)
		for _, language := range judge.Languages() {
			items = append(items, struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Mode string `json:"mode"`
			}{language.ID, language.Name, language.EditorMode})
		}
		respond(c, 200, 0, "success", items)
	})
	r.GET("/api/submissions", h.requireAccount, h.list)
	r.GET("/api/submissions/:id", h.requireAccount, h.get)
	r.POST("/api/submissions", h.requireAccount, h.create(string(judge.Submit)))
	r.POST("/api/runs", h.requireAccount, h.create(string(judge.Run)))
}

const actorKey = "submission-actor"

func (h *Handler) requireAccount(c *gin.Context) {
	cookie, err := c.Request.Cookie(auth.CookieName)
	if err != nil || cookie.Value == "" {
		respond(c, 401, 40101, "请先登录", nil)
		c.Abort()
		return
	}
	user, err := h.auth.CurrentUser(c.Request.Context(), cookie.Value)
	if err != nil {
		respondError(c, err)
		return
	}
	if user.ID == 0 || user.Status != "active" {
		respondError(c, auth.ErrCredentials)
		return
	}
	c.Set(actorKey, Actor{ID: user.ID, AccountType: user.AccountType, Role: user.GlobalRole})
	c.Next()
}

func actor(c *gin.Context) Actor { value, _ := c.Get(actorKey); a, _ := value.(Actor); return a }

func respond(c *gin.Context, status, code int, message string, data any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}{code, message, data})
}

var errRateLimited = errors.New("submission rate limited")

func respondError(c *gin.Context, err error) {
	c.Abort()
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, httpx.ErrInvalidJSON):
		respond(c, 400, 40001, "提交参数不合法", nil)
	case errors.Is(err, auth.ErrCredentials):
		respond(c, 401, 40101, "会话无效，请重新登录", nil)
	case errors.Is(err, ErrForbidden):
		respond(c, 403, 40302, "当前账号不能进行此操作", nil)
	case errors.Is(err, ErrNotFound):
		respond(c, 404, 40401, "题目或提交记录不存在", nil)
	case errors.Is(err, ErrNoData):
		respond(c, 409, 40902, "题目尚无可用测试数据", nil)
	case errors.Is(err, errRateLimited):
		c.Header("Retry-After", "60")
		respond(c, 429, 42901, "提交过于频繁，请稍后重试", nil)
	case errors.Is(err, ErrQueueFull):
		c.Header("Retry-After", "5")
		respond(c, 503, 50302, "评测队列已满，请稍后重试", nil)
	default:
		respond(c, 503, 50301, "服务暂时不可用，请稍后重试", nil)
	}
}

func positive(raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 63)
	if err != nil || n == 0 {
		return 0, ErrInvalid
	}
	return n, nil
}

func parsePage(c *gin.Context) (int, int, error) {
	page, limit := 1, 20
	for _, item := range []struct {
		key   string
		value *int
		max   int
	}{{"page", &page, 100000}, {"limit", &limit, 100}} {
		if raw, ok := c.GetQuery(item.key); ok {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > item.max {
				return 0, 0, ErrInvalid
			}
			*item.value = n
		}
	}
	return page, limit, nil
}

func (h *Handler) list(c *gin.Context) {
	page, limit, err := parsePage(c)
	if err != nil {
		respondError(c, err)
		return
	}
	opts := ListOptions{Page: page, Limit: limit, Kind: c.Query("kind")}
	if raw, ok := c.GetQuery("problem_id"); ok {
		opts.ProblemID, err = positive(raw)
		if err != nil {
			respondError(c, err)
			return
		}
	}
	if opts.Kind != "" && !judge.ValidKind(judge.Kind(opts.Kind)) {
		respondError(c, ErrInvalid)
		return
	}
	result, err := h.service.List(c.Request.Context(), actor(c), opts)
	if err != nil {
		respondError(c, err)
		return
	}
	respond(c, 200, 0, "success", result)
}

func (h *Handler) get(c *gin.Context) {
	id, err := positive(c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	result, err := h.service.Get(c.Request.Context(), actor(c), id)
	if err != nil {
		respondError(c, err)
		return
	}
	// Defense in depth at the external boundary, even for alternative stores.
	if result.Kind == string(judge.Submit) {
		result.Stdin = nil
		result.Output = ""
		if result.Status != judge.CompileError {
			result.ErrorMessage = ""
		}
	}
	respond(c, 200, 0, "success", result)
}

func (h *Handler) create(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		a := actor(c)
		if a.AccountType != "permanent" {
			respondError(c, ErrForbidden)
			return
		}
		var input CreateInput
		if kind == string(judge.Submit) {
			var body struct {
				ProblemID  uint64 `json:"problem_id"`
				Language   string `json:"language"`
				SourceCode string `json:"source_code"`
			}
			if err := c.ShouldBindWith(&body, httpx.JSONBinding{MaxBytes: 256 << 10}); err != nil {
				respondError(c, err)
				return
			}
			input = CreateInput{ProblemID: body.ProblemID, Language: body.Language, SourceCode: body.SourceCode}
		} else if err := c.ShouldBindWith(&input, httpx.JSONBinding{MaxBytes: 256 << 10}); err != nil {
			respondError(c, err)
			return
		}
		if err := h.limit(c.Request, a.ID, kind); err != nil {
			respondError(c, err)
			return
		}
		result, err := h.service.Create(c.Request.Context(), a, kind, input, server.RequestID(c.Request.Context()))
		if err != nil {
			respondError(c, err)
			return
		}
		if result.Kind == string(judge.Submit) {
			result.Stdin = nil
			result.Output = ""
			result.ErrorMessage = ""
		}
		respond(c, 201, 0, "success", result)
	}
}

const admissionRateScript = `
local a = redis.call('INCR', KEYS[1])
if a == 1 then redis.call('EXPIRE', KEYS[1], 60) end
local b = redis.call('INCR', KEYS[2])
if b == 1 then redis.call('EXPIRE', KEYS[2], 60) end
if a > tonumber(ARGV[1]) or b > tonumber(ARGV[2]) then return 0 end
return 1
`

func (h *Handler) limit(r *http.Request, userID uint64, kind string) error {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	key := func(s string) string { digest := sha256.Sum256([]byte(s)); return hex.EncodeToString(digest[:]) }
	userMax, ipMax := 10, 30
	if kind == string(judge.Run) {
		userMax, ipMax = 5, 15
	}
	keys := []string{"oj:submission:rate:" + kind + ":user:" + key(strconv.FormatUint(userID, 10)), "oj:submission:rate:" + kind + ":ip:" + key(strings.Trim(ip, "[]"))}
	allowed, err := h.redis.Eval(r.Context(), admissionRateScript, keys, userMax, ipMax).Int()
	if err != nil {
		return ErrUnavailable
	}
	if allowed != 1 {
		return errRateLimited
	}
	return nil
}
