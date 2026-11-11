// Package operations provides authenticated administration without Docker access.
package operations

import (
	"biue/internal/auth"
	"biue/internal/competition"
	"biue/internal/judge"
	"biue/internal/problem"
	"biue/internal/problemdata"
	"biue/internal/server"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrForbidden = errors.New("administrator required")
var ErrInvalid = errors.New("invalid administrative request")
var ErrConflict = errors.New("administrative state conflict")
var ErrUnavailable = errors.New("administrative storage unavailable")

type Service struct {
	db          *sqlx.DB
	redis       *redis.Client
	data        *problemdata.Store
	problems    *problem.Service
	competition *competition.Service
	judge       *judge.JudgeAdmin
	capacity    int
}

func New(db *sqlx.DB, r *redis.Client, data *problemdata.Store, problems *problem.Service, contests *competition.Service, capacity int) *Service {
	j, _ := judge.NewJudgeAdmin(db, capacity)
	return &Service{db: db, redis: r, data: data, problems: problems, competition: contests, judge: j, capacity: capacity}
}
func (s *Service) authorized(ctx context.Context, a auth.User) error {
	var ok bool
	if s.db.GetContext(ctx, &ok, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND global_role='super_admin' AND account_type='permanent' AND status='active')`, a.ID) != nil {
		return ErrUnavailable
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

type User struct {
	ID       uint64  `db:"id" json:"id"`
	Username string  `db:"username" json:"username"`
	Type     string  `db:"account_type" json:"account_type"`
	Role     string  `db:"global_role" json:"global_role"`
	Status   string  `db:"status" json:"status"`
	Name     string  `db:"display_name" json:"display_name"`
	Student  *string `db:"student_no" json:"student_no"`
	PersonID uint64  `db:"person_id" json:"person_id"`
}

func (s *Service) Users(ctx context.Context, a auth.User, after uint64, q string) ([]User, error) {
	if e := s.authorized(ctx, a); e != nil {
		return nil, e
	}
	if !utf8.ValidString(q) || len(q) > 800 {
		return nil, ErrInvalid
	}
	q = "%" + strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(strings.TrimSpace(q)) + "%"
	rows := []User{}
	e := s.db.SelectContext(ctx, &rows, `SELECT u.id,u.username,u.account_type,u.global_role,u.status,p.display_name,p.student_no,pa.person_id FROM users u JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE u.id>? AND (u.username LIKE ? ESCAPE '=' OR p.display_name LIKE ? ESCAPE '=' OR p.student_no LIKE ? ESCAPE '=') ORDER BY u.id LIMIT 100`, after, q, q, q)
	if e != nil {
		return nil, ErrUnavailable
	}
	return rows, nil
}
func (s *Service) SetStatus(ctx context.Context, a auth.User, id uint64, status, reason string) error {
	if id == 0 || id == a.ID || status != "active" && status != "disabled" || !utf8.ValidString(reason) || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 255 || strings.ContainsFunc(reason, unicode.IsControl) {
		return ErrInvalid
	}
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	var admission int
	if e = tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`); e != nil {
		return ErrUnavailable
	}
	var current User
	if e = tx.GetContext(ctx, &current, `SELECT id,username,account_type,global_role,status FROM users WHERE id=? FOR UPDATE`, a.ID); e != nil {
		return ErrUnavailable
	}
	if current.Role != "super_admin" || current.Status != "active" || current.Type != "permanent" {
		return ErrForbidden
	}
	var target User
	e = tx.GetContext(ctx, &target, `SELECT id,username,account_type,global_role,status FROM users WHERE id=? FOR UPDATE`, id)
	if e == sql.ErrNoRows {
		return ErrConflict
	}
	if e != nil {
		return ErrUnavailable
	}
	if target.Status == "retired" {
		return ErrConflict
	}
	if target.Status == status {
		return nil
	}
	if status == "disabled" && target.Role == "super_admin" {
		var count int
		if tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM users WHERE global_role='super_admin' AND status='active' AND account_type='permanent'`) != nil {
			return ErrUnavailable
		}
		if count <= 1 {
			return ErrConflict
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE users SET status=?,token_version=token_version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, status, id); e != nil {
		return ErrUnavailable
	}
	if _, e = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=UTC_TIMESTAMP(6) WHERE user_id=? AND revoked_at IS NULL`, id); e != nil {
		return ErrUnavailable
	}
	b, _ := json.Marshal(map[string]string{"status": status, "reason": reason})
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,'account.set_status','user',?,?,?,UTC_TIMESTAMP(6))`, a.ID, id, server.RequestID(ctx), string(b)); e != nil {
		return ErrUnavailable
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM global_board_cache`); e != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

