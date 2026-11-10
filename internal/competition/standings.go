package competition

import (
	"biue/internal/auth"
	"biue/internal/scoring"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"time"
)

type Member struct {
	TeamID   uint64          `db:"team_id" json:"team_id"`
	UserID   uint64          `db:"user_id" json:"user_id"`
	PersonID uint64          `db:"person_id" json:"person_id"`
	Identity json.RawMessage `db:"identity_snapshot" json:"identity"`
}
type Team struct {
	ID      uint64    `db:"id" json:"id"`
	Name    string    `db:"name" json:"name"`
	Type    string    `db:"participant_type" json:"participant_type"`
	Status  string    `db:"status" json:"status"`
	Created time.Time `db:"created_at" json:"created_at"`
}
type ProblemView struct {
	ID               uint64          `db:"problem_id" json:"problem_id"`
	StatementVersion uint64          `db:"statement_version" json:"statement_version"`
	DataVersion      uint64          `db:"data_version" json:"data_version"`
	Statement        json.RawMessage `db:"snapshot_json" json:"statement"`
}
type View struct {
	Frozen    bool          `json:"frozen"`
	Contest   Contest       `json:"contest"`
	Manager   bool          `json:"manager"`
	Teams     []Team        `json:"teams"`
	Members   []Member      `json:"members"`
	Problems  []ProblemView `json:"problems"`
	Standings []scoring.Row `json:"standings"`
}

