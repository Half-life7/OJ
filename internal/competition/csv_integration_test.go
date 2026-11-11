package competition

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"biue/internal/auth"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func TestCSVImportCreatesTemporaryAccountsWithRealMySQL(t *testing.T) {
	dsn := os.Getenv("BIUE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BIUE_TEST_MYSQL_DSN is not configured")
	}
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var locked int
	if err := db.Get(&locked, `SELECT GET_LOCK('biue_integration_mysql',30)`); err != nil || locked != 1 {
		t.Fatalf("integration lock: %v %d", err, locked)
	}
	defer db.Exec(`SELECT RELEASE_LOCK('biue_integration_mysql')`)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`SET FOREIGN_KEY_CHECKS=0`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"contest_points", "contest_snapshots", "contest_freezes", "contest_imports",
		"contest_team_members", "contest_problems", "contest_admins", "contest_teams",
		"community_documents", "problem_list_items", "problem_list_favorites",
		"discussion_likes", "community_write_limits", "submission_case_results",
		"submission_results", "judge_task_outbox", "judge_attempt_failures",
		"judge_dead_letters", "judge_tasks", "submissions", "rejudge_requests",
		"testcases", "problem_data_versions", "problem_versions", "problem_tags",
		"tags", "problems", "global_board_cache", "audit_logs", "student_no_history",
		"auth_sessions", "person_accounts", "contests", "users", "persons",
	} {
		exec("DELETE FROM " + table)
	}
	if _, err := db.Exec(`SET FOREIGN_KEY_CHECKS=1`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	exec(`INSERT INTO persons(id,display_name,student_no,student_no_status,created_at,updated_at) VALUES (1,'Admin',NULL,'none',?,?)`, now, now)
	exec(`INSERT INTO users(id,username,password_hash,account_type,global_role,status,created_at,updated_at) VALUES (1,'admin_user','$2a$12$abcdefghijklmnopqrstuv','permanent','super_admin','active',?,?)`, now, now)
	exec(`INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (1,1,?)`, now)
	exec(`INSERT INTO contests(id,title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,created_by,created_at) VALUES (1,'CSV Contest','OI','admin_add',0,0,?,?,1,?)`,
		now.Add(-time.Hour), now.Add(time.Hour), now)

	service := New(db)
	service.now = func() time.Time { return now }
	actor := auth.User{ID: 1, AccountType: "permanent", GlobalRole: "super_admin", Status: "active"}
	preview, err := service.PreviewCSV(context.Background(), actor, 1, "username,display_name,student_no,team,participant_type\nexam001,张三,S999,Team A,official\n")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Valid || len(preview.Rows) != 1 || !preview.Rows[0].NewAccount || preview.Rows[0].Password == "" || preview.Rows[0].PasswordHash != "" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	var stored string
	if err := db.Get(&stored, `SELECT csv_data FROM contest_imports WHERE token=?`, preview.Token); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, preview.Rows[0].Password) || !strings.Contains(stored, "password_hash") {
		t.Fatalf("stored import did not preserve only the password hash: %s", stored)
	}
	result, err := service.ConfirmCSV(context.Background(), actor, 1, preview.Token)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedAccounts != 1 || result.Rows != 1 || result.Teams != 1 {
		t.Fatalf("unexpected confirm result: %+v", result)
	}
	var account struct {
		UserID    uint64 `db:"user_id"`
		Account   string `db:"account_type"`
		StudentNo string `db:"student_no"`
		Team      string `db:"name"`
	}
	err = db.Get(&account, `SELECT u.id AS user_id,u.account_type,p.student_no,t.name
		FROM users u
		JOIN person_accounts pa ON pa.user_id=u.id
		JOIN persons p ON p.id=pa.person_id
		JOIN contest_team_members m ON m.user_id=u.id
		JOIN contest_teams t ON t.id=m.team_id AND t.contest_id=m.contest_id
		WHERE u.username='exam001' AND m.contest_id=1`)
	if err != nil {
		t.Fatal(err)
	}
	if account.Account != "contest" || account.StudentNo != "S999" || account.Team != "Team A" {
		t.Fatalf("temporary account was not linked by student identity: %+v", account)
	}
	exec(`INSERT INTO contests(id,title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,created_by,created_at) VALUES (2,'Running','OI','admin_add',0,0,?,?,1,?)`,
		now.Add(-time.Hour), now.Add(time.Hour), now)
	userID, err := service.Temporary(context.Background(), actor, 2, Temporary{
		Username: "exam002", Password: "another-password", Name: "张三", Student: "S999", Organization: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	var samePerson int
	if err := db.Get(&samePerson, `SELECT COUNT(*) FROM person_accounts pa
		JOIN person_accounts first_account ON first_account.person_id=pa.person_id
		WHERE pa.user_id=? AND first_account.user_id=?`, userID, account.UserID); err != nil {
		t.Fatal(err)
	}
	if samePerson != 1 {
		t.Fatalf("manual temporary account did not reuse the student identity: %d", samePerson)
	}
}