type Audit struct {
	ID        uint64          `db:"id" json:"id"`
	Actor     *uint64         `db:"actor_user_id" json:"actor_user_id"`
	Action    string          `db:"action" json:"action"`
	Object    string          `db:"object_type" json:"object_type"`
	ObjectID  uint64          `db:"object_id" json:"object_id"`
	RequestID string          `db:"request_id" json:"request_id"`
	Details   json.RawMessage `db:"details" json:"details"`
	At        time.Time       `db:"created_at" json:"created_at"`
}

func (s *Service) Audits(ctx context.Context, a auth.User, after uint64) ([]Audit, error) {
	if e := s.authorized(ctx, a); e != nil {
		return nil, e
	}
	rows := []Audit{}
	if s.db.SelectContext(ctx, &rows, `SELECT id,actor_user_id,action,object_type,object_id,request_id,details,created_at FROM audit_logs WHERE id>? ORDER BY id LIMIT 100`, after) != nil {
		return nil, ErrUnavailable
	}
	return rows, nil
}
func (s *Service) Metrics(ctx context.Context, a auth.User) (map[string]any, error) {
	if e := s.authorized(ctx, a); e != nil {
		return nil, e
	}
	v := map[string]any{}
	queries := map[string]string{"users": "SELECT COUNT(*) FROM users", "problems": "SELECT COUNT(*) FROM problems", "contests": "SELECT COUNT(*) FROM contests", "submissions": "SELECT COUNT(*) FROM submissions", "pending_tasks": "SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')", "pending_outbox": "SELECT COUNT(*) FROM judge_task_outbox WHERE dispatched_at IS NULL", "dead_letters": "SELECT COUNT(*) FROM judge_dead_letters", "quarantined": "SELECT COUNT(*) FROM judge_message_quarantine"}
	for key, q := range queries {
		var n int64
		if s.db.GetContext(ctx, &n, q) != nil {
			return nil, ErrUnavailable
		}
		v[key] = n
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for key, url := range map[string]string{"worker_ready": "http://worker:8081/ready", "dispatcher_ready": "http://dispatcher:8082/ready"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		r, e := client.Do(req)
		ok := e == nil && r.StatusCode == 200
		if r != nil {
			r.Body.Close()
		}
		v[key] = ok
	}
	v["runner_via_worker_ready"] = v["worker_ready"]
	v["redis_ready"] = s.redis.Ping(ctx).Err() == nil
	return v, nil
}

type Quarantine struct {
	Digest   string    `db:"payload_digest" json:"payload_digest"`
	StreamID string    `db:"stream_id" json:"stream_id"`
	Reason   string    `db:"reason" json:"reason"`
	Created  time.Time `db:"created_at" json:"created_at"`
}

func (s *Service) Quarantine(ctx context.Context, a auth.User, after uint64) ([]Quarantine, error) {
	if e := s.authorized(ctx, a); e != nil {
		return nil, e
	}
	rows := []Quarantine{}
	if after > 100000 {
		return nil, ErrInvalid
	}
	if s.db.SelectContext(ctx, &rows, `SELECT stream_id,payload_digest,reason,created_at FROM judge_message_quarantine ORDER BY created_at,stream_id,payload_digest LIMIT 100 OFFSET ?`, after) != nil {
		return nil, ErrUnavailable
	}
	return rows, nil
}
