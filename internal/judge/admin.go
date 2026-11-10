package judge

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
)

var ErrAdminForbidden = errors.New("active super administrator required")
var ErrAdminConflict = errors.New("dead letter is not the current terminal generation")
var ErrAdminCapacity = errors.New("judge queue capacity reached")

type JudgeAdmin struct {
	db       *sqlx.DB
	capacity int
}
type DeadLetter struct {
	ID           uint64        `db:"id" json:"id"`
	TaskID       uint64        `db:"task_id" json:"task_id"`
	SubmissionID uint64        `db:"submission_id" json:"submission_id"`
	Generation   uint64        `db:"generation" json:"generation"`
	Attempt      uint32        `db:"attempt" json:"attempt"`
	Reason       FailureReason `db:"reason" json:"reason"`
	CreatedAt    time.Time     `db:"created_at" json:"created_at"`
}

func NewJudgeAdmin(db *sqlx.DB, capacity int) (*JudgeAdmin, error) {
	if db == nil || capacity < 1 || capacity > 100000 {
		return nil, ErrExecutionStore
	}
	return &JudgeAdmin{db: db, capacity: capacity}, nil
}
func (a *JudgeAdmin) ListDead(ctx context.Context, actor uint64, limit int) ([]DeadLetter, error) {
	if actor == 0 || limit < 1 || limit > 100 {
		return nil, ErrAdminForbidden
	}
	var allowed bool
	if a.db.GetContext(ctx, &allowed, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND global_role = 'super_admin' AND account_type = 'permanent' AND status = 'active')`, actor) != nil {
		return nil, ErrExecutionStore
	}
	if !allowed {
		return nil, ErrAdminForbidden
	}
	items := []DeadLetter{}
	if a.db.SelectContext(ctx, &items, `SELECT id, task_id, submission_id, generation, attempt, reason, created_at FROM judge_dead_letters ORDER BY id DESC LIMIT ?`, limit) != nil {
		return nil, ErrExecutionStore
	}
	return items, nil
}
func (a *JudgeAdmin) RetryDead(ctx context.Context, actor, id, generation uint64) (TaskEnvelope, error) {
	if actor == 0 || id == 0 || generation == 0 || generation == math.MaxUint64 {
		return TaskEnvelope{}, ErrAdminConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	defer tx.Rollback()
	var admission int
	if tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id = 1 FOR UPDATE`) != nil || admission != 1 {
		return TaskEnvelope{}, ErrExecutionStore
	}
	var account struct {
		Role   string `db:"global_role"`
		Kind   string `db:"account_type"`
		Status string `db:"status"`
	}
	if tx.GetContext(ctx, &account, `SELECT global_role, account_type, status FROM users WHERE id = ? FOR UPDATE`, actor) != nil {
		return TaskEnvelope{}, ErrAdminForbidden
	}
	if account.Role != "super_admin" || account.Kind != "permanent" || account.Status != "active" {
		return TaskEnvelope{}, ErrAdminForbidden
	}
	var count int
	if tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`) != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	if count >= a.capacity {
		return TaskEnvelope{}, ErrAdminCapacity
	}
	var dead DeadLetter
	if tx.GetContext(ctx, &dead, `SELECT id, task_id, submission_id, generation, attempt, reason, created_at FROM judge_dead_letters WHERE id = ?`, id) != nil {
		return TaskEnvelope{}, ErrAdminConflict
	}
	var current struct {
		Generation uint64 `db:"generation"`
		Status     Status `db:"status"`
	}
	if tx.GetContext(ctx, &current, `SELECT generation, status FROM submissions WHERE id = ? AND (contest_id IS NULL OR NOT EXISTS (SELECT 1 FROM contests WHERE contests.id=submissions.contest_id AND settled_at IS NOT NULL)) FOR UPDATE`, dead.SubmissionID) != nil {
		return TaskEnvelope{}, ErrAdminConflict
	}
	if current.Generation != generation || dead.Generation != generation || current.Status != SystemError || dead.Attempt != MaxAttempts {
		return TaskEnvelope{}, ErrAdminConflict
	}
	var task Status
	if tx.GetContext(ctx, &task, `SELECT status FROM judge_tasks WHERE id = ? AND submission_id = ? AND generation = ? AND attempt = 4 FOR UPDATE`, dead.TaskID, dead.SubmissionID, generation) != nil || task != SystemError {
		return TaskEnvelope{}, ErrAdminConflict
	}
	newGeneration := generation + 1
	insert, err := tx.ExecContext(ctx, `INSERT INTO judge_tasks (submission_id, generation, attempt, status, error_message, created_at, updated_at) VALUES (?, ?, 1, 'Pending', '', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`, dead.SubmissionID, newGeneration)
	if err != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	taskID, err := insert.LastInsertId()
	if err != nil || taskID < 1 {
		return TaskEnvelope{}, ErrExecutionStore
	}
	updated, err := tx.ExecContext(ctx, `UPDATE submissions SET generation = ?, status = 'Pending', score = 0, execution_time_ms = 0, memory_usage_kb = 0, passed_cases = 0, output = '', error_message = '', updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = 'SystemError'`, newGeneration, dead.SubmissionID, generation)
	if err != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	changed, err := updated.RowsAffected()
	if err != nil || changed != 1 {
		return TaskEnvelope{}, ErrAdminConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO judge_task_outbox (task_id, submission_id, generation, attempt, created_at) VALUES (?, ?, ?, 1, UTC_TIMESTAMP(6))`, taskID, dead.SubmissionID, newGeneration); err != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	details, _ := json.Marshal(map[string]uint64{"dead_letter_id": id, "previous_generation": generation})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_user_id, action, object_type, object_id, request_id, details, created_at) VALUES (?, 'judge.retry', 'submission', ?, 'ojctl', ?, UTC_TIMESTAMP(6))`, actor, dead.SubmissionID, string(details)); err != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	if tx.Commit() != nil {
		return TaskEnvelope{}, ErrExecutionStore
	}
	return TaskEnvelope{TaskID: uint64(taskID), SubmissionID: dead.SubmissionID, Generation: newGeneration, Attempt: 1}, nil
}
