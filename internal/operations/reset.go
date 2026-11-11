package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"biue/internal/auth"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

const ResetConfirmation = "初始化全部业务数据"

type ResetInput struct {
	Confirmation string `json:"confirmation"`
	Reason       string `json:"reason"`
}

type ResetResult struct {
	Deleted      map[string]int64 `json:"deleted"`
	RedisKeys    int64            `json:"redis_keys"`
	DataVersions int              `json:"data_versions"`
	Warnings     []string         `json:"warnings"`
	Complete     bool             `json:"complete"`
}

func validReason(reason string, maximum int) bool {
	reason = strings.TrimSpace(reason)
	return reason != "" && utf8.ValidString(reason) && utf8.RuneCountInString(reason) <= maximum && !strings.ContainsFunc(reason, unicode.IsControl)
}

// Reset removes all business data while preserving the authenticated permanent
// super administrator, that administrator's identity and sessions, schema
// migration state, and the singleton judge admission lock.
func (s *Service) Reset(ctx context.Context, a auth.User, input ResetInput, requestID string) (ResetResult, error) {
	if input.Confirmation != ResetConfirmation || !validReason(input.Reason, 255) {
		return ResetResult{}, ErrInvalid
	}
	if s.db == nil {
		return ResetResult{}, ErrUnavailable
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ResetResult{}, ErrUnavailable
	}
	defer tx.Rollback()
	var admission int
	if err := tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`); err != nil || admission != 1 {
		return ResetResult{}, ErrUnavailable
	}
	var current struct {
		ID       uint64 `db:"id"`
		PersonID uint64 `db:"person_id"`
		Role     string `db:"global_role"`
		Type     string `db:"account_type"`
		Status   string `db:"status"`
	}
	if err := tx.GetContext(ctx, &current, `SELECT u.id,pa.person_id,u.global_role,u.account_type,u.status FROM users u JOIN person_accounts pa ON pa.user_id=u.id WHERE u.id=? FOR UPDATE`, a.ID); err != nil {
		return ResetResult{}, ErrForbidden
	}
	if current.ID != a.ID || current.Role != "super_admin" || current.Type != "permanent" || current.Status != "active" {
		return ResetResult{}, ErrForbidden
	}
	var active int64
	if err := tx.GetContext(ctx, &active, `SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`); err != nil {
		return ResetResult{}, ErrUnavailable
	}
	if active != 0 {
		return ResetResult{}, ErrConflict
	}
	// Problem rows and their immutable versions reference each other. Release
	// both nullable pointers before deleting either side of that cycle.
	if _, err := tx.ExecContext(ctx, `UPDATE problems SET statement_version=NULL,data_version=NULL`); err != nil {
		slog.Error("operations reset failed", "step", "problem pointers", "error", err)
		return ResetResult{}, ErrUnavailable
	}
	result := ResetResult{Deleted: map[string]int64{}, Warnings: []string{}}
	tables := []string{
		"judge_dead_letters",
		"judge_task_outbox",
		"submission_case_results",
		"submission_results",
		"judge_attempt_failures",
		"judge_tasks",
		"rejudge_requests",
		"submissions",
		"judge_message_quarantine",
		"problem_list_items",
		"problem_list_favorites",
		"discussion_likes",
		"community_write_limits",
		"community_documents",
		"contest_snapshots",
		"contest_points",
		"contest_freezes",
		"contest_imports",
		"contest_team_members",
		"contest_teams",
		"contest_problems",
		"contest_admins",
		"contests",
		"global_board_cache",
		"problem_tags",
		"testcases",
		"tags",
		"problem_versions",
		"problem_data_versions",
	}
	if _, err := tx.ExecContext(ctx, `UPDATE community_documents SET parent_id=NULL WHERE parent_id IS NOT NULL`); err != nil {
		return ResetResult{}, ErrUnavailable
	}
	for _, table := range tables {
		if err := countDelete(ctx, tx, result.Deleted, table); err != nil {
			slog.Error("operations reset failed", "step", table, "error", err)
			return ResetResult{}, ErrUnavailable
		}
	}
	if err := countDelete(ctx, tx, result.Deleted, "problems"); err != nil {
		slog.Error("operations reset failed", "step", "problems", "error", err)
		return ResetResult{}, ErrUnavailable
	}
	if err := countDelete(ctx, tx, result.Deleted, "student_no_history"); err != nil {
		slog.Error("operations reset failed", "step", "student_no_history", "error", err)
		return ResetResult{}, ErrUnavailable
	}
	// audit_logs.actor_user_id uses ON DELETE RESTRICT, so clear the old audit
	// trail before removing other users. A new reset audit is written after the
	// cleanup and continues to reference the preserved administrator.
	if err := countDelete(ctx, tx, result.Deleted, "audit_logs"); err != nil {
		slog.Error("operations reset failed", "step", "audit_logs", "error", err)
		return ResetResult{}, ErrUnavailable
	}
	scoped := []struct {
		name string
		sql  string
		arg  uint64
	}{
		{"auth_sessions", "DELETE FROM auth_sessions WHERE user_id<>?", a.ID},
		{"person_accounts", "DELETE FROM person_accounts WHERE user_id<>?", a.ID},
		{"users", "DELETE FROM users WHERE id<>?", a.ID},
		{"persons", "DELETE FROM persons WHERE id<>?", current.PersonID},
	}
	for _, item := range scoped {
		rows, err := tx.ExecContext(ctx, item.sql, item.arg)
		if err != nil {
			slog.Error("operations reset failed", "step", item.name, "error", err)
			return ResetResult{}, ErrUnavailable
		}
		count, err := rows.RowsAffected()
		if err != nil {
			return ResetResult{}, ErrUnavailable
		}
		result.Deleted[item.name] = count
	}
	details, _ := json.Marshal(map[string]any{"reason": strings.TrimSpace(input.Reason), "deleted": result.Deleted})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,'operations.reset','system',0,?,?,UTC_TIMESTAMP(6))`, a.ID, requestID, string(details)); err != nil {
		return ResetResult{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ResetResult{}, ErrUnavailable
	}

	result.RedisKeys, err = clearNamespace(ctx, s.redis, "oj:*")
	if err != nil {
		result.Warnings = append(result.Warnings, "数据库已初始化，但 Redis oj:* 缓存未完全清理")
	}
	if s.data == nil {
		result.Warnings = append(result.Warnings, "题目数据目录未接入，未执行文件清理")
	} else {
		result.DataVersions, err = s.data.Clear(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, "数据库已初始化，但题目数据目录未完全清理")
		}
	}
	result.Complete = len(result.Warnings) == 0
	return result, nil
}

