package competition

import (
	"biue/internal/auth"
	"biue/internal/submission"
	"context"
)

func (s *Service) Submissions(ctx context.Context, a auth.User, id, user, after uint64) ([]submission.Summary, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	if user != a.ID {
		if err = allowed(ctx, tx, a, id); err != nil {
			return nil, storageError(err)
		}
	}
	rows := []submission.Summary{}
	err = tx.SelectContext(ctx, &rows, `SELECT id,problem_id,user_id,statement_version,COALESCE(data_version,0) AS data_version,generation,kind,language,status,score,execution_time_ms,memory_usage_kb,passed_cases,total_cases,created_at,updated_at FROM submissions WHERE contest_id=? AND user_id=? AND id>? ORDER BY id LIMIT 100`, id, user, after)
	return rows, storageError(err)
}
