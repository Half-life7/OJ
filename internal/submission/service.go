package submission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"biue/internal/judge"

	"github.com/jmoiron/sqlx"
)

type Config struct{ QueueCapacity int }

type Service struct {
	db       *sqlx.DB
	capacity int
	logger   *slog.Logger
	now      func() time.Time
}

func New(db *sqlx.DB, cfg Config, logger *slog.Logger) (*Service, error) {
	if cfg.QueueCapacity == 0 {
		cfg.QueueCapacity = 1000
	}
	if db == nil || cfg.QueueCapacity < 1 || cfg.QueueCapacity > 100000 {
		return nil, errors.New("submission service requires database and bounded queue capacity")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, capacity: cfg.QueueCapacity, logger: logger, now: time.Now}, nil
}

func (s *Service) unavailable(operation string) error {
	// SQL errors may embed source code or custom input; log only the operation.
	s.logger.Error("submission operation failed", "operation", operation)
	return ErrUnavailable
}

func validateCreate(kind string, input CreateInput) error {
	if !judge.ValidKind(judge.Kind(kind)) || input.ProblemID == 0 {
		return ErrInvalid
	}
	if _, ok := judge.LookupLanguage(input.Language); !ok {
		return ErrInvalid
	}
	if len(input.SourceCode) > 64<<10 || !utf8.ValidString(input.SourceCode) || strings.TrimSpace(input.SourceCode) == "" || strings.ContainsRune(input.SourceCode, '\x00') {
		return ErrInvalid
	}
	if input.Stdin != nil && (kind != "Run" || len(*input.Stdin) > 64<<10 || !utf8.ValidString(*input.Stdin)) {
		return ErrInvalid
	}
	return nil
}

type pinnedProblem struct {
	ID               uint64 `db:"id"`
	StatementVersion uint64 `db:"statement_version"`
	DataVersion      uint64 `db:"data_version"`
	Snapshot         []byte `db:"snapshot_json"`
	CaseCount        int    `db:"case_count"`
}

