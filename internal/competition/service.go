package competition

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"biue/internal/auth"
	"biue/internal/server"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

var ErrInvalid = errors.New("invalid competition request")
var ErrForbidden = errors.New("competition access forbidden")
var ErrConflict = errors.New("competition state conflict")
var ErrUnavailable = errors.New("competition storage unavailable")

type Service struct {
	db     *sqlx.DB
	now    func() time.Time
	points PointsPolicy
}

func New(db *sqlx.DB) *Service { return &Service{db: db, now: time.Now, points: LinearPoints{}} }
func clean(s string, n int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && strings.IndexFunc(s, unicode.IsControl) < 0
}
func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrConflict) {
		return err
	}
	var e *mysql.MySQLError
	if errors.As(err, &e) && (e.Number == 1062 || e.Number == 1452) {
		return ErrConflict
	}
	return ErrUnavailable
}

// All contest mutations share the submission admission lock. Settlement and
// withdrawal cannot race admissions or administrative rejudges.
func (s *Service) write(ctx context.Context, actor auth.User, fn func(*sqlx.Tx, auth.User) error) error {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	var id int
	if err = tx.GetContext(ctx, &id, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`); err != nil {
		return ErrUnavailable
	}
	var a auth.User
	if err = tx.GetContext(ctx, &a, `SELECT id, username, account_type, global_role, status FROM users WHERE id=? FOR UPDATE`, actor.ID); err != nil {
		return storageError(err)
	}
	if a.Status != "active" {
		return ErrForbidden
	}
	if err = fn(tx, a); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit())
}
func audit(ctx context.Context, tx *sqlx.Tx, actor uint64, action, object string, id uint64, details any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,?,?,?,?,?,UTC_TIMESTAMP(6))`, actor, action, object, id, server.RequestID(ctx), string(b))
	return err
}

type Contest struct {
	ID       uint64     `db:"id" json:"id"`
	Title    string     `db:"title" json:"title"`
	Mode     string     `db:"scoring_mode" json:"scoring_mode"`
	Policy   string     `db:"enrollment_policy" json:"enrollment_policy"`
	Approval bool       `db:"approval_required" json:"approval_required"`
	TeamMode bool       `db:"team_mode" json:"team_mode"`
	Start    time.Time  `db:"starts_at" json:"starts_at"`
	End      time.Time  `db:"ends_at" json:"ends_at"`
	Revision uint64     `db:"revision" json:"revision"`
	Settled  *time.Time `db:"settled_at" json:"settled_at"`
}

const columns = `id,title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,revision,settled_at`

func contest(ctx context.Context, tx *sqlx.Tx, id uint64) (Contest, error) {
	var c Contest
	err := tx.GetContext(ctx, &c, `SELECT `+columns+` FROM contests WHERE id=? FOR UPDATE`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrConflict
	}
	return c, err
}
func allowed(ctx context.Context, tx *sqlx.Tx, a auth.User, id uint64) error {
	if a.GlobalRole == "super_admin" && a.AccountType == "permanent" {
		return nil
	}
	var n int
	err := tx.GetContext(ctx, &n, `SELECT COUNT(*) FROM contest_admins WHERE contest_id=? AND user_id=?`, id, a.ID)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrForbidden
	}
	return nil
}
func (s *Service) List(ctx context.Context, after uint64) ([]Contest, error) {
	items := []Contest{}
	err := s.db.SelectContext(ctx, &items, `SELECT `+columns+` FROM contests WHERE id>? ORDER BY id LIMIT 100`, after)
	return items, storageError(err)
}

type FieldError struct{ Fields map[string]string }

func (e *FieldError) Error() string { return "请检查标记的比赛字段" }
func (e *FieldError) Unwrap() error { return ErrInvalid }

