package submission

import (
	"biue/internal/auth"
	"biue/internal/judge"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var streams = struct {
	sync.Mutex
	users map[uint64]int
	total int
}{users: map[uint64]int{}}

func acquireStream(user uint64) bool {
	streams.Lock()
	defer streams.Unlock()
	if streams.total >= 32 || streams.users[user] >= 2 {
		return false
	}
	streams.total++
	streams.users[user]++
	return true
}
func releaseStream(user uint64) {
	streams.Lock()
	defer streams.Unlock()
	streams.total--
	streams.users[user]--
	if streams.users[user] == 0 {
		delete(streams.users, user)
	}
}

// Events sends bounded summary-only snapshots. Authentication and object access
// are rechecked before every snapshot so revocation ends an existing stream.
func (h *Handler) events(c *gin.Context) {
	id, e := positive(c.Param("id"))
	if e != nil {
		respondError(c, e)
		return
	}
	a := actor(c)
	if !acquireStream(a.ID) {
		respond(c, 429, 42901, "实时连接过多，请稍后重试", nil)
		return
	}
	defer releaseStream(a.ID)
	initial, e := h.service.Get(c.Request.Context(), a, id)
	if e != nil {
		respondError(c, e)
		return
	}
	if _, ok := any(c.Writer).(http.Flusher); !ok {
		respondError(c, ErrUnavailable)
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	emit := func(name, eventID string, v any) bool {
		b, e := json.Marshal(v)
		if e != nil {
			return false
		}
		if _, e = fmt.Fprintf(c.Writer, "id: %s\nevent: %s\ndata: %s\n\n", eventID, name, b); e != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	last := c.GetHeader("Last-Event-ID")
	send := func(s Submission) bool {
		eventID := strconv.FormatUint(s.Generation, 10) + ":" + string(s.Status) + ":" + s.UpdatedAt.UTC().Format("20060102T150405.000000Z")
		if eventID != last {
			if !emit("snapshot", eventID, s.Summary) {
				return false
			}
			last = eventID
		}
		if judge.Terminal(s.Status) {
			emit("end", eventID, map[string]uint64{"id": id})
			return false
		}
		return true
	}
	fmt.Fprint(c.Writer, "retry: 1000\n\n")
	if !send(initial) {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			cookie, e := c.Request.Cookie(auth.CookieName)
			if e != nil {
				return
			}
			u, e := h.auth.CurrentUser(c.Request.Context(), cookie.Value)
			if e == nil && (u.ID != a.ID || u.Status != "active") {
				e = auth.ErrCredentials
			}
			if e != nil {
				if errors.Is(e, auth.ErrCredentials) {
					emit("unauthorized", last, map[string]bool{"revoked": true})
				} else {
					emit("unavailable", last, struct{}{})
				}
				return
			}
			current := Actor{ID: u.ID, AccountType: u.AccountType, Role: u.GlobalRole}
			s, e := h.service.Get(c.Request.Context(), current, id)
			if e != nil {
				if errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
					emit("unauthorized", last, map[string]bool{"revoked": true})
				} else {
					emit("unavailable", last, struct{}{})
				}
				return
			}
			if !send(s) {
				return
			}
			if _, e = fmt.Fprint(c.Writer, ": keep-alive\n\n"); e != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
