package judge

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"biue/internal/testcase"

	"github.com/jmoiron/sqlx"
)

var ErrExecutionStore = errors.New("judge execution storage failed")

type DatasetReader interface {
	Read(context.Context, string) (testcase.Dataset, error)
}

type ExecutionStore struct {
	leases *LeaseStore
	data   DatasetReader
}

func NewExecutionStore(leases *LeaseStore, data DatasetReader) (*ExecutionStore, error) {
	if leases == nil || data == nil {
		return nil, ErrExecutionStore
	}
	return &ExecutionStore{leases: leases, data: data}, nil
}

const requestQuery = `SELECT s.kind, s.language, s.source_code, s.stdin, s.total_cases, pv.snapshot_json, COALESCE(dv.storage_key, '') AS storage_key, COALESCE(dv.digest, '') AS digest, COALESCE(dv.case_count, 0) AS case_count, COALESCE(dv.total_bytes, 0) AS total_bytes, COALESCE(dv.total_score, 0) AS total_score FROM submissions s JOIN problem_versions pv ON pv.problem_id = s.problem_id AND pv.version = s.statement_version LEFT JOIN problem_data_versions dv ON dv.problem_id = s.problem_id AND dv.data_version = s.data_version JOIN judge_tasks t ON t.submission_id = s.id AND t.generation = s.generation WHERE t.id = ? AND s.id = ? AND s.generation = ? AND t.attempt = ? AND t.lease_token = ? AND t.lease_expires_at > UTC_TIMESTAMP(6) AND t.status = s.status AND t.status IN ('Queued','Compiling','Running')`