func validateContest(c Contest, now time.Time) error {
	fields := map[string]string{}
	if !clean(c.Title, 200) {
		fields["title"] = "比赛名称需为 1～200 个字符，且不能含控制字符"
	}
	if c.Mode != "OI" && c.Mode != "ACM" && c.Mode != "IOI" {
		fields["scoring_mode"] = "请选择 OI、ACM 或 IOI 计分模式"
	}
	if c.Policy != "open" && c.Policy != "admin_add" {
		fields["enrollment_policy"] = "请选择有效的报名方式"
	}
	if c.Start.IsZero() || !c.Start.After(now) || c.Start.Year() > 9999 {
		fields["starts_at"] = "开始时间必须晚于服务器当前时间，请至少预留几分钟"
	}
	if c.End.IsZero() || !c.End.After(c.Start) || c.End.Year() > 9999 {
		fields["ends_at"] = "结束时间必须晚于开始时间"
	} else if c.End.Sub(c.Start) > 30*24*time.Hour {
		fields["ends_at"] = "比赛持续时间不能超过 30 天"
	}
	if len(fields) > 0 {
		return &FieldError{Fields: fields}
	}
	return nil
}
func (s *Service) Save(ctx context.Context, a auth.User, c Contest) (uint64, error) {
	var id uint64
	err := s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		now := s.now().UTC()
		c.Title = strings.TrimSpace(c.Title)
		if err := validateContest(c, now); err != nil {
			return err
		}
		if c.ID == 0 {
			if a.GlobalRole != "super_admin" || a.AccountType != "permanent" {
				return ErrForbidden
			}
			r, e := tx.ExecContext(ctx, `INSERT INTO contests(title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,created_by,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, c.Title, c.Mode, c.Policy, c.Approval, c.TeamMode, c.Start.UTC(), c.End.UTC(), a.ID, now)
			if e != nil {
				return e
			}
			n, e := r.LastInsertId()
			if e != nil {
				return e
			}
			id = uint64(n)
		} else {
			old, e := contest(ctx, tx, c.ID)
			if e != nil {
				return e
			}
			if e = allowed(ctx, tx, a, c.ID); e != nil {
				return e
			}
			if !now.Before(old.Start) || old.Revision != c.Revision || old.Settled != nil || old.TeamMode != c.TeamMode {
				return ErrConflict
			}
			_, e = tx.ExecContext(ctx, `UPDATE contests SET title=?,scoring_mode=?,enrollment_policy=?,approval_required=?,starts_at=?,ends_at=?,revision=revision+1 WHERE id=?`, c.Title, c.Mode, c.Policy, c.Approval, c.Start.UTC(), c.End.UTC(), c.ID)
			if e != nil {
				return e
			}
			id = c.ID
		}
		return audit(ctx, tx, a.ID, "contest.save", "contest", id, c)
	})
	return id, err
}

type Manage struct {
	TeamIDs   []uint64 `json:"team_ids"`
	Action    string   `json:"action"`
	ProblemID uint64   `json:"problem_id"`
	UserID    uint64   `json:"user_id"`
	TeamID    uint64   `json:"team_id"`
	Name      string   `json:"name"`
	Type      string   `json:"participant_type"`
	Status    string   `json:"status"`
	Members   []uint64 `json:"members"`
}

type UserOption struct {
	ID          uint64  `db:"id" json:"id"`
	Username    string  `db:"username" json:"username"`
	DisplayName string  `db:"display_name" json:"display_name"`
	AccountType string  `db:"account_type" json:"account_type"`
	StudentNo   *string `db:"student_no" json:"student_no"`
}

type ProblemOption struct {
	ID               uint64 `db:"id" json:"id"`
	Title            string `db:"title" json:"title"`
	StatementVersion uint64 `db:"statement_version" json:"statement_version"`
	DataVersion      uint64 `db:"data_version" json:"data_version"`
}

type ManageOptions struct {
	Users    []UserOption    `json:"users"`
	Admins   []UserOption    `json:"admins"`
	Problems []ProblemOption `json:"problems"`
}

func (s *Service) ManageOptions(ctx context.Context, a auth.User, id uint64, query string) (ManageOptions, error) {
	query = strings.TrimSpace(query)
	if id == 0 || utf8.RuneCountInString(query) > 100 || strings.ContainsFunc(query, unicode.IsControl) {
		return ManageOptions{}, ErrInvalid
	}
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManageOptions{}, ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = loadContest(ctx, tx, id); errors.Is(err, sql.ErrNoRows) {
		return ManageOptions{}, ErrConflict
	}
	if err != nil {
		return ManageOptions{}, storageError(err)
	}
	if err = allowed(ctx, tx, a, id); err != nil {
		return ManageOptions{}, err
	}
	like := "%" + strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(query) + "%"
	result := ManageOptions{Users: []UserOption{}, Admins: []UserOption{}, Problems: []ProblemOption{}}
	if err = tx.SelectContext(ctx, &result.Users, `SELECT u.id,u.username,u.account_type,p.display_name,p.student_no FROM users u JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE u.status='active' AND (u.username LIKE ? ESCAPE '=' OR p.display_name LIKE ? ESCAPE '=' OR p.student_no LIKE ? ESCAPE '=') ORDER BY u.id LIMIT 30`, like, like, like); err != nil {
		return ManageOptions{}, storageError(err)
	}
	if err = tx.SelectContext(ctx, &result.Admins, `SELECT u.id,u.username,u.account_type,p.display_name,p.student_no FROM contest_admins ca JOIN users u ON u.id=ca.user_id JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE ca.contest_id=? ORDER BY u.id`, id); err != nil {
		return ManageOptions{}, storageError(err)
	}
	args := []any{like, like}
	where := `(p.title LIKE ? ESCAPE '=' OR CAST(p.id AS CHAR) LIKE ? ESCAPE '=')`
	if problemID, e := strconv.ParseUint(query, 10, 63); e == nil && problemID > 0 {
		where = `(p.id=? OR p.title LIKE ? ESCAPE '=')`
		args = []any{problemID, like}
	}
	if err = tx.SelectContext(ctx, &result.Problems, `SELECT p.id,p.title,p.statement_version,p.data_version FROM problems p WHERE p.status='published' AND p.data_version IS NOT NULL AND `+where+` ORDER BY p.id DESC LIMIT 30`, args...); err != nil {
		return ManageOptions{}, storageError(err)
	}
	if err = tx.Commit(); err != nil {
		return ManageOptions{}, ErrUnavailable
	}
	return result, nil
}

func (s *Service) Manage(ctx context.Context, a auth.User, id uint64, m Manage) error {
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, err := contest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = allowed(ctx, tx, a, id); err != nil {
			return err
		}
		if c.Settled != nil && m.Action != "retire_account" && m.Action != "unfreeze" && m.Action != "grant_admin" && m.Action != "revoke_admin" {
			return ErrConflict
		}
		now := s.now().UTC()
		switch m.Action {
		case "bulk_status":
			if len(m.TeamIDs) < 1 || len(m.TeamIDs) > 500 || m.Status != "active" && m.Status != "withdrawn" || m.Type != "official" && m.Type != "star" {
				return ErrInvalid
			}
			seen := map[uint64]bool{}
			for _, team := range m.TeamIDs {
				if team == 0 || seen[team] {
					return ErrInvalid
				}
				seen[team] = true
				var ok bool
				if err = tx.GetContext(ctx, &ok, `SELECT EXISTS(SELECT 1 FROM contest_teams WHERE contest_id=? AND id=?)`, id, team); err != nil {
					return err
				}
				if !ok {
					return ErrConflict
				}
				if _, err = tx.ExecContext(ctx, `UPDATE contest_teams SET status=?,participant_type=? WHERE contest_id=? AND id=?`, m.Status, m.Type, id, team); err != nil {
					return err
				}
			}
		case "freeze", "unfreeze":
			if now.Before(c.Start) {
				return ErrConflict
			}
			if m.Action == "unfreeze" {
				_, err = tx.ExecContext(ctx, `UPDATE contest_freezes SET ended_at=UTC_TIMESTAMP(6) WHERE contest_id=? AND ended_at IS NULL`, id)
			} else {
				var count int
				if err = tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM contest_freezes WHERE contest_id=? AND ended_at IS NULL`, id); err != nil {
					return err
				}
				if count == 0 {
					rows, e := calculate(ctx, tx, c)
					if e != nil {
						return e
					}
					b, e := json.Marshal(rows)
					if e != nil {
						return e
					}
					_, err = tx.ExecContext(ctx, `INSERT INTO contest_freezes(contest_id,standings,created_at) VALUES (?,?,UTC_TIMESTAMP(6))`, id, string(b))
				}
			}
		case "retire_account":
			if now.Before(c.End) {
				return ErrConflict
			}
			r, e := tx.ExecContext(ctx, `UPDATE users u JOIN contest_team_members m ON m.user_id=u.id SET u.status='retired',u.token_version=u.token_version+1,u.updated_at=UTC_TIMESTAMP(6) WHERE m.contest_id=? AND u.id=? AND u.account_type='contest' AND u.status='active'`, id, m.UserID)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
		case "grant_admin", "revoke_admin":
			if a.GlobalRole != "super_admin" {
				return ErrForbidden
			}
			if m.Action == "grant_admin" {
				var n int
				if err = tx.GetContext(ctx, &n, `SELECT COUNT(*) FROM users WHERE id=? AND account_type='permanent' AND status='active'`, m.UserID); err != nil {
					return err
				}
				if n != 1 {
					return ErrInvalid
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO contest_admins(contest_id,user_id) VALUES (?,?)`, id, m.UserID)
			} else {
				var result sql.Result
				result, err = tx.ExecContext(ctx, `DELETE FROM contest_admins WHERE contest_id=? AND user_id=?`, id, m.UserID)
				if err == nil {
					var affected int64
					affected, err = result.RowsAffected()
					if err == nil && affected != 1 {
						return ErrConflict
					}
				}
			}
		case "add_problem":
			if !now.Before(c.Start) {
				return ErrConflict
			}
			r, e := tx.ExecContext(ctx, `INSERT INTO contest_problems(contest_id,problem_id,statement_version,data_version) SELECT ?,id,statement_version,data_version FROM problems WHERE id=? AND status='published' AND data_version IS NOT NULL`, id, m.ProblemID)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
		case "remove_problem":
			if !now.Before(c.Start) {
				return ErrConflict
			}
			var result sql.Result
			result, err = tx.ExecContext(ctx, `DELETE FROM contest_problems WHERE contest_id=? AND problem_id=?`, id, m.ProblemID)
			if err == nil {
				var affected int64
				affected, err = result.RowsAffected()
				if err == nil && affected != 1 {
					return ErrConflict
				}
			}
		case "add_team":
			if !now.Before(c.Start) {
				return ErrConflict
			}
			_, err = s.addTeam(ctx, tx, c, m.Name, m.Members, "active", m.Type)
		case "team_status":
			if m.Status != "active" && m.Status != "withdrawn" {
				return ErrInvalid
			}
			if m.Type != "official" && m.Type != "star" {
				return ErrInvalid
			}
			r, e := tx.ExecContext(ctx, `UPDATE contest_teams SET status=?,participant_type=? WHERE contest_id=? AND id=?`, m.Status, m.Type, id, m.TeamID)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n == 0 {
				var count int
				if e = tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM contest_teams WHERE contest_id=? AND id=?`, id, m.TeamID); e != nil {
					return e
				}
				if count != 1 {
					return ErrConflict
				}
			}
		default:
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, a.ID, "contest."+m.Action, "contest", id, m)
	})
}