func countDelete(ctx context.Context, tx *sqlx.Tx, counts map[string]int64, table string) error {
	allowed := map[string]bool{
		"audit_logs": true, "community_documents": true, "community_write_limits": true,
		"contest_admins": true, "contest_freezes": true, "contest_imports": true,
		"contest_points": true, "contest_problems": true, "contest_snapshots": true,
		"contest_team_members": true, "contest_teams": true, "contests": true,
		"discussion_likes": true, "global_board_cache": true, "judge_attempt_failures": true,
		"judge_dead_letters": true, "judge_message_quarantine": true, "judge_task_outbox": true,
		"judge_tasks": true, "person_accounts": true, "problem_data_versions": true,
		"problem_list_favorites": true, "problem_list_items": true, "problem_tags": true,
		"problem_versions": true, "problems": true, "rejudge_requests": true,
		"student_no_history": true, "submission_case_results": true, "submission_results": true,
		"submissions": true, "tags": true, "testcases": true,
	}
	if !allowed[table] {
		return fmt.Errorf("unsupported reset table")
	}
	rows, err := tx.ExecContext(ctx, "DELETE FROM "+table)
	if err != nil {
		return err
	}
	count, err := rows.RowsAffected()
	if err != nil {
		return err
	}
	counts[table] = count
	return nil
}

func clearNamespace(ctx context.Context, client *redis.Client, pattern string) (int64, error) {
	if client == nil {
		return 0, errors.New("redis unavailable")
	}
	var cursor uint64
	var removed int64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return removed, err
		}
		if len(keys) > 0 {
			n, err := client.Del(ctx, keys...).Result()
			if err != nil {
				return removed, err
			}
			removed += n
		}
		cursor = next
		if cursor == 0 {
			return removed, nil
		}
	}
}