func loadContest(ctx context.Context, tx *sqlx.Tx, id uint64) (Contest, error) {
	var c Contest
	err := tx.GetContext(ctx, &c, `SELECT `+columns+` FROM contests WHERE id=?`, id)
	return c, err
}
func readInput(ctx context.Context, tx *sqlx.Tx, c Contest) (scoring.Input, error) {
	in := scoring.Input{Start: c.Start, End: c.End}
	teams := []Team{}
	if err := tx.SelectContext(ctx, &teams, `SELECT id,name,participant_type,status,created_at FROM contest_teams WHERE contest_id=? ORDER BY id`, c.ID); err != nil {
		return in, err
	}
	for _, t := range teams {
		in.Teams = append(in.Teams, scoring.Team{ID: t.ID, Official: t.Type == "official", Active: t.Status == "active"})
	}
	var cases []struct {
		Problem uint64 `db:"problem_id"`
		ID      string `db:"case_id"`
		Score   int64  `db:"score"`
	}
	if err := tx.SelectContext(ctx, &cases, `SELECT tc.problem_id,tc.case_id,tc.score FROM contest_problems cp JOIN testcases tc ON tc.problem_id=cp.problem_id AND tc.data_version=cp.data_version WHERE cp.contest_id=? ORDER BY tc.problem_id,tc.case_id`, c.ID); err != nil {
		return in, err
	}
	for _, p := range cases {
		if len(in.Problems) == 0 || in.Problems[len(in.Problems)-1].ID != p.Problem {
			in.Problems = append(in.Problems, scoring.Problem{ID: p.Problem, Cases: map[string]int64{}})
		}
		in.Problems[len(in.Problems)-1].Cases[p.ID] = p.Score
	}
	var subs []struct {
		ID      uint64    `db:"id"`
		Team    uint64    `db:"contest_team_id"`
		Problem uint64    `db:"problem_id"`
		At      time.Time `db:"created_at"`
		Status  string    `db:"status"`
		Score   int64     `db:"score"`
	}
	if err := tx.SelectContext(ctx, &subs, `SELECT s.id,s.contest_team_id,s.problem_id,s.created_at,r.status,r.score FROM submissions s JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation WHERE s.contest_id=? AND s.kind='Submit' AND s.status=r.status ORDER BY s.created_at,s.id`, c.ID); err != nil {
		return in, err
	}
	index := map[uint64]int{}
	for _, s := range subs {
		index[s.ID] = len(in.Submissions)
		in.Submissions = append(in.Submissions, scoring.Submission{ID: s.ID, TeamID: s.Team, ProblemID: s.Problem, At: s.At, Kind: "Submit", Status: s.Status, Score: s.Score, Cases: map[string]int64{}})
	}
	var results []struct {
		Submission uint64 `db:"submission_id"`
		Case       string `db:"case_id"`
		Score      int64  `db:"score"`
		Status     string `db:"status"`
	}
	if err := tx.SelectContext(ctx, &results, `SELECT cr.submission_id,cr.case_id,cr.score,cr.status FROM submission_case_results cr JOIN submissions s ON s.id=cr.submission_id AND s.generation=cr.generation JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation AND r.status=s.status WHERE s.contest_id=? AND s.kind='Submit'`, c.ID); err != nil {
		return in, err
	}
	for _, r := range results {
		i, ok := index[r.Submission]
		if !ok {
			return in, ErrConflict
		}
		in.Submissions[i].Cases[r.Case] = r.Score
		if in.Submissions[i].PassedCases == nil {
			in.Submissions[i].PassedCases = map[string]bool{}
		}
		in.Submissions[i].PassedCases[r.Case] = r.Status == "Accepted"
	}
	return in, nil
}
func calculate(ctx context.Context, tx *sqlx.Tx, c Contest) ([]scoring.Row, error) {
	in, err := readInput(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	engine, err := scoring.New(c.Mode)
	if err != nil {
		return nil, err
	}
	return engine.Calculate(in)
}
func (s *Service) View(ctx context.Context, a auth.User, id uint64) (View, error) {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return View{}, ErrUnavailable
	}
	defer tx.Rollback()
	c, err := loadContest(ctx, tx, id)
	if err != nil {
		return View{}, storageError(err)
	}
	err = allowed(ctx, tx, a, id)
	if err != nil && err != ErrForbidden {
		return View{}, storageError(err)
	}
	manager := err == nil
	v := View{Contest: c, Manager: manager, Teams: []Team{}, Members: []Member{}, Problems: []ProblemView{}, Standings: []scoring.Row{}}
	var member bool
	if err = tx.GetContext(ctx, &member, `SELECT EXISTS(SELECT 1 FROM contest_team_members WHERE contest_id=? AND user_id=?)`, id, a.ID); err != nil {
		return v, ErrUnavailable
	}
	if !manager && !member {
		return v, nil
	}
	if err = tx.SelectContext(ctx, &v.Teams, `SELECT id,name,participant_type,status,created_at FROM contest_teams WHERE contest_id=? ORDER BY id`, id); err != nil {
		return v, ErrUnavailable
	}
	if err = tx.SelectContext(ctx, &v.Members, `SELECT team_id,user_id,person_id,identity_snapshot FROM contest_team_members WHERE contest_id=? ORDER BY team_id,user_id`, id); err != nil {
		return v, ErrUnavailable
	}
	if manager || !s.now().Before(c.Start) {
		if err = tx.SelectContext(ctx, &v.Problems, `SELECT cp.problem_id,cp.statement_version,cp.data_version,pv.snapshot_json FROM contest_problems cp JOIN problem_versions pv ON pv.problem_id=cp.problem_id AND pv.version=cp.statement_version WHERE cp.contest_id=? ORDER BY cp.problem_id`, id); err != nil {
			return v, ErrUnavailable
		}
	}
	{
		var frozen []byte
		err = tx.GetContext(ctx, &frozen, `SELECT standings FROM contest_freezes WHERE contest_id=? AND ended_at IS NULL ORDER BY id DESC LIMIT 1`, id)
		if err == nil {
			v.Frozen = true
			if !manager && json.Unmarshal(frozen, &v.Standings) != nil {
				return View{}, ErrUnavailable
			}
			if !manager {
				return v, storageError(tx.Commit())
			}
		}
		if err != nil && err != sql.ErrNoRows {
			return View{}, ErrUnavailable
		}
	}
	if c.Settled != nil {
		var b []byte
		if err = tx.GetContext(ctx, &b, `SELECT standings FROM contest_snapshots WHERE contest_id=?`, id); err == nil {
			err = json.Unmarshal(b, &v.Standings)
		}
	} else {
		v.Standings, err = calculate(ctx, tx, c)
	}
	if err != nil {
		return View{}, storageError(err)
	}
	return v, storageError(tx.Commit())
}
func (s *Service) Settle(ctx context.Context, a auth.User, id uint64) ([]scoring.Row, error) {
	var rows []scoring.Row
	err := s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, err := contest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = allowed(ctx, tx, a, id); err != nil {
			return err
		}
		if c.Settled != nil {
			var data []byte
			if err = tx.GetContext(ctx, &data, `SELECT standings FROM contest_snapshots WHERE contest_id=?`, id); err != nil {
				return err
			}
			return json.Unmarshal(data, &rows)
		}
		if s.now().Before(c.End) {
			return ErrConflict
		}
		var ids []uint64
		if err = tx.SelectContext(ctx, &ids, `SELECT id FROM submissions WHERE contest_id=? ORDER BY id FOR UPDATE`, id); err != nil {
			return err
		}
		var incomplete int
		if err = tx.GetContext(ctx, &incomplete, `SELECT COUNT(*) FROM submissions s LEFT JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation WHERE s.contest_id=? AND s.kind='Submit' AND (s.status IN ('Pending','Queued','Compiling','Running') OR r.submission_id IS NULL OR s.status<>r.status)`, id); err != nil {
			return err
		}
		if incomplete != 0 {
			return ErrConflict
		}
		rows, err = calculate(ctx, tx, c)
		if err != nil {
			return err
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO contest_snapshots(contest_id,standings,created_at) VALUES (?,?,UTC_TIMESTAMP(6))`, id, string(data)); err != nil {
			return err
		}
		n := 0
		for _, r := range rows {
			if r.Rank > 0 {
				n++
			}
		}
		for _, r := range rows {
			if r.Rank == 0 {
				continue
			}
			points, e := s.points.Calculate(n, r.Rank)
			if e != nil {
				return e
			}
			if points < 0 || points > 100 {
				return ErrInvalid
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO contest_points(contest_id,person_id,team_id,points) SELECT m.contest_id,pa.person_id,m.team_id,? FROM contest_team_members m JOIN person_accounts pa ON pa.user_id=m.user_id WHERE m.contest_id=? AND m.team_id=? ON DUPLICATE KEY UPDATE points=GREATEST(contest_points.points,VALUES(points))`, points, id, r.TeamID); e != nil {
				return e
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE contests SET settled_at=UTC_TIMESTAMP(6) WHERE id=?`, id); err != nil {
			return err
		}
		if err = audit(ctx, tx, a.ID, "contest.settle", "contest", id, map[string]any{"teams": n, "policy": s.points.Name()}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM global_board_cache`)
		return err
	})
	return rows, err
}