func (s *Service) addTeam(ctx context.Context, tx *sqlx.Tx, c Contest, name string, members []uint64, status, kind string) (uint64, error) {
	if !clean(name, 100) || len(members) < 1 || len(members) > 20 || !c.TeamMode && len(members) != 1 || kind != "official" && kind != "star" {
		return 0, ErrInvalid
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO contest_teams(contest_id,name,participant_type,status,created_at) VALUES (?,?,?,?,UTC_TIMESTAMP(6))`, c.ID, name, kind, status)
	if err != nil {
		return 0, err
	}
	n, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	team := uint64(n)
	for _, user := range members {
		var p struct {
			ID            uint64  `db:"id"`
			Person        uint64  `db:"person_id"`
			Username      string  `db:"username"`
			Name          string  `db:"display_name"`
			Student       *string `db:"student_no"`
			StudentStatus string  `db:"student_no_status"`
			Organization  string  `db:"organization"`
			Kind          string  `db:"account_type"`
		}
		if err = tx.GetContext(ctx, &p, `SELECT u.id,pa.person_id,u.username,p.display_name,p.student_no,p.student_no_status,p.organization,u.account_type FROM users u JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE u.id=? AND u.status='active' FOR SHARE`, user); err != nil {
			return 0, err
		}
		if p.Kind == "contest" {
			var count int
			if err = tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM contest_team_members WHERE user_id=?`, user); err != nil {
				return 0, err
			}
			if count != 0 {
				return 0, ErrForbidden
			}
		}
		snapshot, e := json.Marshal(p)
		if e != nil {
			return 0, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO contest_team_members(contest_id,team_id,user_id,person_id,identity_snapshot) VALUES (?,?,?,?,?)`, c.ID, team, user, p.Person, string(snapshot)); err != nil {
			return 0, err
		}
	}
	return team, nil
}
func (s *Service) Enroll(ctx context.Context, a auth.User, id uint64) error {
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, err := contest(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.AccountType != "permanent" || c.Policy != "open" || c.TeamMode {
			return ErrForbidden
		}
		if !s.now().Before(c.Start) || c.Settled != nil {
			return ErrConflict
		}
		status := "active"
		if c.Approval {
			status = "pending"
		}
		_, err = s.addTeam(ctx, tx, c, a.Username, []uint64{a.ID}, status, "official")
		if err != nil {
			return err
		}
		return audit(ctx, tx, a.ID, "contest.enroll", "contest", id, nil)
	})
}
