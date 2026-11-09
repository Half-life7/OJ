package judge

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// ReconcileOne scans old published intents and reopens only those whose recorded
// message is missing or no longer matches. MySQL remains authoritative after a
// Redis data loss; replaying a concurrently completed task is safe for consumers.
func (d *Dispatcher) ReconcileOne(ctx context.Context, dead bool) (bool, error) {
	d.reconcileMu.Lock()
	defer d.reconcileMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cursor := &d.cursor
	stream, table := Stream, "judge_task_outbox"
	query := `SELECT o.id, o.stream_id, o.task_id, o.submission_id, o.generation, o.attempt FROM judge_task_outbox o JOIN judge_tasks t ON t.id = o.task_id AND t.attempt = o.attempt AND t.generation = o.generation JOIN submissions s ON s.id = t.submission_id AND s.generation = t.generation WHERE o.id > ? AND o.dispatched_at < TIMESTAMPADD(SECOND, -60, UTC_TIMESTAMP(6)) AND t.status IN ('Pending','Queued','Compiling','Running') AND s.status = t.status ORDER BY o.id LIMIT 1`
	if dead {
		cursor = &d.deadCursor
		stream, table = DeadStream, "judge_dead_letters"
		query = `SELECT id, stream_id, task_id, submission_id, generation, attempt, reason FROM judge_dead_letters WHERE id > ? AND dispatched_at < TIMESTAMPADD(SECOND, -60, UTC_TIMESTAMP(6)) ORDER BY id LIMIT 1`
	}
	var row struct {
		ID           uint64        `db:"id"`
		StreamID     string        `db:"stream_id"`
		TaskID       uint64        `db:"task_id"`
		SubmissionID uint64        `db:"submission_id"`
		Generation   uint64        `db:"generation"`
		Attempt      uint32        `db:"attempt"`
		Reason       FailureReason `db:"reason"`
	}
	err := d.db.GetContext(ctx, &row, query, *cursor)
	if errors.Is(err, sql.ErrNoRows) {
		*cursor = 0
		return false, nil
	}
	if err != nil {
		return false, ErrDispatch
	}
	*cursor = row.ID
	expected := TaskEnvelope{TaskID: row.TaskID, SubmissionID: row.SubmissionID, Generation: row.Generation, Attempt: row.Attempt}
	if expected.Validate() != nil {
		return false, ErrDispatch
	}
	messages, err := d.redis.XRangeN(ctx, stream, row.StreamID, row.StreamID, 1).Result()
	if err != nil {
		return false, ErrDispatch
	}
	present := false
	if len(messages) == 1 {
		values := messages[0].Values
		if dead {
			copy := map[string]any{}
			for k, v := range values {
				if k != "reason" && k != "dead_letter_id" {
					copy[k] = v
				}
			}
			parsed, err := ParseEnvelope(copy)
			present = err == nil && parsed == expected && len(values) == 6 && values["dead_letter_id"] == strconv.FormatUint(row.ID, 10) && values["reason"] == string(row.Reason)
		} else {
			parsed, err := ParseEnvelope(values)
			present = err == nil && parsed == expected
		}
	}
	if present {
		return true, nil
	}
	if _, err := d.db.ExecContext(ctx, `UPDATE `+table+` SET dispatched_at = NULL, stream_id = NULL WHERE id = ? AND stream_id = ? AND dispatched_at IS NOT NULL`, row.ID, row.StreamID); err != nil {
		return false, ErrDispatch
	}
	return true, nil
}
