package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"biue/internal/auth"
	"biue/internal/judge"
	"biue/internal/server"
)

type ScopeRejudgeInput struct {
	Key       string `json:"key"`
	Reason    string `json:"reason"`
	ContestID uint64 `json:"contest_id,omitempty"`
	ProblemID uint64 `json:"problem_id,omitempty"`
}

type ScopeRejudgeResult struct {
	Queued   int                  `json:"queued"`
	Skipped  int64                `json:"skipped"`
	Batches  int                  `json:"batches"`
	Tasks    []judge.TaskEnvelope `json:"tasks"`
	Warnings []string             `json:"warnings"`
	Complete bool                 `json:"complete"`
}

const terminalSubmissionStatuses = `'Accepted','WrongAnswer','TimeLimitExceeded','MemoryLimitExceeded','RuntimeError','CompileError','SystemError','Cancelled'`

var scopeKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func (s *Service) RejudgeContest(ctx context.Context, actor auth.User, input ScopeRejudgeInput) (ScopeRejudgeResult, error) {
	if input.ContestID == 0 || input.ProblemID != 0 || !scopeKeyPattern.MatchString(input.Key) || !validReason(input.Reason, 1024) {
		return ScopeRejudgeResult{}, ErrInvalid
	}
	if err := s.authorized(ctx, actor); err != nil {
		return ScopeRejudgeResult{}, err
	}
	var settled sql.NullTime
	err := s.db.GetContext(ctx, &settled, `SELECT settled_at FROM contests WHERE id=?`, input.ContestID)
	if err == sql.ErrNoRows {
		return ScopeRejudgeResult{}, ErrConflict
	}
	if err != nil {
		return ScopeRejudgeResult{}, ErrUnavailable
	}
	if settled.Valid {
		return ScopeRejudgeResult{}, ErrConflict
	}
	query := `SELECT id,generation FROM submissions WHERE contest_id=? AND kind='Submit' AND status IN (` + terminalSubmissionStatuses + `) ORDER BY id`
	countQuery := `SELECT COUNT(*) FROM submissions WHERE contest_id=? AND kind='Submit' AND (contest_id IS NULL OR NOT EXISTS (SELECT 1 FROM contests c WHERE c.id=submissions.contest_id AND c.settled_at IS NOT NULL))`
	return s.rejudgeScope(ctx, actor, input, query, countQuery, input.ContestID, "contest")
}

func (s *Service) RejudgeProblem(ctx context.Context, actor auth.User, input ScopeRejudgeInput) (ScopeRejudgeResult, error) {
	if input.ProblemID == 0 || input.ContestID != 0 || !scopeKeyPattern.MatchString(input.Key) || !validReason(input.Reason, 1024) {
		return ScopeRejudgeResult{}, ErrInvalid
	}
	if err := s.authorized(ctx, actor); err != nil {
		return ScopeRejudgeResult{}, err
	}
	var found uint64
	err := s.db.GetContext(ctx, &found, `SELECT id FROM problems WHERE id=?`, input.ProblemID)
	if err == sql.ErrNoRows {
		return ScopeRejudgeResult{}, ErrConflict
	}
	if err != nil {
		return ScopeRejudgeResult{}, ErrUnavailable
	}
	query := `SELECT s.id,s.generation FROM submissions s WHERE s.problem_id=? AND s.kind='Submit' AND s.status IN (` + terminalSubmissionStatuses + `) AND (s.contest_id IS NULL OR NOT EXISTS (SELECT 1 FROM contests c WHERE c.id=s.contest_id AND c.settled_at IS NOT NULL)) ORDER BY s.id`
	countQuery := `SELECT COUNT(*) FROM submissions s WHERE s.problem_id=? AND s.kind='Submit'`
	return s.rejudgeScope(ctx, actor, input, query, countQuery, input.ProblemID, "problem")
}

func (s *Service) rejudgeScope(ctx context.Context, actor auth.User, input ScopeRejudgeInput, query, countQuery string, scopeID uint64, scope string) (ScopeRejudgeResult, error) {
	var pending int
	if err := s.db.GetContext(ctx, &pending, `SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`); err != nil {
		return ScopeRejudgeResult{}, ErrUnavailable
	}
	remaining := s.capacity - pending
	if remaining <= 0 {
		return ScopeRejudgeResult{}, judge.ErrAdminCapacity
	}
	var total int64
	if err := s.db.GetContext(ctx, &total, countQuery, scopeID); err != nil {
		return ScopeRejudgeResult{}, ErrUnavailable
	}
	items := []judge.RejudgeItem{}
	if err := s.db.SelectContext(ctx, &items, query+" LIMIT ?", scopeID, remaining); err != nil {
		return ScopeRejudgeResult{}, ErrUnavailable
	}
	result := ScopeRejudgeResult{Tasks: []judge.TaskEnvelope{}, Skipped: total - int64(len(items)), Warnings: []string{}, Complete: true}
	for start := 0; start < len(items); start += 100 {
		end := min(start+100, len(items))
		batch := append([]judge.RejudgeItem(nil), items[start:end]...)
		tasks, err := s.judge.Rejudge(ctx, actor.ID, judge.RejudgeInput{
			RequestID: server.RequestID(ctx), Key: stableScopeKey(input.Key, scope, scopeID, batch),
			Reason: input.Reason, Items: batch, UseLatestProblem: scope == "problem",
		})
		if err != nil {
			result.Complete = false
			result.Warnings = append(result.Warnings, "已完成批次保持排队；后续批次入队失败，请核对队列后重试")
			return result, nil
		}
		result.Tasks = append(result.Tasks, tasks...)
		result.Queued += len(tasks)
		result.Batches++
	}
	return result, nil
}

func stableScopeKey(base, scope string, scopeID uint64, items []judge.RejudgeItem) string {
	raw, _ := json.Marshal(struct {
		Base    string
		Scope   string
		ScopeID uint64
		Items   []judge.RejudgeItem
	}{base, scope, scopeID, items})
	sum := sha256.Sum256(raw)
	return "rj-" + hex.EncodeToString(sum[:20])
}
