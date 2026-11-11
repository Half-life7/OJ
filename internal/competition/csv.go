package competition

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"biue/internal/auth"

	"github.com/jmoiron/sqlx"
)

type ImportRow struct {
	Line         int    `json:"line"`
	Username     string `json:"username"`
	UserID       uint64 `json:"user_id"`
	PersonID     uint64 `json:"person_id"`
	Revision     uint64 `json:"revision"`
	DisplayName  string `json:"display_name"`
	StudentNo    string `json:"student_no"`
	Organization string `json:"organization"`
	Team         string `json:"team"`
	Type         string `json:"participant_type"`
	NewAccount   bool   `json:"new_account"`
	Password     string `json:"initial_password,omitempty"`
	PasswordHash string `json:"-"`
	Error        string `json:"error,omitempty"`
}

type ImportPreview struct {
	Token   string          `json:"token"`
	Rows    []ImportRow     `json:"rows"`
	Columns []ColumnMapping `json:"columns"`
	Valid   bool            `json:"valid"`
}

type CSVConfirmResult struct {
	Rows            int `json:"rows"`
	Teams           int `json:"teams"`
	CreatedAccounts int `json:"created_accounts"`
}

type ColumnMapping struct {
	Source   string `json:"source"`
	Field    string `json:"field"`
	Required bool   `json:"required"`
}

type storedImportRow struct {
	Line         int    `json:"line"`
	Username     string `json:"username"`
	UserID       uint64 `json:"user_id"`
	PersonID     uint64 `json:"person_id"`
	Revision     uint64 `json:"revision"`
	DisplayName  string `json:"display_name"`
	StudentNo    string `json:"student_no"`
	Organization string `json:"organization"`
	Team         string `json:"team"`
	Type         string `json:"participant_type"`
	NewAccount   bool   `json:"new_account"`
	PasswordHash string `json:"password_hash,omitempty"`
	Error        string `json:"error,omitempty"`
}

var rosterHeaderAliases = map[string]string{
	"username": "username", "user": "username", "account": "username", "账号": "username", "用户名": "username", "账号id": "username",
	"display_name": "display_name", "name": "display_name", "姓名": "display_name", "显示名称": "display_name",
	"student_no": "student_no", "student": "student_no", "学号": "student_no",
	"organization": "organization", "school": "organization", "学校": "organization", "组织": "organization", "学校或组织": "organization",
	"team": "team", "team_name": "team", "队伍": "team", "队名": "team",
	"participant_type": "participant_type", "type": "participant_type", "类型": "participant_type", "选手类型": "participant_type",
}

