package community

import (
	"context"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func TestContestBoardWithRealMySQL(t *testing.T) {
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

	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
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
	exec(`INSERT INTO persons(id,display_name,student_no,student_no_status,created_at,updated_at) VALUES
		(1,'Formal One','S001','verified',?,?),
		(2,'Formal Zero','S002','verified',?,?),
		(3,'Contest Only','S003','verified',?,?)`, now, now, now, now, now, now)
	exec(`INSERT INTO users(id,username,password_hash,account_type,global_role,status,created_at,updated_at) VALUES
		(1,'formal_one','$2a$12$abcdefghijklmnopqrstuv','permanent','user','active',?,?),
		(2,'contest_one','$2a$12$abcdefghijklmnopqrstuv','contest','user','active',?,?),
		(3,'formal_zero','$2a$12$abcdefghijklmnopqrstuv','permanent','user','active',?,?),
		(4,'contest_only','$2a$12$abcdefghijklmnopqrstuv','contest','user','active',?,?)`,
		now, now, now, now, now, now, now, now)
	exec(`INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (1,1,?),(2,1,?),(3,2,?),(4,3,?)`, now, now, now, now)
	exec(`INSERT INTO contests(id,title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,created_by,created_at,settled_at) VALUES
		(1,'Finished A','OI','admin_add',0,0,?,?,1,?,?),
		(2,'Finished B','OI','admin_add',0,0,?,?,1,?,?),
		(3,'Unsettled','OI','admin_add',0,0,?,?,1,?,NULL)`,
		now.Add(-3*time.Hour), now.Add(-2*time.Hour), now, now,
		now.Add(-2*time.Hour), now.Add(-time.Hour), now, now,
		now.Add(-time.Hour), now.Add(time.Hour), now)
	exec(`INSERT INTO contest_teams(id,contest_id,name,participant_type,status,created_at) VALUES
		(1,1,'A','official','active',?),
		(2,2,'B','official','active',?),
		(3,3,'C','official','active',?),
		(4,1,'Only','official','active',?)`, now, now, now, now)
	exec(`INSERT INTO contest_team_members(contest_id,team_id,user_id,person_id,identity_snapshot) VALUES
		(1,1,2,1,'{}'),
		(2,2,1,1,'{}'),
		(3,3,2,1,'{}'),
		(1,4,4,3,'{}')`)
	exec(`INSERT INTO contest_points(contest_id,person_id,team_id,points) VALUES
		(1,1,1,100),
		(2,1,2,80),
		(3,1,3,50),
		(1,3,4,40)`)

	service := New(db)
	service.now = func() time.Time { return now }
	query, err := boardQueryPeriod("contest", "all")
	if err != nil {
		t.Fatal(err)
	}
	rawRows := []BoardRow{}
	if err := db.SelectContext(context.Background(), &rawRows, query); err != nil {
		t.Fatalf("contest board query failed: %v", err)
	}
	board, err := service.BoardPeriod(context.Background(), "contest", "all", 1)
	if err != nil {
		t.Fatal(err)
	}
	if board.Total != 3 {
		t.Fatalf("expected two formal students plus one contest-only identity, got %+v", board.Items)
	}
	if len(board.Items) != 3 || board.Items[0].StudentNo == nil || *board.Items[0].StudentNo != "S001" ||
		board.Items[0].Value != 180 || board.Items[0].Participations != 2 {
		t.Fatalf("linked accounts did not aggregate by student number: %+v", board.Items)
	}
	var zero *BoardRow
	for i := range board.Items {
		if board.Items[i].StudentNo != nil && *board.Items[i].StudentNo == "S002" {
			zero = &board.Items[i]
			break
		}
	}
	if zero == nil || zero.Value != 0 || zero.Participations != 0 || zero.Reached != nil {
		t.Fatalf("zero-participation formal identity missing: %+v", board.Items)
	}
}
