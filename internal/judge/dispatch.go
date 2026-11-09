package judge

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

const Stream = "oj:judge"
const ConsumerGroup = "judgers"
const DeadStream = "oj:judge:dead"

var ErrDispatch = errors.New("judge dispatch failed")

// Dispatcher delivers committed outbox intents at least once. Consumers must
// deduplicate by task/generation/attempt; a Redis success followed by a database
// failure deliberately leaves the intent available for another delivery.
type Dispatcher struct {
	db                 *sqlx.DB
	redis              *redis.Client
	reconcileMu        sync.Mutex
	cursor, deadCursor uint64
}

func NewDispatcher(db *sqlx.DB, client *redis.Client) (*Dispatcher, error) {
	if db == nil || client == nil {
		return nil, errors.New("dispatcher requires MySQL and Redis")
	}
	return &Dispatcher{db: db, redis: client}, nil
}

// DispatchOne locks one intent, allowing independent processes to skip it.
// Network I/O stays inside a bounded transaction so no second dispatcher can
// mark the same intent concurrently. No task state is advanced here: the
// consumer will claim the task using its generation, attempt and lease CAS.
func (d *Dispatcher) DispatchOne(ctx context.Context) (bool, error) {
	return d.dispatch(ctx, false)
}

// DispatchDeadOne delivers durable dead-letter records at least once. The
// dead_letter_id field lets later tooling deduplicate identical notifications.
func (d *Dispatcher) DispatchDeadOne(ctx context.Context) (bool, error) {
	return d.dispatch(ctx, true)
}

func (d *Dispatcher) dispatch(ctx context.Context, dead bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, ErrDispatch
	}
	defer tx.Rollback()
	var row struct {
		ID           uint64        `db:"id"`
		TaskID       uint64        `db:"task_id"`
		SubmissionID uint64        `db:"submission_id"`
		Generation   uint64        `db:"generation"`
		Attempt      uint32        `db:"attempt"`
		Reason       FailureReason `db:"reason"`
	}
	// All table/stream choices are internal constants, never caller input.
	table, stream := "judge_task_outbox", Stream
	query := `SELECT id, task_id, submission_id, generation, attempt FROM judge_task_outbox WHERE dispatched_at IS NULL AND available_at <= UTC_TIMESTAMP(6) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`
	if dead {
		table, stream = "judge_dead_letters", DeadStream
		query = `SELECT id, task_id, submission_id, generation, attempt, reason FROM judge_dead_letters WHERE dispatched_at IS NULL ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`
	}
	err = tx.GetContext(ctx, &row, query)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrDispatch
	}
	envelope := TaskEnvelope{TaskID: row.TaskID, SubmissionID: row.SubmissionID, Generation: row.Generation, Attempt: row.Attempt}
	if row.ID == 0 || envelope.Validate() != nil || dead && (envelope.Attempt != MaxAttempts || !validFailure(row.Reason)) {
		return false, ErrDispatch
	}
	if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET dispatch_attempts = dispatch_attempts + 1 WHERE id = ?`, row.ID); err != nil {
		return false, ErrDispatch
	}
	values := envelope.StreamValues()
	if dead {
		values["reason"] = string(row.Reason)
		values["dead_letter_id"] = strconv.FormatUint(row.ID, 10)
	}
	id, sendErr := d.redis.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
	if sendErr != nil {
		// Preserve the failed attempt counter while keeping the intent pending.
		if err := tx.Commit(); err != nil {
			return false, ErrDispatch
		}
		return false, ErrDispatch
	}
	if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET dispatched_at = ?, stream_id = ? WHERE id = ? AND dispatched_at IS NULL`, time.Now().UTC(), id, row.ID); err != nil {
		return false, ErrDispatch
	}
	if err = tx.Commit(); err != nil {
		return false, ErrDispatch
	}
	return true, nil
}
