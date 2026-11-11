package judge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type RejudgeItem struct {
	ID         uint64 `json:"id"`
	Generation uint64 `json:"generation"`
}
type RejudgeInput struct {
	RequestID        string        `json:"-"`
	Key              string        `json:"key"`
	Reason           string        `json:"reason"`
	Items            []RejudgeItem `json:"items"`
	UseLatestProblem bool          `json:"use_latest_problem,omitempty"`
}

var requestKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// Rejudge appends generations and outbox intents atomically. An idempotency key
// replays the original envelopes even if those tasks have already finished.
func (a *JudgeAdmin) Rejudge(ctx context.Context, actor uint64, in RejudgeInput) ([]TaskEnvelope, error) {
	if actor == 0 || !requestKey.MatchString(in.Key) || len(in.Items) < 1 || len(in.Items) > 100 || strings.TrimSpace(in.Reason) == "" || !utf8.ValidString(in.Reason) || len(in.Reason) > 1024 {
		return nil, ErrAdminConflict
	}
	in.Items = append([]RejudgeItem(nil), in.Items...)
	sort.Slice(in.Items, func(i, j int) bool { return in.Items[i].ID < in.Items[j].ID })
	for i, item := range in.Items {
		if item.ID == 0 || item.Generation == 0 || item.Generation == math.MaxUint64 || i > 0 && in.Items[i-1].ID == item.ID {
			return nil, ErrAdminConflict
		}
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return nil, ErrAdminConflict
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, e := a.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, ErrExecutionStore
	}
	defer tx.Rollback()
	var admission int
	if tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`) != nil {
		return nil, ErrExecutionStore
	}
	var user struct {
		Role   string `db:"global_role"`
		Type   string `db:"account_type"`
		Status string `db:"status"`
	}
	if tx.GetContext(ctx, &user, `SELECT global_role,account_type,status FROM users WHERE id=? FOR UPDATE`, actor) != nil {
		return nil, ErrAdminForbidden
	}
	if user.Role != "super_admin" || user.Type != "permanent" || user.Status != "active" {
		return nil, ErrAdminForbidden
	}
	var saved struct {
		Digest string `db:"request_digest"`
		Tasks  []byte `db:"tasks"`
	}
	e = tx.GetContext(ctx, &saved, `SELECT request_digest,tasks FROM rejudge_requests WHERE actor_id=? AND request_key=? FOR UPDATE`, actor, in.Key)
	if e == nil {
		var tasks []TaskEnvelope
		if saved.Digest != digest {
			return nil, ErrAdminConflict
		}
		if json.Unmarshal(saved.Tasks, &tasks) != nil {
			return nil, ErrExecutionStore
		}
		return tasks, nil
	}
	if e != sql.ErrNoRows {
		return nil, ErrExecutionStore
	}
	var pending int
	if tx.GetContext(ctx, &pending, `SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`) != nil {
		return nil, ErrExecutionStore
	}
	if pending+len(in.Items) > a.capacity {
		return nil, ErrAdminCapacity
	}
	tasks := make([]TaskEnvelope, 0, len(in.Items))
	for _, item := range in.Items {
		var current struct {
			ProblemID        uint64  `db:"problem_id"`
			StatementVersion uint64  `db:"statement_version"`
			DataVersion      *uint64 `db:"data_version"`
			Generation       uint64  `db:"generation"`
			Status           Status  `db:"status"`
			Contest          *uint64 `db:"contest_id"`
		}
		if tx.GetContext(ctx, &current, `SELECT problem_id,statement_version,data_version,generation,status,contest_id FROM submissions WHERE id=? FOR UPDATE`, item.ID) != nil {
			return nil, ErrAdminConflict
		}
		if current.Generation != item.Generation || !Terminal(current.Status) {
			return nil, ErrAdminConflict
		}
		if current.Contest != nil {
			var settled *time.Time
			if tx.GetContext(ctx, &settled, `SELECT settled_at FROM contests WHERE id=? FOR UPDATE`, *current.Contest) != nil {
				return nil, ErrExecutionStore
			}
			if settled != nil {
				return nil, ErrAdminConflict
			}
		}
		statementVersion, dataVersion := current.StatementVersion, current.DataVersion
		if in.UseLatestProblem && current.Contest == nil {
			var latest struct {
				StatementVersion uint64  `db:"statement_version"`
				DataVersion      *uint64 `db:"data_version"`
			}
			if tx.GetContext(ctx, &latest, `SELECT statement_version,data_version FROM problems WHERE id=? FOR SHARE`, current.ProblemID) != nil ||
				latest.StatementVersion == 0 || latest.DataVersion == nil {
				return nil, ErrAdminConflict
			}
			statementVersion, dataVersion = latest.StatementVersion, latest.DataVersion
		}
		generation := current.Generation + 1
		r, e := tx.ExecContext(ctx, `INSERT INTO judge_tasks(submission_id,generation,attempt,status,error_message,created_at,updated_at) VALUES (?,?,1,'Pending','',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, item.ID, generation)
		if e != nil {
			return nil, ErrExecutionStore
		}
		id, e := r.LastInsertId()
		if e != nil || id < 1 {
			return nil, ErrExecutionStore
		}
		updateSQL := `UPDATE submissions SET generation=?,statement_version=?,data_version=?,status='Pending',score=0,execution_time_ms=0,memory_usage_kb=0,passed_cases=0,output='',error_message='',updated_at=UTC_TIMESTAMP(6) WHERE id=? AND generation=?`
		updateArgs := []any{generation, statementVersion, dataVersion, item.ID, item.Generation}
		if !in.UseLatestProblem || current.Contest != nil {
			updateSQL = `UPDATE submissions SET generation=?,status='Pending',score=0,execution_time_ms=0,memory_usage_kb=0,passed_cases=0,output='',error_message='',updated_at=UTC_TIMESTAMP(6) WHERE id=? AND generation=?`
			updateArgs = []any{generation, item.ID, item.Generation}
		}
		r, e = tx.ExecContext(ctx, updateSQL, updateArgs...)
		if e != nil {
			return nil, ErrExecutionStore
		}
		n, e := r.RowsAffected()
		if e != nil || n != 1 {
			return nil, ErrAdminConflict
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO judge_task_outbox(task_id,submission_id,generation,attempt,created_at) VALUES (?,?,?,1,UTC_TIMESTAMP(6))`, id, item.ID, generation); e != nil {
			return nil, ErrExecutionStore
		}
		tasks = append(tasks, TaskEnvelope{TaskID: uint64(id), SubmissionID: item.ID, Generation: generation, Attempt: 1})
	}
	records, e := json.Marshal(tasks)
	if e != nil {
		return nil, ErrExecutionStore
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO rejudge_requests(actor_id,request_key,request_digest,tasks,created_at) VALUES (?,?,?,?,UTC_TIMESTAMP(6))`, actor, in.Key, digest, string(records)); e != nil {
		return nil, ErrExecutionStore
	}
	details, _ := json.Marshal(map[string]any{"key": in.Key, "reason": in.Reason, "items": in.Items})
	if in.RequestID == "" {
		in.RequestID = "ojctl"
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,'judge.rejudge','system',0,?,?,UTC_TIMESTAMP(6))`, actor, in.RequestID, string(details)); e != nil {
		return nil, ErrExecutionStore
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM global_board_cache`); e != nil {
		return nil, ErrExecutionStore
	}
	if tx.Commit() != nil {
		return nil, ErrExecutionStore
	}
	return tasks, nil
}