func parseRoster(raw string) ([]map[string]string, []ColumnMapping, error) {
	if len(raw) > 256<<10 || !utf8.ValidString(raw) {
		return nil, nil, ErrInvalid
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff")))
	headers, err := r.Read()
	if err != nil {
		return nil, nil, ErrInvalid
	}
	seen := map[string]bool{}
	columns := make([]ColumnMapping, 0, len(headers))
	for i, header := range headers {
		source := strings.TrimSpace(header)
		canonical := rosterHeaderAliases[strings.ToLower(source)]
		if canonical == "" || seen[canonical] {
			return nil, nil, ErrInvalid
		}
		seen[canonical] = true
		headers[i] = canonical
		columns = append(columns, ColumnMapping{Source: source, Field: canonical, Required: canonical == "username"})
	}
	if !seen["username"] {
		return nil, nil, ErrInvalid
	}
	rows := []map[string]string{}
	for {
		fields, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(rows) >= 500 || len(fields) != len(headers) {
			return nil, nil, ErrInvalid
		}
		row := map[string]string{}
		for i, value := range fields {
			row[headers[i]] = strings.TrimSpace(value)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, ErrInvalid
	}
	return rows, columns, nil
}

func resolveRoster(ctx context.Context, tx *sqlx.Tx, c Contest, raw string) ([]ImportRow, bool, []ColumnMapping, error) {
	source, columns, err := parseRoster(raw)
	if err != nil {
		return nil, false, nil, err
	}
	rows, valid, err := resolveRosterValues(ctx, tx, c, source)
	return rows, valid, columns, err
}

func resolveRosterValues(ctx context.Context, tx *sqlx.Tx, c Contest, source []map[string]string) ([]ImportRow, bool, error) {
	rows := make([]ImportRow, 0, len(source))
	seenUsers := map[uint64]bool{}
	seenPersons := map[uint64]bool{}
	seenUsernames := map[string]bool{}
	seenStudents := map[string]bool{}
	teams := map[string]string{}
	teamSizes := map[string]int{}
	valid := true
	for i, line := range source {
		row := ImportRow{
			Line:         i + 2,
			Username:     strings.TrimSpace(line["username"]),
			DisplayName:  strings.TrimSpace(line["display_name"]),
			StudentNo:    strings.TrimSpace(line["student_no"]),
			Organization: strings.TrimSpace(line["organization"]),
			Team:         strings.TrimSpace(line["team"]),
			Type:         strings.TrimSpace(line["participant_type"]),
		}
		if row.Type == "" {
			row.Type = "official"
		}
		normalized, usernameErr := auth.TemporaryUsername(row.Username)
		if usernameErr != nil {
			row.Error = "账号格式不合法"
		} else {
			row.Username = normalized
		}
		if row.Team == "" {
			row.Team = row.Username
		}
		if seenUsernames[row.Username] {
			row.Error = "CSV 内账号重复"
		}
		seenUsernames[row.Username] = true

		if usernameErr == nil {
			var account struct {
				User         uint64 `db:"user_id"`
				Person       uint64 `db:"person_id"`
				Revision     uint64 `db:"revision"`
				Name         string `db:"display_name"`
				Student      string `db:"student_no"`
				Organization string `db:"organization"`
				Kind         string `db:"account_type"`
				Status       string `db:"status"`
			}
			err := tx.GetContext(ctx, &account, `SELECT u.id AS user_id,pa.person_id,p.revision,p.display_name,COALESCE(p.student_no,'') AS student_no,p.organization,u.account_type,u.status FROM users u JOIN person_accounts pa ON pa.user_id=u.id JOIN persons p ON p.id=pa.person_id WHERE u.username=?`, row.Username)
			switch {
			case err == nil:
				resolveExistingRosterRow(ctx, tx, c, &row, account, seenUsers, seenPersons)
			case err == sql.ErrNoRows:
				resolveNewRosterRow(ctx, tx, c, &row, seenPersons, seenStudents)
			default:
				return nil, false, err
			}
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

func resolveExistingRosterRow(ctx context.Context, tx *sqlx.Tx, c Contest, row *ImportRow, account struct {
	User         uint64 `db:"user_id"`
	Person       uint64 `db:"person_id"`
	Revision     uint64 `db:"revision"`
	Name         string `db:"display_name"`
	Student      string `db:"student_no"`
	Organization string `db:"organization"`
	Kind         string `db:"account_type"`
	Status       string `db:"status"`
}, seenUsers, seenPersons map[uint64]bool) {
	row.UserID, row.PersonID, row.Revision = account.User, account.Person, account.Revision
	if row.DisplayName == "" {
		row.DisplayName = account.Name
	}
	if row.StudentNo == "" {
		row.StudentNo = account.Student
	}
	if row.Organization == "" {
		row.Organization = account.Organization
	}
	switch {
	case account.Status != "active":
		row.Error = "账号未启用"
	case account.Kind != "permanent":
		row.Error = "已存在的比赛账号不能再次导入"
	case seenUsers[account.User] || seenPersons[account.Person]:
		row.Error = "CSV 内重复账号或身份"
	case row.DisplayName != account.Name:
		row.Error = "姓名与既有身份不一致"
	case row.StudentNo != account.Student:
		row.Error = "学号冲突，请先进行身份纠错"
	case row.Organization != account.Organization:
		row.Error = "学校或组织不一致"
	}
	var exists int
	if err := tx.GetContext(ctx, &exists, `SELECT COUNT(*) FROM contest_team_members WHERE contest_id=? AND (user_id=? OR person_id=?)`, c.ID, account.User, account.Person); err != nil {
		row.Error = "无法核对比赛身份"
	} else if exists != 0 {
		row.Error = "账号或身份已在比赛中"
	}
	seenUsers[account.User] = true
	seenPersons[account.Person] = true
}

func resolveNewRosterRow(ctx context.Context, tx *sqlx.Tx, c Contest, row *ImportRow, seenPersons map[uint64]bool, seenStudents map[string]bool) {
	row.NewAccount = true
	if !clean(row.DisplayName, 100) {
		row.Error = "新比赛账号必须填写有效姓名"
	}
	if !clean(row.StudentNo, 64) {
		row.Error = "新比赛账号必须填写有效学号"
	}
	if row.Organization != "" && !clean(row.Organization, 200) {
		row.Error = "学校或组织不合法"
	}
	if seenStudents[row.StudentNo] {
		row.Error = "CSV 内学号重复"
	}
	seenStudents[row.StudentNo] = true
	var person struct {
		ID           uint64 `db:"id"`
		Name         string `db:"display_name"`
		StudentNo    string `db:"student_no"`
		Organization string `db:"organization"`
	}
	err := tx.GetContext(ctx, &person, `SELECT id,display_name,student_no,organization FROM persons WHERE student_no=?`, row.StudentNo)
	if err == nil {
		row.PersonID = person.ID
		if row.DisplayName != person.Name {
			row.Error = "姓名与该学号的既有身份不一致"
		}
		if row.Organization != "" && row.Organization != person.Organization {
			row.Error = "学校或组织与既有身份不一致"
		}
		if seenPersons[person.ID] {
			row.Error = "CSV 内重复身份"
		}
		seenPersons[person.ID] = true
		var exists int
		if membershipErr := tx.GetContext(ctx, &exists, `SELECT COUNT(*) FROM contest_team_members WHERE contest_id=? AND person_id=?`, c.ID, person.ID); membershipErr != nil {
			row.Error = "无法核对比赛身份"
		} else if exists != 0 {
			row.Error = "该学号已在比赛中"
		}
	} else if err != sql.ErrNoRows {
		row.Error = "无法核对学号"
	}
}

func ensureImportCredentials(rows []ImportRow) error {
	indices := []int{}
	for i := range rows {
		if rows[i].Error == "" && rows[i].NewAccount {
			indices = append(indices, i)
		}
	}
	if len(indices) > 200 {
		return ErrInvalid
	}
	if len(indices) == 0 {
		return nil
	}
	jobs := make(chan int)
	errs := make(chan error, len(indices))
	var workers sync.WaitGroup
	for worker := 0; worker < min(8, len(indices)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				password, err := generatedPassword()
				if err != nil {
					errs <- err
					continue
				}
				name, hash, err := auth.TemporaryCredentials(rows[index].Username, password)
				if err != nil {
					errs <- ErrUnavailable
					continue
				}
				rows[index].Username = name
				rows[index].Password = password
				rows[index].PasswordHash = hash
			}
		}()
	}
	for _, index := range indices {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func generatedPassword() (string, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
}

func encodeStoredRows(rows []ImportRow) ([]byte, error) {
	stored := make([]storedImportRow, len(rows))
	for i, row := range rows {
		stored[i] = storedImportRow{
			Line: row.Line, Username: row.Username, UserID: row.UserID, PersonID: row.PersonID,
			Revision: row.Revision, DisplayName: row.DisplayName, StudentNo: row.StudentNo,
			Organization: row.Organization, Team: row.Team, Type: row.Type,
			NewAccount: row.NewAccount, PasswordHash: row.PasswordHash, Error: row.Error,
		}
	}
	return json.Marshal(stored)
}

func decodeStoredRows(data []byte) ([]ImportRow, error) {
	var stored []storedImportRow
	if json.Unmarshal(data, &stored) != nil {
		return nil, ErrUnavailable
	}
	rows := make([]ImportRow, len(stored))
	for i, row := range stored {
		rows[i] = ImportRow{
			Line: row.Line, Username: row.Username, UserID: row.UserID, PersonID: row.PersonID,
			Revision: row.Revision, DisplayName: row.DisplayName, StudentNo: row.StudentNo,
			Organization: row.Organization, Team: row.Team, Type: row.Type,
			NewAccount: row.NewAccount, PasswordHash: row.PasswordHash, Error: row.Error,
		}
	}
	return rows, nil
}

func rosterSource(rows []ImportRow) []map[string]string {
	source := make([]map[string]string, len(rows))
	for i, row := range rows {
		source[i] = map[string]string{
			"username": row.Username, "display_name": row.DisplayName, "student_no": row.StudentNo,
			"organization": row.Organization, "team": row.Team, "participant_type": row.Type,
		}
	}
	return source
}

func sameRosterFields(a, b []ImportRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Line != b[i].Line || a[i].Username != b[i].Username || a[i].UserID != b[i].UserID ||
			a[i].PersonID != b[i].PersonID || a[i].Revision != b[i].Revision ||
			a[i].DisplayName != b[i].DisplayName || a[i].StudentNo != b[i].StudentNo ||
			a[i].Organization != b[i].Organization || a[i].Team != b[i].Team || a[i].Type != b[i].Type ||
			a[i].NewAccount != b[i].NewAccount || a[i].Error != b[i].Error {
			return false
		}
	}
	return true
}

func (s *Service) PreviewCSV(ctx context.Context, a auth.User, id uint64, raw string) (ImportPreview, error) {
	preview := ImportPreview{}
	err := s.write(ctx, a, func(tx *sqlx.Tx, actor auth.User) error {
		c, err := contest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = allowed(ctx, tx, actor, id); err != nil {
			return err
		}
		if !s.now().Before(c.End) || c.Settled != nil {
			return ErrConflict
		}
		preview.Rows, preview.Valid, preview.Columns, err = resolveRoster(ctx, tx, c, raw)
		if err != nil {
			return err
		}
		if err = ensureImportCredentials(preview.Rows); err != nil {
			return err
		}
		var token [16]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		preview.Token = hex.EncodeToString(token[:])
		stored, err := encodeStoredRows(preview.Rows)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO contest_imports(token,contest_id,actor_id,csv_data,preview,expires_at) VALUES (?,?,?,?,?,?)`, preview.Token, id, actor.ID, string(stored), string(stored), s.now().UTC().Add(15*time.Minute)); err != nil {
			return err
		}
		return audit(ctx, tx, actor.ID, "contest.csv_preview", "contest", id, map[string]any{"rows": len(preview.Rows), "valid": preview.Valid})
	})
	for i := range preview.Rows {
		preview.Rows[i].PasswordHash = ""
	}
	return preview, err
}

func (s *Service) ConfirmCSV(ctx context.Context, a auth.User, id uint64, token string) (CSVConfirmResult, error) {
	var result CSVConfirmResult
	if decoded, err := hex.DecodeString(token); err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != token {
		return result, ErrInvalid
	}
	err := s.write(ctx, a, func(tx *sqlx.Tx, actor auth.User) error {
		c, err := contest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = allowed(ctx, tx, actor, id); err != nil {
			return err
		}
		var saved struct {
			Data    []byte     `db:"csv_data"`
			Expires time.Time  `db:"expires_at"`
			Applied *time.Time `db:"applied_at"`
		}
		err = tx.GetContext(ctx, &saved, `SELECT csv_data,expires_at,applied_at FROM contest_imports WHERE token=? AND contest_id=? AND actor_id=? FOR UPDATE`, token, id, actor.ID)
		if err == sql.ErrNoRows {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if saved.Applied != nil {
			original, err := decodeStoredRows(saved.Data)
			if err != nil {
				return err
			}
			result.Rows = len(original)
			return nil
		}
		if !s.now().Before(saved.Expires) || !s.now().Before(c.End) || c.Settled != nil {
			return ErrConflict
		}
		original, err := decodeStoredRows(saved.Data)
		if err != nil {
			return err
		}
		rows, valid, err := resolveRosterValues(ctx, tx, c, rosterSource(original))
		if err != nil {
			return err
		}
		if !valid || !sameRosterFields(rows, original) {
			return ErrConflict
		}
		for i := range rows {
			if !rows[i].NewAccount {
				continue
			}
			if rows[i].PasswordHash == "" && original[i].PasswordHash != "" {
				rows[i].PasswordHash = original[i].PasswordHash
			}
			if rows[i].PasswordHash == "" {
				return ErrConflict
			}
			if rows[i].PersonID == 0 {
				inserted, err := tx.ExecContext(ctx, `INSERT INTO persons(display_name,student_no,student_no_status,organization,created_at,updated_at) VALUES (?,?, 'verified',?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, rows[i].DisplayName, rows[i].StudentNo, rows[i].Organization)
				if err != nil {
					return err
				}
				personID, err := inserted.LastInsertId()
				if err != nil || personID <= 0 {
					return ErrUnavailable
				}
				rows[i].PersonID = uint64(personID)
			}
			inserted, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,account_type,global_role,status,token_version,created_at,updated_at) VALUES (?,?,'contest','user','active',1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, rows[i].Username, rows[i].PasswordHash)
			if err != nil {
				return err
			}
			userID, err := inserted.LastInsertId()
			if err != nil || userID <= 0 {
				return ErrUnavailable
			}
			rows[i].UserID = uint64(userID)
			if _, err = tx.ExecContext(ctx, `INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (?,?,UTC_TIMESTAMP(6))`, rows[i].UserID, rows[i].PersonID); err != nil {
				return err
			}
			result.CreatedAccounts++
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
			if _, err = s.addTeam(ctx, tx, c, team, groups[team], "active", types[team]); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE contest_imports SET applied_at=UTC_TIMESTAMP(6) WHERE token=?`, token); err != nil {
			return err
		}
		result.Rows, result.Teams = len(rows), len(order)
		return audit(ctx, tx, actor.ID, "contest.csv_confirm", "contest", id, map[string]any{"rows": result.Rows, "teams": result.Teams, "created_accounts": result.CreatedAccounts})
	})
	return result, err
}

func spreadsheetCell(s string) string {
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