// Create serializes admission before locking account and problem rows. The
// submission, task, outbox intent and audit either commit together or not at all.
func (s *Service) Create(ctx context.Context, actor Actor, kind string, input CreateInput, requestID string) (Submission, error) {
	if actor.ID == 0 || (actor.AccountType != "permanent" && (input.ContestID == 0 || actor.AccountType != "contest")) {
		return Submission{}, ErrForbidden
	}
	if err := validateCreate(kind, input); err != nil {
		return Submission{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Submission{}, s.unavailable("create_begin")
	}
	defer tx.Rollback()
	var admissionID uint64
	if err := tx.GetContext(ctx, &admissionID, `SELECT id FROM judge_admission WHERE id = 1 FOR UPDATE`); err != nil || admissionID != 1 {
		return Submission{}, s.unavailable("admission_lock")
	}
	var pending int64
	if err := tx.GetContext(ctx, &pending, `SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`); err != nil {
		return Submission{}, s.unavailable("admission_count")
	}
	if pending >= int64(s.capacity) {
		return Submission{}, ErrQueueFull
	}
	// Recheck current account status under the same lock used by disabling or
	// changing credentials, rather than trusting an earlier HTTP authorization.
	var account struct {
		AccountType string `db:"account_type"`
		Status      string `db:"status"`
	}
	err = tx.GetContext(ctx, &account, `SELECT account_type, status FROM users WHERE id = ? FOR UPDATE`, actor.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrForbidden
	}
	if err != nil {
		return Submission{}, s.unavailable("account_lock")
	}
	if (account.AccountType != "permanent" && (input.ContestID == 0 || account.AccountType != "contest")) || account.Status != "active" {
		return Submission{}, ErrForbidden
	}
	var problem pinnedProblem
	now := s.now().UTC().Truncate(time.Microsecond)
	var teamID uint64
	if input.ContestID != 0 {
		teamID, problem, err = s.pinContest(ctx, tx, actor.ID, input.ContestID, input.ProblemID, now)
	} else {
		err = tx.GetContext(ctx, &problem, `SELECT p.id, p.statement_version, COALESCE(p.data_version, 0) AS data_version, pv.snapshot_json, COALESCE(dv.case_count, 0) AS case_count FROM problems p JOIN problem_versions pv ON pv.problem_id = p.id AND pv.version = p.statement_version LEFT JOIN problem_data_versions dv ON dv.problem_id = p.id AND dv.data_version = p.data_version WHERE p.id = ? AND p.status = 'published' AND p.visibility = 'public' FOR SHARE`, input.ProblemID)
	}
	if errors.Is(err, ErrForbidden) {
		return Submission{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, s.unavailable("problem_lock")
	}
	if problem.StatementVersion == 0 || problem.ID != input.ProblemID {
		return Submission{}, s.unavailable("problem_version")
	}
	var judging struct {
		Type string `json:"problem_type"`
	}
	if json.Unmarshal(problem.Snapshot, &judging) != nil {
		return Submission{}, s.unavailable("judging_snapshot")
	}
	if judging.Type == "output_only" {
		if kind != "Submit" || input.Language != "text" {
			return Submission{}, ErrInvalid
		}
		outputs, e := judge.ParseOutputs(input.SourceCode)
		if e != nil {
			return Submission{}, ErrInvalid
		}
		var ids []string
		if e = tx.SelectContext(ctx, &ids, `SELECT case_id FROM testcases WHERE problem_id=? AND data_version=?`, input.ProblemID, problem.DataVersion); e != nil {
			return Submission{}, s.unavailable("output_case_ids")
		}
		allowed := map[string]bool{}
		for _, id := range ids {
			allowed[id] = true
		}
		for id := range outputs {
			if !allowed[id] {
				return Submission{}, ErrInvalid
			}
		}
	} else if input.Language == "text" {
		return Submission{}, ErrInvalid
	}
	if kind == "Submit" && problem.DataVersion == 0 {
		return Submission{}, ErrNoData
	}
	totalCases := 1
	var stdin *string
	if kind == "Run" {
		if input.Stdin != nil {
			value := *input.Stdin
			stdin = &value
		} else {
			var snapshot struct {
				SampleInput string `json:"sample_input"`
			}
			if len(problem.Snapshot) > 256<<10 || json.Unmarshal(problem.Snapshot, &snapshot) != nil || !utf8.ValidString(snapshot.SampleInput) || len(snapshot.SampleInput) > 64<<10 {
				return Submission{}, s.unavailable("sample_snapshot")
			}
			stdin = &snapshot.SampleInput
		}
	} else {
		totalCases = problem.CaseCount
		if totalCases < 1 || totalCases > 512 {
			return Submission{}, s.unavailable("data_case_count")
		}
	}
	var dataVersion any
	if problem.DataVersion != 0 {
		dataVersion = problem.DataVersion
	}
	insertSQL := `INSERT INTO submissions (user_id, problem_id, statement_version, data_version, contest_id, contest_team_id, kind, language, source_code, stdin, generation, status, score, execution_time_ms, memory_usage_kb, passed_cases, total_cases, output, error_message, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?, 1, 'Pending', 0, 0, 0, 0, ?, '', '', ?, ?)`
	args := []any{actor.ID, input.ProblemID, problem.StatementVersion, dataVersion}
	if input.ContestID != 0 {
		insertSQL = strings.Replace(insertSQL, "NULL, NULL", "?, ?", 1)
		args = append(args, input.ContestID, teamID)
	}
	args = append(args, kind, input.Language, input.SourceCode, stdin, totalCases, now, now)
	result, err := tx.ExecContext(ctx, insertSQL, args...)
	if err != nil {
		return Submission{}, s.unavailable("create_submission")
	}
	insertID, err := result.LastInsertId()
	if err != nil || insertID <= 0 {
		return Submission{}, s.unavailable("create_submission_id")
	}
	id := uint64(insertID)
	result, err = tx.ExecContext(ctx, `INSERT INTO judge_tasks (submission_id, generation, attempt, status, lease_token, lease_expires_at, error_message, created_at, updated_at) VALUES (?, 1, 1, 'Pending', NULL, NULL, '', ?, ?)`, id, now, now)
	if err != nil {
		return Submission{}, s.unavailable("create_task")
	}
	insertID, err = result.LastInsertId()
	if err != nil || insertID <= 0 {
		return Submission{}, s.unavailable("create_task_id")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO judge_task_outbox (task_id, submission_id, generation, attempt, created_at) VALUES (?, ?, 1, 1, ?)`, uint64(insertID), id, now); err != nil {
		return Submission{}, s.unavailable("create_outbox")
	}
	if !utf8.ValidString(requestID) {
		return Submission{}, ErrInvalid
	}
	if utf8.RuneCountInString(requestID) > 128 {
		requestID = string([]rune(requestID)[:128])
	}
	action := "submission.create"
	if kind == "Run" {
		action = "run.create"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_user_id, action, object_type, object_id, request_id, details, created_at) VALUES (?, ?, 'submission', ?, ?, '{}', ?)`, actor.ID, action, id, requestID, now); err != nil {
		return Submission{}, s.unavailable("create_audit")
	}
	if err := tx.Commit(); err != nil {
		return Submission{}, s.unavailable("create_commit")
	}
	return Submission{Summary: Summary{ID: id, ProblemID: input.ProblemID, UserID: actor.ID, StatementVersion: problem.StatementVersion, DataVersion: problem.DataVersion, Generation: 1, Kind: kind, Language: input.Language, Status: judge.Pending, TotalCases: totalCases, CreatedAt: now, UpdatedAt: now}, SourceCode: input.SourceCode, Stdin: stdin}, nil
}

const summaryColumns = `s.id, s.problem_id, s.user_id, s.statement_version, COALESCE(s.data_version, 0) AS data_version, s.generation, s.kind, s.language, s.status, s.score, s.execution_time_ms, s.memory_usage_kb, s.passed_cases, s.total_cases, s.created_at, s.updated_at`

func normalizeList(opts ListOptions) (ListOptions, error) {
	if opts.Page == 0 {
		opts.Page = 1
	}
	if opts.Limit == 0 {
		opts.Limit = 20
	}
	if opts.Page < 1 || opts.Page > 100000 || opts.Limit < 1 || opts.Limit > 100 || opts.Kind != "" && opts.Kind != string(judge.Submit) {
		return ListOptions{}, ErrInvalid
	}
	return opts, nil
}

func (s *Service) List(ctx context.Context, actor Actor, opts ListOptions) (Page, error) {
	if actor.ID == 0 {
		return Page{}, ErrForbidden
	}
	opts, err := normalizeList(opts)
	if err != nil {
		return Page{}, err
	}
	where := " FROM submissions s WHERE s.user_id = ? AND s.kind = 'Submit'"
	args := []any{actor.ID}
	if opts.ProblemID != 0 {
		where += " AND s.problem_id = ?"
		args = append(args, opts.ProblemID)
	}
	page := Page{Items: []Summary{}, Page: opts.Page, Limit: opts.Limit}
	if err := s.db.GetContext(ctx, &page.Total, "SELECT COUNT(*)"+where, args...); err != nil {
		return Page{}, s.unavailable("list_count")
	}
	args = append(args, opts.Limit, (opts.Page-1)*opts.Limit)
	if err := s.db.SelectContext(ctx, &page.Items, "SELECT "+summaryColumns+where+" ORDER BY s.id DESC LIMIT ? OFFSET ?", args...); err != nil {
		return Page{}, s.unavailable("list")
	}
	return page, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, id uint64) (Submission, error) {
	if actor.ID == 0 {
		return Submission{}, ErrForbidden
	}
	if id == 0 {
		return Submission{}, ErrInvalid
	}
	query := "SELECT " + summaryColumns + ", s.source_code, s.stdin, s.output, s.error_message FROM submissions s WHERE s.id = ?"
	args := []any{id}
	if actor.Role != "super_admin" {
		query += " AND (s.user_id = ? OR EXISTS (SELECT 1 FROM contest_admins ca WHERE ca.contest_id=s.contest_id AND ca.user_id=?))"
		args = append(args, actor.ID, actor.ID)
	}
	var submission Submission
	err := s.db.GetContext(ctx, &submission, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, s.unavailable("get")
	}
	if submission.Kind != "Run" {
		submission.Stdin = nil
		submission.Output = ""
		if submission.Status != judge.CompileError {
			submission.ErrorMessage = ""
		}
	}
	return submission, nil
}
