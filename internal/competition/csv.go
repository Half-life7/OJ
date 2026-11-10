package competition

import (
	"biue/internal/auth"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

type ImportRow struct {
	Line     int    `json:"line"`
	Username string `json:"username"`
	UserID   uint64 `json:"user_id"`
	PersonID uint64 `json:"person_id"`
	Revision uint64 `json:"revision"`
	Team     string `json:"team"`
	Type     string `json:"participant_type"`
	Error    string `json:"error,omitempty"`
}
type ImportPreview struct {
	Token string      `json:"token"`
	Rows  []ImportRow `json:"rows"`
	Valid bool        `json:"valid"`
}

func parseRoster(raw string) ([]map[string]string, error) {
	if len(raw) > 256<<10 || !utf8.ValidString(raw) {
		return nil, ErrInvalid
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff")))
	headers, e := r.Read()
	if e != nil {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for i, h := range headers {
		h = strings.ToLower(strings.TrimSpace(h))
		switch h {
		case "username", "display_name", "student_no", "organization", "team", "participant_type":
		default:
			return nil, ErrInvalid
		}
		if seen[h] {
			return nil, ErrInvalid
		}
		seen[h] = true
		headers[i] = h
	}
	if !seen["username"] {
		return nil, ErrInvalid
	}
	rows := []map[string]string{}
	for {
		fields, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(rows) >= 500 {
			return nil, ErrInvalid
		}
		row := map[string]string{}
		for i, v := range fields {
			row[headers[i]] = strings.TrimSpace(v)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, ErrInvalid
	}
	return rows, nil
}
func resolveRoster(ctx context.Context, tx *sqlx.Tx, c Contest, raw string) ([]ImportRow, bool, error) {
	source, e := parseRoster(raw)
	if e != nil {
		return nil, false, e
	}
	rows := []ImportRow{}
	seenUsers, seenPersons := map[uint64]bool{}, map[uint64]bool{}
	teams := map[string]string{}
	teamSizes := map[string]int{}
	valid := true
	for i, line := range source {
		row := ImportRow{Line: i + 2, Username: line["username"], Team: line["team"], Type: line["participant_type"]}
		if row.Team == "" {
			row.Team = row.Username
		}
		if row.Type == "" {
			row.Type = "official"
		}
		var p struct {
			User         uint64 `db:"user_id"`
			Person       uint64 `db:"person_id"`
			Revision     uint64 `db:"revision"`
			Name         string `db:"display_name"`
			Student      string `db:"student_no"`
			Organization string `db:"organization"`
			Kind         string `db:"account_type"`
			Status       string `db:"status"`
		}
		e = tx.GetContext(ctx, &p, `SELECT u.id AS user_id,pa.person_id,p.revision,p.display_name,COALESCE(p.student_no,'') AS student_no,p.organization,u.account_type,u.status FROM users u JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE u.username=?`, row.Username)
		if e == sql.ErrNoRows {
			row.Error = "账号不存在"
		} else if e != nil {
			return nil, false, e
		} else {
			row.UserID, row.PersonID, row.Revision = p.User, p.Person, p.Revision
			switch {
			case p.Status != "active":
				row.Error = "账号未启用"
			case p.Kind != "permanent":
				row.Error = "临时账号请使用本场比赛的账号管理"
			case seenUsers[p.User] || seenPersons[p.Person]:
				row.Error = "重复账号或身份"
			case line["display_name"] != "" && line["display_name"] != p.Name:
				row.Error = "姓名与既有身份不一致"
			case line["student_no"] != "" && line["student_no"] != p.Student:
				row.Error = "学号冲突，请先进行身份纠错"
			case line["organization"] != "" && line["organization"] != p.Organization:
				row.Error = "学校或组织不一致"
			}
			var exists int
			if e = tx.GetContext(ctx, &exists, `SELECT COUNT(*) FROM contest_team_members WHERE contest_id=? AND (user_id=? OR person_id=?)`, c.ID, p.User, p.Person); e != nil {
				return nil, false, e
			}
			if exists != 0 {
				row.Error = "账号或身份已在比赛中"
			}
			seenUsers[p.User] = true
			seenPersons[p.Person] = true
		}
		if !clean(row.Team, 100) || row.Type != "official" && row.Type != "star" {
			row.Error = "队名或选手类型不合法"
		}
		if previous, ok := teams[row.Team]; ok && previous != row.Type {
			row.Error = "同队选手类型不一致"
		}
		teams[row.Team] = row.Type
		teamSizes[row.Team]++
		if teamSizes[row.Team] > 20 || !c.TeamMode && teamSizes[row.Team] > 1 {
			row.Error = "队伍人数不符合比赛设置"
		}
		valid = valid && row.Error == ""
		rows = append(rows, row)
	}
	return rows, valid, nil
}
func (s *Service) PreviewCSV(ctx context.Context, a auth.User, id uint64, raw string) (ImportPreview, error) {
	preview := ImportPreview{}
	e := s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, e := contest(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = allowed(ctx, tx, a, id); e != nil {
			return e
		}
		if !s.now().Before(c.Start) || c.Settled != nil {
			return ErrConflict
		}
		preview.Rows, preview.Valid, e = resolveRoster(ctx, tx, c, raw)
		if e != nil {
			return e
		}
		var token [16]byte
		if _, e = rand.Read(token[:]); e != nil {
			return e
		}
		preview.Token = hex.EncodeToString(token[:])
		b, e := json.Marshal(preview.Rows)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO contest_imports(token,contest_id,actor_id,csv_data,preview,expires_at) VALUES (?,?,?,?,?,?)`, preview.Token, id, a.ID, raw, string(b), s.now().UTC().Add(15*time.Minute)); e != nil {
			return e
		}
		return audit(ctx, tx, a.ID, "contest.csv_preview", "contest", id, map[string]any{"rows": len(preview.Rows), "valid": preview.Valid})
	})
	return preview, e
}
func (s *Service) ConfirmCSV(ctx context.Context, a auth.User, id uint64, token string) error {
	if b, e := hex.DecodeString(token); e != nil || len(b) != 16 || hex.EncodeToString(b) != token {
		return ErrInvalid
	}
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, e := contest(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = allowed(ctx, tx, a, id); e != nil {
			return e
		}
		var saved struct {
			CSV     string     `db:"csv_data"`
			Preview []byte     `db:"preview"`
			Expires time.Time  `db:"expires_at"`
			Applied *time.Time `db:"applied_at"`
		}
		e = tx.GetContext(ctx, &saved, `SELECT csv_data,preview,expires_at,applied_at FROM contest_imports WHERE token=? AND contest_id=? AND actor_id=? FOR UPDATE`, token, id, a.ID)
		if e == sql.ErrNoRows {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if saved.Applied != nil {
			return nil
		}
		if !s.now().Before(saved.Expires) || !s.now().Before(c.Start) || c.Settled != nil {
			return ErrConflict
		}
		rows, valid, e := resolveRoster(ctx, tx, c, saved.CSV)
		if e != nil {
			return e
		}
		var original []ImportRow
		if json.Unmarshal(saved.Preview, &original) != nil {
			return ErrUnavailable
		}
		if !valid || !reflect.DeepEqual(rows, original) {
			return ErrConflict
		}
		groups := map[string][]uint64{}
		types := map[string]string{}
		order := []string{}
		for _, row := range rows {
			if _, ok := groups[row.Team]; !ok {
				order = append(order, row.Team)
			}
			groups[row.Team] = append(groups[row.Team], row.UserID)
			types[row.Team] = row.Type
		}
		for _, team := range order {
			if _, e = s.addTeam(ctx, tx, c, team, groups[team], "active", types[team]); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE contest_imports SET applied_at=UTC_TIMESTAMP(6) WHERE token=?`, token); e != nil {
			return e
		}
		return audit(ctx, tx, a.ID, "contest.csv_confirm", "contest", id, map[string]any{"rows": len(rows), "teams": len(order)})
	})
}

func spreadsheetCell(s string) string {
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
