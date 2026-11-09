package judge_runner

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"biue/internal/judge"
	"github.com/gin-gonic/gin"
)

func Handler(manager *Manager, token string) http.Handler {
	r := gin.New()
	r.SetTrustedProxies(nil)
	admission := make(chan struct{}, cap(manager.slots)+1)
	r.GET("/health", func(c *gin.Context) { c.Status(200) })
	auth := func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		provided := strings.TrimPrefix(header, "Bearer ")
		if !strings.HasPrefix(header, "Bearer ") || len(token) < 32 || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			c.AbortWithStatus(401)
		}
	}
	r.GET("/ready", auth, func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5e9)
		defer cancel()
		if manager.Ready(ctx) != nil {
			c.Status(503)
		} else {
			c.Status(200)
		}
	})
	r.GET("/engine-ready", auth, func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5e9)
		defer cancel()
		if manager.BackendReady(ctx) != nil {
			c.Status(503)
		} else {
			c.Status(200)
		}
	})
	r.POST("/v1/judge", auth, func(c *gin.Context) {
		select {
		case admission <- struct{}{}:
			defer func() { <-admission }()
		default:
			c.Status(503)
			return
		}
		media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || media != "application/json" {
			c.Status(415)
			return
		}
		body := http.MaxBytesReader(c.Writer, c.Request.Body, judge.MaxRequestBytes)
		defer body.Close()
		decoder := json.NewDecoder(body)
		decoder.DisallowUnknownFields()
		var job judge.RunnerJob
		var extra any
		if decoder.Decode(&job) != nil || decoder.Decode(&extra) != io.EOF || job.Request.Validate() != nil || !validToken(job.LeaseToken) {
			c.Status(400)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), judge.RunnerBudget(job.Request))
		defer cancel()
		started := false
		encoder := json.NewEncoder(c.Writer)
		emit := func(event judge.Event) error {
			if !started {
				c.Header("Content-Type", "application/x-ndjson")
				c.Header("Cache-Control", "no-store")
				c.Header("X-Content-Type-Options", "nosniff")
				c.Status(200)
				started = true
			}
			if encoder.Encode(event) != nil {
				return ErrSandbox
			}
			c.Writer.Flush()
			return nil
		}
		result, err := manager.Judge(ctx, job.Request, job.LeaseToken, func(phase judge.Status) error { return emit(judge.Event{Phase: phase}) })
		if err != nil {
			if !started {
				if err == ErrConflict {
					c.Status(409)
				} else {
					c.Status(503)
				}
			}
			return
		}
		_ = emit(judge.Event{Result: &result})
	})
	return r
}

func validToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