// LoadRequest follows only the immutable version references pinned at creation.
// Reading files does not hold SQL locks; every later mutation rechecks the lease.
func (s *ExecutionStore) LoadRequest(ctx context.Context, lease Lease) (Request, error) {
	if !validLease(lease) {
		return Request{}, ErrLeaseLost
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var row struct {
		Kind       Kind   `db:"kind"`
		Language   string `db:"language"`
		Source     string `db:"source_code"`
		Stdin      []byte `db:"stdin"`
		TotalCases int    `db:"total_cases"`
		Snapshot   []byte `db:"snapshot_json"`
		StorageKey string `db:"storage_key"`
		Digest     string `db:"digest"`
		CaseCount  int    `db:"case_count"`
		TotalBytes int64  `db:"total_bytes"`
		TotalScore int64  `db:"total_score"`
	}
	err := s.leases.db.GetContext(ctx, &row, requestQuery, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, lease.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrLeaseLost
	}
	if err != nil {
		return Request{}, ErrExecutionStore
	}
	var limits struct {
		Time   int64 `json:"time_limit_ms"`
		Memory int64 `json:"memory_limit_mb"`
	}
	if len(row.Snapshot) > 256<<10 || json.Unmarshal(row.Snapshot, &limits) != nil {
		return Request{}, ErrExecutionStore
	}
	r := Request{Task: lease.Task, Kind: row.Kind, Language: row.Language, SourceCode: row.Source, TimeLimitMS: limits.Time, MemoryLimitMB: limits.Memory}
	if row.Kind == Run {
		if row.TotalCases != 1 {
			return Request{}, ErrExecutionStore
		}
		r.Stdin = row.Stdin
	} else if row.Kind == Submit {
		data, err := s.data.Read(ctx, row.StorageKey)
		if err != nil || hex.EncodeToString(data.Digest[:]) != row.Digest || len(data.Cases) != row.CaseCount || row.TotalCases != row.CaseCount || data.TotalBytes != row.TotalBytes || data.TotalScore != row.TotalScore {
			return Request{}, ErrRequestData
		}
		for _, c := range data.Cases {
			r.Cases = append(r.Cases, Case{ID: c.ID, Input: c.Input, Expected: c.Output, Score: c.Score})
		}
	}
	if r.Validate() != nil {
		return Request{}, ErrExecutionStore
	}
	return r, nil
}

func validLease(lease Lease) bool {
	decoded, err := hex.DecodeString(lease.Token)
	return lease.Task.Validate() == nil && err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == lease.Token
}

// lockOwned uses the same submission→task lock order as Claim. A boolean SQL
// expression checks token and expiry without returning the token to the caller.
func (s *ExecutionStore) lockOwned(ctx context.Context, tx *sqlx.Tx, lease Lease) (Status, error) {
	var submission struct {
		Generation uint64 `db:"generation"`
		Status     Status `db:"status"`
	}
	err := tx.GetContext(ctx, &submission, `SELECT generation, status FROM submissions WHERE id = ? FOR UPDATE`, lease.Task.SubmissionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", ErrExecutionStore
	}
	if submission.Generation != lease.Task.Generation || Terminal(submission.Status) {
		return "", ErrLeaseLost
	}
	var task struct {
		Status Status `db:"status"`
		Owned  bool   `db:"owned"`
	}
	err = tx.GetContext(ctx, &task, `SELECT status, (attempt = ? AND lease_token = ? AND lease_expires_at > UTC_TIMESTAMP(6)) AS owned FROM judge_tasks WHERE id = ? AND submission_id = ? AND generation = ? FOR UPDATE`, lease.Task.Attempt, lease.Token, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", ErrExecutionStore
	}
	if !task.Owned || Terminal(task.Status) {
		return "", ErrLeaseLost
	}
	if !ValidStatus(task.Status) || task.Status != submission.Status {
		return "", ErrExecutionStore
	}
	return task.Status, nil
}

// Advance permits replaying the current phase but cannot skip or reverse phases.
func (s *ExecutionStore) Advance(ctx context.Context, lease Lease, to Status) error {
	if !validLease(lease) {
		return ErrLeaseLost
	}
	if to != Compiling && to != Running {
		return ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.leases.db.BeginTxx(ctx, nil)
	if err != nil {
		return ErrExecutionStore
	}
	defer tx.Rollback()
	from, err := s.lockOwned(ctx, tx, lease)
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	if !CanTransition(from, to) {
		return ErrInvalidRequest
	}
	if err := s.updateState(ctx, tx, lease, from, to, false); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrExecutionStore
	}
	return nil
}

func (s *ExecutionStore) updateState(ctx context.Context, tx *sqlx.Tx, lease Lease, from, to Status, terminal bool) error {
	query := `UPDATE judge_tasks SET status = ?, updated_at = UTC_TIMESTAMP(6)`
	if terminal {
		query += `, lease_token = NULL, lease_expires_at = NULL`
	}
	query += ` WHERE id = ? AND submission_id = ? AND generation = ? AND attempt = ? AND lease_token = ? AND lease_expires_at > UTC_TIMESTAMP(6) AND status = ?`
	result, err := tx.ExecContext(ctx, query, to, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, lease.Token, from)
	if err != nil {
		return ErrExecutionStore
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ErrExecutionStore
	}
	if count != 1 {
		return ErrLeaseLost
	}
	if !terminal {
		result, err = tx.ExecContext(ctx, `UPDATE submissions SET status = ?, updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = ?`, to, lease.Task.SubmissionID, lease.Task.Generation, from)
		if err != nil {
			return ErrExecutionStore
		}
		count, err = result.RowsAffected()
		if err != nil {
			return ErrExecutionStore
		}
		if count != 1 {
			return ErrLeaseLost
		}
	}
	return nil
}

// Finish re-loads the trusted request before validating a runner result, then
// fences and atomically saves its history, cases and current summary. Unknown
// commit outcomes remain unacknowledged and are resolved through stored history.
func (s *ExecutionStore) Finish(ctx context.Context, lease Lease, result Result) error {
	// Infrastructure failures must consume the retry budget, never bypass it.
	if result.Status == SystemError {
		return ErrInvalidResult
	}
	request, err := s.LoadRequest(ctx, lease)
	if err != nil {
		return err
	}
	if ValidateResult(request, result) != nil {
		return ErrInvalidResult
	}
	if request.Kind == Submit {
		result.Output = ""
		if result.Status != CompileError {
			result.ErrorMessage = ""
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.leases.db.BeginTxx(ctx, nil)
	if err != nil {
		return ErrExecutionStore
	}
	defer tx.Rollback()
	from, err := s.lockOwned(ctx, tx, lease)
	if err != nil {
		return err
	}
	if !CanTransition(from, result.Status) {
		return ErrInvalidResult
	}
	if err := s.updateState(ctx, tx, lease, from, result.Status, true); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO submission_results (submission_id, generation, attempt, status, score, execution_time_ms, memory_usage_kb, passed_cases, total_cases, output, error_message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, result.Status, result.Score, result.ExecutionTimeMS, result.MemoryUsageKB, result.PassedCases, result.TotalCases, result.Output, result.ErrorMessage)
	if err != nil {
		return ErrExecutionStore
	}
	for _, c := range result.Cases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_case_results (submission_id, generation, case_id, status, score, execution_time_ms, memory_usage_kb) VALUES (?, ?, ?, ?, ?, ?, ?)`, lease.Task.SubmissionID, lease.Task.Generation, c.ID, c.Status, c.Score, c.ExecutionTimeMS, c.MemoryUsageKB); err != nil {
			return ErrExecutionStore
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE submissions SET status = ?, score = ?, execution_time_ms = ?, memory_usage_kb = ?, passed_cases = ?, total_cases = ?, output = ?, error_message = ?, updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND generation = ? AND status = ?`, result.Status, result.Score, result.ExecutionTimeMS, result.MemoryUsageKB, result.PassedCases, result.TotalCases, result.Output, result.ErrorMessage, lease.Task.SubmissionID, lease.Task.Generation, from)
	if err != nil {
		return ErrExecutionStore
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return ErrExecutionStore
	}
	if count != 1 {
		return ErrLeaseLost
	}
	if tx.Commit() != nil {
		return ErrExecutionStore
	}
	return nil
}

// AcknowledgeCompleted requires durable terminal
// history for this exact task/generation/attempt, including after a lost commit
// response or Redis failure. It does not require the old lease to remain live.
func (s *ExecutionStore) AcknowledgeCompleted(ctx context.Context, queue *Queue, d Delivery) error {
	if queue == nil || d.Invalid || d.ID == "" || d.Task.Validate() != nil {
		return ErrInvalidEnvelope
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var status Status
	err := s.leases.db.GetContext(ctx, &status, `SELECT r.status FROM submission_results r JOIN judge_tasks t ON t.submission_id = r.submission_id AND t.generation = r.generation WHERE t.id = ? AND r.submission_id = ? AND r.generation = ? AND r.attempt = ?`, d.Task.TaskID, d.Task.SubmissionID, d.Task.Generation, d.Task.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return ErrExecutionStore
	}
	if !Terminal(status) {
		return ErrExecutionStore
	}
	return acknowledgeDelivery(ctx, queue, d)
}

func acknowledgeDelivery(ctx context.Context, queue *Queue, d Delivery) error {
	// Bind the requested ACK ID to its actual immutable stream payload too.
	messages, err := queue.client.XRangeN(ctx, Stream, d.ID, d.ID, 1).Result()
	if err != nil {
		return ErrQueue
	}
	if len(messages) > 0 {
		actual := delivery(messages[0])
		if actual.Invalid || actual.Task != d.Task {
			return ErrInvalidEnvelope
		}
	}
	if queue.client.XAck(ctx, Stream, ConsumerGroup, d.ID).Err() != nil {
		return ErrQueue
	}
	return nil
}
