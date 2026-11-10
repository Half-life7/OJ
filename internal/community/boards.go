package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type BoardRow struct {
	Rank     int       `json:"rank"`
	PersonID uint64    `db:"person_id" json:"person_id"`
	Username string    `db:"username" json:"username"`
	Name     string    `db:"display_name" json:"display_name"`
	Value    int64     `db:"value" json:"value"`
	Reached  time.Time `db:"reached_at" json:"reached_at"`
}
type Board struct {
	Period string     `json:"period"`
	Kind   string     `json:"kind"`
	Built  time.Time  `json:"built_at"`
	Items  []BoardRow `json:"items"`
	Total  int        `json:"total"`
	Page   int        `json:"page"`
}

const eligibleAC = `SELECT DISTINCT pa.person_id,s.problem_id,MIN(s.created_at) AS reached_at FROM submissions s JOIN users u ON u.id=s.user_id JOIN person_accounts pa ON pa.user_id=u.id JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation JOIN problems p ON p.id=s.problem_id WHERE u.account_type='permanent' AND u.status='active' AND s.contest_id IS NULL AND s.kind='Submit' AND s.status='Accepted' AND r.status='Accepted' AND p.status='published' AND p.visibility='public' GROUP BY pa.person_id,s.problem_id`
const eligibleSolutions = `SELECT pa.person_id,d.problem_id,MIN(d.published_at) AS reached_at FROM community_documents d JOIN users u ON u.id=d.owner_id JOIN person_accounts pa ON pa.user_id=u.id JOIN problems p ON p.id=d.problem_id WHERE d.kind='solution' AND d.visibility='public' AND d.review_status='approved' AND d.deleted_at IS NULL AND d.published_at IS NOT NULL AND u.account_type='permanent' AND u.status='active' AND p.status='published' AND p.visibility='public' AND EXISTS(SELECT 1 FROM submissions s JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation WHERE s.user_id=d.owner_id AND s.problem_id=d.problem_id AND s.kind='Submit' AND s.status='Accepted' AND r.status='Accepted') GROUP BY pa.person_id,d.problem_id`
const eligiblePoints = `SELECT pa.person_id,cp.contest_id,MAX(cp.points) AS points,MAX(c.settled_at) AS reached_at FROM contest_points cp JOIN contests c ON c.id=cp.contest_id JOIN contest_team_members m ON m.contest_id=cp.contest_id AND m.team_id=cp.team_id JOIN person_accounts pa ON pa.user_id=m.user_id JOIN users u ON u.id=m.user_id JOIN contest_teams t ON t.id=m.team_id AND t.contest_id=m.contest_id WHERE c.settled_at IS NOT NULL AND t.participant_type='official' AND t.status='active' AND u.account_type='permanent' AND u.status='active' GROUP BY pa.person_id,cp.contest_id`

