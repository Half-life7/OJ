package submission

import (
	"context"
	"github.com/jmoiron/sqlx"
	"time"
)

// Called after the admission and account locks; contest mutations use the same
// admission lock, so withdrawal and settlement cannot pass this check in flight.
func (s *Service) pinContest(ctx context.Context, tx *sqlx.Tx, user, contestID, problemID uint64, now time.Time) (uint64, pinnedProblem, error) {
	var c struct {
		Start   time.Time  `db:"starts_at"`
		End     time.Time  `db:"ends_at"`
		Settled *time.Time `db:"settled_at"`
	}
	if err := tx.GetContext(ctx, &c, `SELECT starts_at,ends_at,settled_at FROM contests WHERE id=? FOR SHARE`, contestID); err != nil {
		return 0, pinnedProblem{}, err
	}
	if now.Before(c.Start) || !now.Before(c.End) || c.Settled != nil {
		return 0, pinnedProblem{}, ErrForbidden
	}
	var team uint64
	if err := tx.GetContext(ctx, &team, `SELECT t.id FROM contest_team_members m JOIN contest_teams t ON t.id=m.team_id AND t.contest_id=m.contest_id WHERE m.contest_id=? AND m.user_id=? AND t.status='active' FOR SHARE`, contestID, user); err != nil {
		return 0, pinnedProblem{}, err
	}
	var p pinnedProblem
	err := tx.GetContext(ctx, &p, `SELECT cp.problem_id AS id,cp.statement_version,cp.data_version,pv.snapshot_json,dv.case_count FROM contest_problems cp JOIN problem_versions pv ON pv.problem_id=cp.problem_id AND pv.version=cp.statement_version JOIN problem_data_versions dv ON dv.problem_id=cp.problem_id AND dv.data_version=cp.data_version WHERE cp.contest_id=? AND cp.problem_id=? FOR SHARE`, contestID, problemID)
	return team, p, err
}
