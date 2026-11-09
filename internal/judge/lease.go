package judge

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

var (
	ErrLeaseStore = errors.New("judge lease operation failed")
	ErrLeaseLost  = errors.New("judge lease is no longer owned")
)

type ClaimState string

const (
	ClaimAcquired ClaimState = "acquired"
	ClaimBusy     ClaimState = "busy"
	ClaimObsolete ClaimState = "obsolete"
)

// Lease is an internal capability. Never expose its token through HTTP or logs.
type Lease struct {
	Task  TaskEnvelope
	Token string
}

type LeaseStore struct {
	db  *sqlx.DB
	ttl time.Duration
}

func NewLeaseStore(db *sqlx.DB, ttl time.Duration) (*LeaseStore, error) {
	if db == nil || ttl < 10*time.Second || ttl > 5*time.Minute {
		return nil, errors.New("lease store requires MySQL and a 10s to 5m lease")
	}
	return &LeaseStore{db: db, ttl: ttl}, nil
}

// Claim locks the submission before its task, and uses MySQL's clock for all
// lease decisions. Reclaiming an expired lease changes its token and retains
// its attempt and phase: delivery recovery is distinct from execution retry.
func (s *LeaseStore) Claim(ctx context.Context, envelope TaskEnvelope) (Lease, ClaimState, error) {
	if envelope.Validate() != nil {
		return Lease{}, "", ErrInvalidEnvelope
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	defer tx.Rollback()
	var submission struct {
		Generation uint64 `db:"generation"`
		Status     Status `db:"status"`
	}
	err = tx.GetContext(ctx, &submission, `SELECT generation, status FROM submissions WHERE id = ? FOR UPDATE`, envelope.SubmissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ClaimObsolete, nil
	}
	if err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	if submission.Generation != envelope.Generation || Terminal(submission.Status) {
		return Lease{}, ClaimObsolete, nil
	}
	var task struct {
		Attempt uint32 `db:"attempt"`
		Status  Status `db:"status"`
		Active  bool   `db:"active"`
	}
	err = tx.GetContext(ctx, &task, `SELECT attempt, status, (lease_token IS NOT NULL AND lease_expires_at > UTC_TIMESTAMP(6)) AS active FROM judge_tasks WHERE id = ? AND submission_id = ? AND generation = ? FOR UPDATE`, envelope.TaskID, envelope.SubmissionID, envelope.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ClaimObsolete, nil
	}
	if err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	if task.Attempt != envelope.Attempt || Terminal(task.Status) {
		return Lease{}, ClaimObsolete, nil
	}
	if !ValidStatus(task.Status) || task.Status != submission.Status {
		return Lease{}, "", ErrLeaseStore
	}
	if task.Active {
		return Lease{}, ClaimBusy, nil
	}
	lease := Lease{Task: envelope, Token: hex.EncodeToString(token[:])}
	if _, err := tx.ExecContext(ctx, `UPDATE judge_tasks SET lease_token = ?, lease_expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)), status = CASE WHEN status = 'Pending' THEN 'Queued' ELSE status END, updated_at = UTC_TIMESTAMP(6) WHERE id = ?`, lease.Token, s.ttl.Microseconds(), envelope.TaskID); err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	if task.Status == Pending {
		if _, err := tx.ExecContext(ctx, `UPDATE submissions SET status = 'Queued', updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = 'Pending'`, envelope.SubmissionID, envelope.Generation); err != nil {
			return Lease{}, "", ErrLeaseStore
		}
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, "", ErrLeaseStore
	}
	return lease, ClaimAcquired, nil
}

// Renew cannot resurrect an expired lease. A changed token, generation, attempt,
// or terminal task fences out the old owner even if it still owns a Redis ID.
func (s *LeaseStore) Renew(ctx context.Context, lease Lease) error {
	decoded, err := hex.DecodeString(lease.Token)
	if lease.Task.Validate() != nil || err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != lease.Token {
		return ErrLeaseLost
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(ctx, `UPDATE judge_tasks SET lease_expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)), updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND submission_id = ? AND generation = ? AND attempt = ? AND lease_token = ? AND lease_expires_at > UTC_TIMESTAMP(6) AND status IN ('Queued','Compiling','Running')`, s.ttl.Microseconds(), lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, lease.Token)
	if err != nil {
		return ErrLeaseStore
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ErrLeaseStore
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}