func boardQuery(kind string) (string, error) {
	return boardQueryPeriod(kind, "all")
}
func boardQueryPeriod(kind, period string) (string, error) {
	var inner, value string
	switch kind {
	case "practice":
		inner = eligibleAC
		value = "COUNT(*)"
	case "solutions":
		inner = eligibleSolutions
		value = "COUNT(*)"
	case "contest":
		inner = eligiblePoints
		value = "SUM(points)"
	default:
		return "", ErrInvalid
	}
	filter := ""
	switch period {
	case "all":
	case "week":
		filter = " WHERE reached_at>=UTC_TIMESTAMP(6)-INTERVAL 7 DAY"
	case "month":
		filter = " WHERE reached_at>=UTC_TIMESTAMP(6)-INTERVAL 30 DAY"
	default:
		return "", ErrInvalid
	}
	return `SELECT x.person_id,n.username,p.display_name,x.value,x.reached_at FROM (SELECT person_id,` + value + ` AS value,MAX(reached_at) AS reached_at FROM (` + inner + `) facts` + filter + ` GROUP BY person_id) x JOIN persons p ON p.id=x.person_id JOIN (SELECT pa.person_id,MIN(u.username) AS username FROM person_accounts pa JOIN users u ON u.id=pa.user_id WHERE u.account_type='permanent' AND u.status='active' GROUP BY pa.person_id) n ON n.person_id=x.person_id ORDER BY x.value DESC,x.reached_at,x.person_id`, nil
}
func assignRanks(rows []BoardRow) {
	for i := range rows {
		rows[i].Rank = i + 1
		if i > 0 && rows[i].Value == rows[i-1].Value && rows[i].Reached.Equal(rows[i-1].Reached) {
			rows[i].Rank = rows[i-1].Rank
		}
	}
}
func (s *Service) Board(ctx context.Context, kind string, page int) (Board, error) {
	return s.BoardPeriod(ctx, kind, "all", page)
}
func (s *Service) BoardPeriod(ctx context.Context, kind, period string, page int) (Board, error) {
	q, e := boardQueryPeriod(kind, period)
	if e != nil || page < 1 || page > 100000 {
		return Board{}, ErrInvalid
	}
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return Board{}, ErrUnavailable
	}
	defer tx.Rollback()
	var admission int
	if e = tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`); e != nil {
		return Board{}, ErrUnavailable
	}
	var cache struct {
		Data  []byte    `db:"snapshot_json"`
		Built time.Time `db:"built_at"`
	}
	e = tx.GetContext(ctx, &cache, `SELECT snapshot_json,built_at FROM global_board_cache WHERE kind=? AND period=? FOR UPDATE`, kind, period)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Board{}, ErrUnavailable
	}
	rows := []BoardRow{}
	now := s.now().UTC()
	fresh := e == nil && now.Sub(cache.Built) >= 0 && now.Sub(cache.Built) < 30*time.Second
	if fresh {
		if json.Unmarshal(cache.Data, &rows) != nil {
			return Board{}, ErrUnavailable
		}
	} else {
		if e = tx.SelectContext(ctx, &rows, q); e != nil {
			return Board{}, ErrUnavailable
		}
		assignRanks(rows)
		cache.Built = now
		if _, e = tx.ExecContext(ctx, `INSERT INTO global_board_cache(kind,period,snapshot_json,built_at) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE snapshot_json=VALUES(snapshot_json),built_at=VALUES(built_at)`, kind, period, marshal(rows), now); e != nil {
			return Board{}, ErrUnavailable
		}
	}
	b := Board{Kind: kind, Period: period, Built: cache.Built, Items: []BoardRow{}, Total: len(rows), Page: page}
	start := (page - 1) * 50
	if start < len(rows) {
		end := min(start+50, len(rows))
		b.Items = rows[start:end]
	}
	return b, fail(tx.Commit())
}

type ProblemStats struct {
	ProblemID   uint64  `json:"problem_id"`
	Submissions int64   `db:"submissions" json:"submissions"`
	Accepted    int64   `db:"accepted" json:"accepted"`
	People      int64   `db:"people" json:"people"`
	Rate        float64 `json:"acceptance_rate"`
	Solved      bool    `json:"solved"`
}

func (s *Service) Stats(ctx context.Context, user, problem uint64) (ProblemStats, error) {
	tx, e := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return ProblemStats{}, ErrUnavailable
	}
	defer tx.Rollback()
	if e = publicProblem(ctx, tx, problem); e != nil {
		return ProblemStats{}, e
	}
	stats := ProblemStats{ProblemID: problem}
	e = tx.GetContext(ctx, &stats, `SELECT COUNT(*) AS submissions,COALESCE(SUM(s.status='Accepted'),0) AS accepted,COUNT(DISTINCT IF(s.status='Accepted',pa.person_id,NULL)) AS people FROM submissions s JOIN users u ON u.id=s.user_id JOIN person_accounts pa ON pa.user_id=u.id JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation AND r.status=s.status WHERE s.problem_id=? AND s.contest_id IS NULL AND s.kind='Submit' AND u.account_type='permanent' AND u.status='active' AND s.status NOT IN ('SystemError','Cancelled')`, problem)
	if e != nil {
		return stats, ErrUnavailable
	}
	if stats.Submissions > 0 {
		stats.Rate = float64(stats.Accepted) / float64(stats.Submissions)
	}
	if user != 0 {
		e = tx.GetContext(ctx, &stats.Solved, `SELECT EXISTS(SELECT 1 FROM (`+eligibleAC+`) ac JOIN person_accounts pa ON pa.person_id=ac.person_id WHERE pa.user_id=? AND ac.problem_id=?)`, user, problem)
		if e != nil {
			return stats, ErrUnavailable
		}
	}
	return stats, fail(tx.Commit())
}
