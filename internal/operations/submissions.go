package operations

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"biue/internal/auth"
	"biue/internal/judge"
	"biue/internal/submission"
)

type AdminSubmissionQuery struct {
	Before, ProblemID, ContestID uint64
	Limit                        int
	Query, Kind, Status          string
}

type AdminSubmissionRow struct {
	submission.Summary
	Username     string  `db:"username" json:"username"`
	DisplayName  string  `db:"display_name" json:"display_name"`
	ProblemTitle string  `db:"problem_title" json:"problem_title"`
	ContestID    *uint64 `db:"contest_id" json:"contest_id,omitempty"`
	ContestTitle string  `db:"contest_title" json:"contest_title,omitempty"`
}

type AdminSubmissionPage struct {
	Items []AdminSubmissionRow `json:"items"`
	Next  uint64               `json:"next"`
}

func (s *Service) AdminSubmissions(ctx context.Context, actor auth.User, query AdminSubmissionQuery) (AdminSubmissionPage, error) {
	if err := s.authorized(ctx, actor); err != nil {
		return AdminSubmissionPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	query.Query = strings.TrimSpace(query.Query)
	if query.Limit < 1 || query.Limit > 100 || !utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 200 ||
		query.Kind != "" && query.Kind != string(judge.Run) && query.Kind != string(judge.Submit) ||
		query.Status != "" && !judge.ValidStatus(judge.Status(query.Status)) {
		return AdminSubmissionPage{}, ErrInvalid
	}
	where := []string{"1=1"}
	args := []any{}
	if query.Before != 0 {
		where = append(where, "s.id < ?")
		args = append(args, query.Before)
	}
	if query.ProblemID != 0 {
		where = append(where, "s.problem_id = ?")
		args = append(args, query.ProblemID)
	}
	if query.ContestID != 0 {
		where = append(where, "s.contest_id = ?")
		args = append(args, query.ContestID)
	}
	if query.Kind != "" {
		where = append(where, "s.kind = ?")
		args = append(args, query.Kind)
	}
	if query.Status != "" {
		where = append(where, "s.status = ?")
		args = append(args, query.Status)
	}
	if query.Query != "" {
		like := "%" + strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(query.Query) + "%"
		if id, err := strconv.ParseUint(query.Query, 10, 63); err == nil && id > 0 {
			where = append(where, "(s.id = ? OR u.username LIKE ? ESCAPE '=' OR p.display_name LIKE ? ESCAPE '=' OR pb.title LIKE ? ESCAPE '=' OR c.title LIKE ? ESCAPE '=')")
			args = append(args, id, like, like, like, like)
		} else {
			where = append(where, "(u.username LIKE ? ESCAPE '=' OR p.display_name LIKE ? ESCAPE '=' OR pb.title LIKE ? ESCAPE '=' OR c.title LIKE ? ESCAPE '=')")
			args = append(args, like, like, like, like)
		}
	}
	args = append(args, query.Limit)
	items := []AdminSubmissionRow{}
	err := s.db.SelectContext(ctx, &items, `SELECT s.id,s.problem_id,s.user_id,s.statement_version,
		COALESCE(s.data_version,0) AS data_version,s.generation,s.kind,s.language,s.status,s.score,
		s.execution_time_ms,s.memory_usage_kb,s.passed_cases,s.total_cases,s.created_at,s.updated_at,
		u.username,p.display_name,pb.title AS problem_title,s.contest_id,COALESCE(c.title,'') AS contest_title
		FROM submissions s
		JOIN users u ON u.id=s.user_id
		JOIN person_accounts pa ON pa.user_id=u.id
		JOIN persons p ON p.id=pa.person_id
		JOIN problems pb ON pb.id=s.problem_id
		LEFT JOIN contests c ON c.id=s.contest_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY s.id DESC LIMIT ?`, args...)
	if err != nil {
		return AdminSubmissionPage{}, ErrUnavailable
	}
	page := AdminSubmissionPage{Items: items}
	if len(items) == query.Limit {
		page.Next = items[len(items)-1].ID
	}
	return page, nil
}

func (s *Service) AdminSubmission(ctx context.Context, actor auth.User, id uint64) (submission.Submission, error) {
	if err := s.authorized(ctx, actor); err != nil {
		return submission.Submission{}, err
	}
	if id == 0 || s.submissions == nil {
		return submission.Submission{}, ErrUnavailable
	}
	result, err := s.submissions.Get(ctx, submission.Actor{ID: actor.ID, AccountType: actor.AccountType, Role: actor.GlobalRole}, id)
	if err != nil {
		return submission.Submission{}, err
	}
	return result, nil
}
