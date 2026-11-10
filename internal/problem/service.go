package problem

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"biue/internal/testcase"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

type Service struct {
	db     *sqlx.DB
	data   DataStore
	logger *slog.Logger
	now    func() time.Time
}

func New(db *sqlx.DB, data DataStore, logger *slog.Logger) (*Service, error) {
	if db == nil || data == nil {
		return nil, errors.New("problem service requires database and data store")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, data: data, logger: logger, now: time.Now}, nil
}

func admin(a Actor) bool { return a.ID != 0 && a.Role == "super_admin" }

func (s *Service) unavailable(operation string, err error) error {
	// Driver errors can contain entire statements, including hidden problem text.
	s.logger.Error("problem operation failed", "operation", operation)
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return ErrConflict
	}
	return ErrUnavailable
}

func validText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func normalizeInput(input Input) (Input, error) {
	input.Title = strings.TrimSpace(input.Title)
	if !validText(input.Title, 800) || utf8.RuneCountInString(input.Title) == 0 || utf8.RuneCountInString(input.Title) > 200 || strings.ContainsFunc(input.Title, unicode.IsControl) {
		return Input{}, ErrInvalid
	}
	if !validText(input.Description, 64<<10) || strings.TrimSpace(input.Description) == "" {
		return Input{}, ErrInvalid
	}
	for _, text := range []string{input.InputFormat, input.OutputFormat, input.SampleInput, input.SampleOutput} {
		if !validText(text, 16<<10) {
			return Input{}, ErrInvalid
		}
	}
	if input.TimeLimitMS < 1 || input.TimeLimitMS > 60000 || input.MemoryLimitMB < 16 || input.MemoryLimitMB > 4096 {
		return Input{}, ErrInvalid
	}
	if !oneOf(input.Difficulty, "easy", "medium", "hard") {
		return Input{}, ErrInvalid
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.Visibility == "" {
		input.Visibility = "hidden"
	}
	if !oneOf(input.Status, "draft", "published", "archived") || !oneOf(input.Visibility, "public", "hidden", "contest_only") {
		return Input{}, ErrInvalid
	}
	if len(input.Tags) > 16 {
		return Input{}, ErrInvalid
	}
	tags := make([]string, 0, len(input.Tags))
	seen := make(map[string]bool, len(input.Tags))
	for _, tag := range input.Tags {
		tag, err := normalizeTag(tag)
		if err != nil {
			return Input{}, err
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	sort.Strings(tags)
	input.Tags = tags
	return input, nil
}

func normalizeTag(tag string) (string, error) {
	if !utf8.ValidString(tag) {
		return "", ErrInvalid
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	if len(tag) == 0 || utf8.RuneCountInString(tag) > 32 || strings.ContainsFunc(tag, unicode.IsControl) {
		return "", ErrInvalid
	}
	return tag, nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

type problemRow struct {
	ID               uint64    `db:"id"`
	Snapshot         []byte    `db:"snapshot_json"`
	Revision         uint64    `db:"revision"`
	StatementVersion uint64    `db:"statement_version"`
	DataVersion      uint64    `db:"data_version"`
	CreatorID        uint64    `db:"creator_id"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

const problemColumns = `p.id, p.snapshot_json, p.revision, COALESCE(p.statement_version, 0) AS statement_version, COALESCE(p.data_version, 0) AS data_version, p.creator_id, p.created_at, p.updated_at`

func decodeProblem(row problemRow) (Problem, error) {
	var input Input
	if len(row.Snapshot) > 256<<10 || json.Unmarshal(row.Snapshot, &input) != nil || row.StatementVersion == 0 || row.Revision == 0 {
		return Problem{}, ErrUnavailable
	}
	input.Revision = row.Revision
	return Problem{Input: input, ID: row.ID, StatementVersion: row.StatementVersion, DataVersion: row.DataVersion, CreatorID: row.CreatorID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func normalizePagination(page, limit int) (int, int, error) {
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = 20
	}
	if page < 1 || page > 100000 || limit < 1 || limit > 100 {
		return 0, 0, ErrInvalid
	}
	return page, limit, nil
}

func (s *Service) List(ctx context.Context, actor Actor, opts ListOptions) (Page, error) {
	page, limit, err := normalizePagination(opts.Page, opts.Limit)
	if err != nil {
		return Page{}, err
	}
	opts.Query = strings.TrimSpace(opts.Query)
	if !validText(opts.Query, 800) || utf8.RuneCountInString(opts.Query) > 200 {
		return Page{}, ErrInvalid
	}
	if opts.Difficulty != "" && !oneOf(opts.Difficulty, "easy", "medium", "hard") || opts.Status != "" && !oneOf(opts.Status, "draft", "published", "archived") || opts.Visibility != "" && !oneOf(opts.Visibility, "public", "hidden", "contest_only") {
		return Page{}, ErrInvalid
	}
	conditions := []string{"1=1"}
	args := make([]any, 0, 7)
	if !admin(actor) {
		conditions = append(conditions, "p.status = 'published'", "p.visibility = 'public'")
	}
	if opts.Query != "" {
		conditions = append(conditions, "p.title LIKE ? ESCAPE '='")
		args = append(args, "%"+strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(opts.Query)+"%")
	}
	for _, filter := range []struct{ name, value string }{{"difficulty", opts.Difficulty}, {"status", opts.Status}, {"visibility", opts.Visibility}} {
		if filter.value != "" {
			conditions = append(conditions, "p."+filter.name+" = ?")
			args = append(args, filter.value)
		}
	}
	if opts.Tag != "" {
		tag, err := normalizeTag(opts.Tag)
		if err != nil {
			return Page{}, err
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM problem_tags pt JOIN tags t ON t.id = pt.tag_id WHERE pt.problem_id = p.id AND t.name = ?)")
		args = append(args, tag)
	}
	if opts.Category != "" {
		category, err := normalizeTag("category:" + opts.Category)
		if err != nil {
			return Page{}, err
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM problem_tags pt JOIN tags t ON t.id = pt.tag_id WHERE pt.problem_id = p.id AND t.name = ?)")
		args = append(args, category)
	}
	where := " FROM problems p WHERE " + strings.Join(conditions, " AND ")
	result := Page{Items: []Problem{}, Page: page, Limit: limit}
	if err := s.db.GetContext(ctx, &result.Total, "SELECT COUNT(*)"+where, args...); err != nil {
		return Page{}, s.unavailable("list_count", err)
	}
	var rows []problemRow
	listArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	if err := s.db.SelectContext(ctx, &rows, "SELECT "+problemColumns+where+" ORDER BY p.id DESC LIMIT ? OFFSET ?", listArgs...); err != nil {
		return Page{}, s.unavailable("list", err)
	}
	for _, row := range rows {
		problem, err := decodeProblem(row)
		if err != nil {
			return Page{}, err
		}
		result.Items = append(result.Items, problem)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, id uint64) (Problem, error) {
	if id == 0 {
		return Problem{}, ErrInvalid
	}
	query := "SELECT " + problemColumns + " FROM problems p WHERE p.id = ?"
	if !admin(actor) {
		query += " AND p.status = 'published' AND p.visibility = 'public'"
	}
	var row problemRow
	err := s.db.GetContext(ctx, &row, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Problem{}, ErrNotFound
	}
	if err != nil {
		return Problem{}, s.unavailable("get", err)
	}
	return decodeProblem(row)
}

func (s *Service) Create(ctx context.Context, actor Actor, input Input, requestID string) (Problem, error) {
	if !admin(actor) {
		return Problem{}, ErrForbidden
	}
	if input.Revision != 0 {
		return Problem{}, ErrInvalid
	}
	input, err := normalizeInput(input)
	if err != nil {
		return Problem{}, err
	}
	snapshot, err := json.Marshal(input)
	if err != nil {
		return Problem{}, ErrInvalid
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Problem{}, s.unavailable("create_begin", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Microsecond)
	res, err := tx.ExecContext(ctx, `INSERT INTO problems (title, difficulty, status, visibility, snapshot_json, revision, statement_version, data_version, creator_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 1, NULL, NULL, ?, ?, ?)`, input.Title, input.Difficulty, input.Status, input.Visibility, snapshot, actor.ID, now, now)
	if err != nil {
		return Problem{}, s.unavailable("create", err)
	}
	insertID, err := res.LastInsertId()
	if err != nil || insertID <= 0 {
		return Problem{}, s.unavailable("create_id", err)
	}
	id := uint64(insertID)
	if _, err = tx.ExecContext(ctx, `INSERT INTO problem_versions (problem_id, version, snapshot_json, created_by, created_at) VALUES (?, 1, ?, ?, ?)`, id, snapshot, actor.ID, now); err != nil {
		return Problem{}, s.unavailable("create_version", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE problems SET statement_version = 1 WHERE id = ?`, id); err != nil {
		return Problem{}, s.unavailable("create_pointer", err)
	}
	if err = replaceTags(ctx, tx, id, input.Tags); err != nil {
		return Problem{}, s.unavailable("create_tags", err)
	}
	if err = audit(ctx, tx, actor, "problem.create", id, requestID, now); err != nil {
		return Problem{}, s.unavailable("create_audit", err)
	}
	if err = tx.Commit(); err != nil {
		return Problem{}, s.unavailable("create_commit", err)
	}
	input.Revision = 1
	return Problem{Input: input, ID: id, StatementVersion: 1, CreatorID: actor.ID, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Service) Update(ctx context.Context, actor Actor, id uint64, input Input, requestID string) (Problem, error) {
	if !admin(actor) {
		return Problem{}, ErrForbidden
	}
	if id == 0 || input.Revision == 0 {
		return Problem{}, ErrInvalid
	}
	input, err := normalizeInput(input)
	if err != nil {
		return Problem{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Problem{}, s.unavailable("update_begin", err)
	}
	defer tx.Rollback()
	row, err := lockedProblem(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Problem{}, ErrNotFound
	}
	if err != nil {
		return Problem{}, s.unavailable("update_lock", err)
	}
	if row.Revision != input.Revision || row.Revision == math.MaxUint64 || row.StatementVersion == 0 || row.StatementVersion == math.MaxUint64 {
		return Problem{}, ErrConflict
	}
	input.Revision = 0
	snapshot, err := json.Marshal(input)
	if err != nil {
		return Problem{}, ErrInvalid
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	version, revision := row.StatementVersion+1, row.Revision+1
	if _, err = tx.ExecContext(ctx, `INSERT INTO problem_versions (problem_id, version, snapshot_json, created_by, created_at) VALUES (?, ?, ?, ?, ?)`, id, version, snapshot, actor.ID, now); err != nil {
		return Problem{}, s.unavailable("update_version", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE problems SET title = ?, difficulty = ?, status = ?, visibility = ?, snapshot_json = ?, revision = ?, statement_version = ?, updated_at = ? WHERE id = ? AND revision = ?`, input.Title, input.Difficulty, input.Status, input.Visibility, snapshot, revision, version, now, id, row.Revision)
	if err != nil {
		return Problem{}, s.unavailable("update", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return Problem{}, ErrConflict
	}
	if err = replaceTags(ctx, tx, id, input.Tags); err != nil {
		return Problem{}, s.unavailable("update_tags", err)
	}
	if err = audit(ctx, tx, actor, "problem.update", id, requestID, now); err != nil {
		return Problem{}, s.unavailable("update_audit", err)
	}
	if err = tx.Commit(); err != nil {
		return Problem{}, s.unavailable("update_commit", err)
	}
	input.Revision = revision
	return Problem{Input: input, ID: id, StatementVersion: version, DataVersion: row.DataVersion, CreatorID: row.CreatorID, CreatedAt: row.CreatedAt, UpdatedAt: now}, nil
}

func lockedProblem(ctx context.Context, tx *sqlx.Tx, id uint64) (problemRow, error) {
	var row problemRow
	err := tx.GetContext(ctx, &row, "SELECT "+problemColumns+" FROM problems p WHERE p.id = ? FOR UPDATE", id)
	return row, err
}

func replaceTags(ctx context.Context, tx *sqlx.Tx, id uint64, tags []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM problem_tags WHERE problem_id = ?`, id); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?) ON DUPLICATE KEY UPDATE name = ?`, tag, tag); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO problem_tags (problem_id, tag_id) SELECT ?, id FROM tags WHERE name = ?`, id, tag)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return ErrUnavailable
		}
	}
	return nil
}

func audit(ctx context.Context, tx *sqlx.Tx, actor Actor, action string, id uint64, requestID string, now time.Time) error {
	if !utf8.ValidString(requestID) {
		return ErrInvalid
	}
	if utf8.RuneCountInString(requestID) > 128 {
		requestID = string([]rune(requestID)[:128])
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_user_id, action, object_type, object_id, request_id, details, created_at) VALUES (?, ?, 'problem', ?, ?, '{}', ?)`, actor.ID, action, id, requestID, now)
	return err
}

var caseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var storageKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validateDataset(dataset testcase.Dataset) error {
	limits := testcase.DefaultLimits()
	if len(dataset.Cases) == 0 || len(dataset.Cases) > limits.MaxCases {
		return ErrInvalid
	}
	var totalBytes, totalScore int64
	seen := make(map[string]bool, len(dataset.Cases))
	for i, item := range dataset.Cases {
		folded := strings.ToLower(item.ID)
		if !caseIDPattern.MatchString(item.ID) || seen[folded] || (i > 0 && dataset.Cases[i-1].ID >= item.ID) || item.Score < 0 || totalScore > math.MaxInt64-item.Score || int64(len(item.Input)) > limits.MaxFileBytes || int64(len(item.Output)) > limits.MaxFileBytes {
			return ErrInvalid
		}
		seen[folded] = true
		totalScore += item.Score
		totalBytes += int64(len(item.Input)) + int64(len(item.Output))
		if totalBytes > limits.MaxTotalBytes {
			return ErrInvalid
		}
	}
	if totalBytes != dataset.TotalBytes || totalScore != dataset.TotalScore || totalScore <= 0 || testcase.Fingerprint(dataset.Cases) != dataset.Digest {
		return ErrInvalid
	}
	return nil
}

func (s *Service) Import(ctx context.Context, actor Actor, id, revision uint64, dataset testcase.Dataset, requestID string) (DataVersion, error) {
	if !admin(actor) {
		return DataVersion{}, ErrForbidden
	}
	if id == 0 || revision == 0 {
		return DataVersion{}, ErrInvalid
	}
	if err := validateDataset(dataset); err != nil {
		return DataVersion{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return DataVersion{}, s.unavailable("import_begin", err)
	}
	defer tx.Rollback()
	row, err := lockedProblem(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return DataVersion{}, ErrNotFound
	}
	if err != nil {
		return DataVersion{}, s.unavailable("import_lock", err)
	}
	if row.Revision != revision || row.Revision == math.MaxUint64 || row.DataVersion == math.MaxUint64 || row.StatementVersion == 0 {
		return DataVersion{}, ErrConflict
	}
	key, err := s.data.Write(ctx, dataset)
	if err != nil {
		return DataVersion{}, s.unavailable("import_files", err)
	}
	committed := false
	defer func() {
		if !committed {
			s.logger.Warn("published problem data retained for reconciliation", "problem_id", id, "storage_key", key)
		}
	}()
	if !storageKeyPattern.MatchString(key) {
		return DataVersion{}, s.unavailable("import_storage_key", ErrInvalid)
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	result := DataVersion{Version: row.DataVersion + 1, Digest: hex.EncodeToString(dataset.Digest[:]), CaseCount: len(dataset.Cases), TotalBytes: dataset.TotalBytes, TotalScore: dataset.TotalScore, CreatedBy: actor.ID, CreatedAt: now, Revision: revision + 1, Cases: make([]CaseInfo, 0, len(dataset.Cases))}
	if _, err = tx.ExecContext(ctx, `INSERT INTO problem_data_versions (problem_id, data_version, storage_key, digest, case_count, total_bytes, total_score, revision, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, result.Version, key, result.Digest, result.CaseCount, result.TotalBytes, result.TotalScore, result.Revision, actor.ID, now); err != nil {
		return DataVersion{}, s.unavailable("import_version", err)
	}
	for _, item := range dataset.Cases {
		info := CaseInfo{ID: item.ID, Score: item.Score, InputBytes: int64(len(item.Input)), OutputBytes: int64(len(item.Output))}
		if _, err = tx.ExecContext(ctx, `INSERT INTO testcases (problem_id, data_version, case_id, score, input_bytes, output_bytes) VALUES (?, ?, ?, ?, ?, ?)`, id, result.Version, info.ID, info.Score, info.InputBytes, info.OutputBytes); err != nil {
			return DataVersion{}, s.unavailable("import_case", err)
		}
		result.Cases = append(result.Cases, info)
	}
	res, err := tx.ExecContext(ctx, `UPDATE problems SET data_version = ?, revision = ?, updated_at = ? WHERE id = ? AND revision = ?`, result.Version, result.Revision, now, id, revision)
	if err != nil {
		return DataVersion{}, s.unavailable("import_pointer", err)
	}
	if count, err := res.RowsAffected(); err != nil || count != 1 {
		return DataVersion{}, ErrConflict
	}
	if err = audit(ctx, tx, actor, "problem.data_import", id, requestID, now); err != nil {
		return DataVersion{}, s.unavailable("import_audit", err)
	}
	if err = tx.Commit(); err != nil {
		return DataVersion{}, s.unavailable("import_commit", err)
	}
	committed = true
	return result, nil
}

type versionRow struct {
	Version    uint64    `db:"data_version"`
	Digest     string    `db:"digest"`
	CaseCount  int       `db:"case_count"`
	TotalBytes int64     `db:"total_bytes"`
	TotalScore int64     `db:"total_score"`
	Revision   uint64    `db:"revision"`
	CreatedBy  uint64    `db:"created_by"`
	CreatedAt  time.Time `db:"created_at"`
}

const versionColumns = `data_version, digest, case_count, total_bytes, total_score, revision, created_by, created_at`

func (row versionRow) result() DataVersion {
	return DataVersion{Version: row.Version, Digest: row.Digest, CaseCount: row.CaseCount, TotalBytes: row.TotalBytes, TotalScore: row.TotalScore, Revision: row.Revision, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
}

func (s *Service) ListDataVersions(ctx context.Context, actor Actor, id uint64, page, limit int) (VersionPage, error) {
	if !admin(actor) {
		return VersionPage{}, ErrForbidden
	}
	if id == 0 {
		return VersionPage{}, ErrInvalid
	}
	page, limit, err := normalizePagination(page, limit)
	if err != nil {
		return VersionPage{}, err
	}
	if err := s.exists(ctx, id); err != nil {
		return VersionPage{}, err
	}
	result := VersionPage{Items: []DataVersion{}, Page: page, Limit: limit}
	if err := s.db.GetContext(ctx, &result.Total, `SELECT COUNT(*) FROM problem_data_versions WHERE problem_id = ?`, id); err != nil {
		return VersionPage{}, s.unavailable("data_versions_count", err)
	}
	var rows []versionRow
	if err := s.db.SelectContext(ctx, &rows, "SELECT "+versionColumns+" FROM problem_data_versions WHERE problem_id = ? ORDER BY data_version DESC LIMIT ? OFFSET ?", id, limit, (page-1)*limit); err != nil {
		return VersionPage{}, s.unavailable("data_versions", err)
	}
	for _, row := range rows {
		result.Items = append(result.Items, row.result())
	}
	return result, nil
}

func (s *Service) exists(ctx context.Context, id uint64) error {
	var found uint64
	err := s.db.GetContext(ctx, &found, `SELECT id FROM problems WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return s.unavailable("exists", err)
	}
	return nil
}

func (s *Service) GetDataVersion(ctx context.Context, actor Actor, id, version uint64) (DataVersion, error) {
	if !admin(actor) {
		return DataVersion{}, ErrForbidden
	}
	if id == 0 || version == 0 {
		return DataVersion{}, ErrInvalid
	}
	var row versionRow
	err := s.db.GetContext(ctx, &row, "SELECT "+versionColumns+" FROM problem_data_versions WHERE problem_id = ? AND data_version = ?", id, version)
	if errors.Is(err, sql.ErrNoRows) {
		return DataVersion{}, ErrNotFound
	}
	if err != nil {
		return DataVersion{}, s.unavailable("data_version", err)
	}
	var cases []struct {
		ID          string `db:"case_id"`
		Score       int64  `db:"score"`
		InputBytes  int64  `db:"input_bytes"`
		OutputBytes int64  `db:"output_bytes"`
	}
	if err := s.db.SelectContext(ctx, &cases, `SELECT case_id, score, input_bytes, output_bytes FROM testcases WHERE problem_id = ? AND data_version = ? ORDER BY case_id`, id, version); err != nil {
		return DataVersion{}, s.unavailable("data_version_cases", err)
	}
	if len(cases) != row.CaseCount {
		return DataVersion{}, s.unavailable("data_version_integrity", ErrInvalid)
	}
	result := row.result()
	result.Cases = make([]CaseInfo, 0, len(cases))
	for _, item := range cases {
		result.Cases = append(result.Cases, CaseInfo{ID: item.ID, Score: item.Score, InputBytes: item.InputBytes, OutputBytes: item.OutputBytes})
	}
	return result, nil
}
