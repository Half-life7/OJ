package judge

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type FailureReason string

const (
	FailureRunnerUnavailable    FailureReason = "runner_unavailable"
	FailureRunnerProtocol       FailureReason = "runner_protocol"
	FailureRunnerSystem         FailureReason = "runner_system_error"
	FailureDataUnavailable      FailureReason = "data_unavailable"
	FailureExecutionInterrupted FailureReason = "execution_interrupted"
)

func validFailure(reason FailureReason) bool {
	switch reason {
	case FailureRunnerUnavailable, FailureRunnerProtocol, FailureRunnerSystem, FailureDataUnavailable, FailureExecutionInterrupted:
		return true
	default:
		return false
	}
}

type FailureDisposition string

const (
	FailureRetry FailureDisposition = "retry"
	FailureDead  FailureDisposition = "dead"
)

// Fail records an infrastructure failure under a live lease. Ordinary verdicts
// must use Finish. Retrying is the sole controlled backward state transition:
// the attempt increases and a fresh outbox intent commits with the reset state.
// Neither Redis availability nor a readable dataset is needed to record failure.
func (s *ExecutionStore) Fail(ctx context.Context, lease Lease, reason FailureReason) (FailureDisposition, error) {
	if !validLease(lease) {
		return "", ErrLeaseLost
	}
	if !validFailure(reason) {
		return "", ErrInvalidResult
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.leases.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", ErrExecutionStore
	}
	defer tx.Rollback()
	from, err := s.lockOwned(ctx, tx, lease)
	if err != nil {
		return "", err
	}
	if from != Queued && from != Compiling && from != Running {
		return "", ErrLeaseLost
	}
	disposition := FailureRetry
	if lease.Task.Attempt == MaxAttempts {
		disposition = FailureDead
	}
	if disposition == FailureRetry {
		updated, err := tx.ExecContext(ctx, `UPDATE judge_tasks SET attempt = attempt + 1, status = 'Pending', lease_token = NULL, lease_expires_at = NULL, error_message = '', updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND submission_id = ? AND generation = ? AND attempt = ? AND lease_token = ? AND lease_expires_at > UTC_TIMESTAMP(6) AND status = ?`, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, lease.Token, from)
		if err != nil {
			return "", ErrExecutionStore
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return "", ErrExecutionStore
		}
		if count != 1 {
			return "", ErrLeaseLost
		}
		updated, err = tx.ExecContext(ctx, `UPDATE submissions SET status = 'Pending', score = 0, execution_time_ms = 0, memory_usage_kb = 0, passed_cases = 0, output = '', error_message = '', updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = ?`, lease.Task.SubmissionID, lease.Task.Generation, from)
		if err != nil {
			return "", ErrExecutionStore
		}
		count, err = updated.RowsAffected()
		if err != nil {
			return "", ErrExecutionStore
		}
		if count != 1 {
			return "", ErrLeaseLost
		}
		// Initial attempt + at most three retries, delayed by 1, 2 and 4 seconds.
		delay := int64(1 << (lease.Task.Attempt - 1))
		if _, err := tx.ExecContext(ctx, `INSERT INTO judge_task_outbox (task_id, submission_id, generation, attempt, created_at, available_at) VALUES (?, ?, ?, ?, UTC_TIMESTAMP(6), TIMESTAMPADD(SECOND, ?, UTC_TIMESTAMP(6)))`, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt+1, delay); err != nil {
			return "", ErrExecutionStore
		}
	} else {
		if err := s.updateState(ctx, tx, lease, from, SystemError, true); err != nil {
			return "", err
		}
		var total int
		if err := tx.GetContext(ctx, &total, `SELECT total_cases FROM submissions WHERE id = ? AND generation = ?`, lease.Task.SubmissionID, lease.Task.Generation); err != nil || total < 1 || total > 512 {
			return "", ErrExecutionStore
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_results (submission_id, generation, attempt, status, score, execution_time_ms, memory_usage_kb, passed_cases, total_cases, output, error_message, created_at) VALUES (?, ?, ?, 'SystemError', 0, 0, 0, 0, ?, '', '', UTC_TIMESTAMP(6))`, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, total); err != nil {
			return "", ErrExecutionStore
		}
		updated, err := tx.ExecContext(ctx, `UPDATE submissions SET status = 'SystemError', score = 0, execution_time_ms = 0, memory_usage_kb = 0, passed_cases = 0, output = '', error_message = '', updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = ?`, lease.Task.SubmissionID, lease.Task.Generation, from)
		if err != nil {
			return "", ErrExecutionStore
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return "", ErrExecutionStore
		}
		if count != 1 {
			return "", ErrLeaseLost
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO judge_attempt_failures (task_id, submission_id, generation, attempt, reason, disposition, created_at) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, reason, disposition); err != nil {
		return "", ErrExecutionStore
	}
	if disposition == FailureDead {
		if _, err := tx.ExecContext(ctx, `INSERT INTO judge_dead_letters (task_id, submission_id, generation, attempt, reason, created_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, reason); err != nil {
			return "", ErrExecutionStore
		}
	}
	if tx.Commit() != nil {
		return "", ErrExecutionStore
	}
	return disposition, nil
}

// AcknowledgeFailed proves this attempt's committed retry/dead decision before
// acknowledging it. This also resolves a lost commit reply or failed Redis ACK.
func (s *ExecutionStore) AcknowledgeFailed(ctx context.Context, queue *Queue, d Delivery) error {
	if queue == nil || d.Invalid || d.ID == "" || d.Task.Validate() != nil {
		return ErrInvalidEnvelope
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var disposition FailureDisposition
	err := s.leases.db.GetContext(ctx, &disposition, `SELECT disposition FROM judge_attempt_failures WHERE task_id = ? AND submission_id = ? AND generation = ? AND attempt = ?`, d.Task.TaskID, d.Task.SubmissionID, d.Task.Generation, d.Task.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return ErrExecutionStore
	}
	if disposition != FailureRetry && disposition != FailureDead {
		return ErrExecutionStore
	}
	if disposition == FailureRetry && d.Task.Attempt >= MaxAttempts || disposition == FailureDead && d.Task.Attempt != MaxAttempts {
		return ErrExecutionStore
	}
	return acknowledgeDelivery(ctx, queue, d)
}
