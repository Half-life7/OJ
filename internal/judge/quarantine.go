package judge

import (
	"context"
	"time"
)

// Quarantine retains only an immutable digest and a controlled reason, never
// raw queue fields. Retrying an unknown commit is idempotent.
func (s *ExecutionStore) Quarantine(ctx context.Context, q *Queue, d Delivery) error {
	if !d.Invalid {
		return ErrInvalidEnvelope
	}
	return s.quarantine(ctx, q, d, "invalid_envelope")
}

// QuarantineUnresolved is called only after both durable result/failure proofs
// were absent. Recheck that this is not a current live task before isolating it.
func (s *ExecutionStore) QuarantineUnresolved(ctx context.Context, q *Queue, d Delivery) error {
	if d.Invalid || d.Task.Validate() != nil {
		return ErrInvalidEnvelope
	}
	var active bool
	err := s.leases.db.GetContext(ctx, &active, `SELECT EXISTS(SELECT 1 FROM judge_tasks t JOIN submissions s ON s.id = t.submission_id AND s.generation = t.generation WHERE t.id = ? AND t.submission_id = ? AND t.generation = ? AND t.attempt = ? AND t.status IN ('Pending','Queued','Compiling','Running'))`, d.Task.TaskID, d.Task.SubmissionID, d.Task.Generation, d.Task.Attempt)
	if err != nil {
		return ErrExecutionStore
	}
	if active {
		return ErrLeaseLost
	}
	return s.quarantine(ctx, q, d, "unresolvable_envelope")
}

func (s *ExecutionStore) quarantine(ctx context.Context, q *Queue, d Delivery, reason string) error {
	if q == nil || d.ID == "" || len(d.Fingerprint) != 64 {
		return ErrInvalidEnvelope
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	messages, err := q.client.XRangeN(ctx, Stream, d.ID, d.ID, 1).Result()
	if err != nil {
		return ErrQueue
	}
	if len(messages) != 1 {
		return ErrInvalidEnvelope
	}
	actual := delivery(messages[0])
	if actual.Invalid != d.Invalid || actual.Fingerprint != d.Fingerprint {
		return ErrInvalidEnvelope
	}
	if _, err := s.leases.db.ExecContext(ctx, `INSERT INTO judge_message_quarantine (stream_id, payload_digest, reason, created_at) VALUES (?, ?, ?, UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE reason = reason`, d.ID, d.Fingerprint, reason); err != nil {
		return ErrExecutionStore
	}
	if q.client.XAck(ctx, Stream, ConsumerGroup, d.ID).Err() != nil {
		return ErrQueue
	}
	return nil
}
