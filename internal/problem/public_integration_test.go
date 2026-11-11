package problem

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func TestPublicProblemIsImmediatelyListedWithRealMySQL(t *testing.T) {
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
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`SET FOREIGN_KEY_CHECKS=1`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO persons(id,display_name,student_no_status,created_at,updated_at) VALUES (1,'Admin','none',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,account_type,global_role,status,created_at,updated_at) VALUES (1,'admin','$2a$12$abcdefghijklmnopqrstuv','permanent','super_admin','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (1,1,?)`, now); err != nil {
		t.Fatal(err)
	}

	service, err := New(db, &recordingStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	input := validInput()
	input.Status = "draft"
	input.Visibility = "public"
	created, err := service.Create(context.Background(), Actor{ID: 1, Role: "super_admin"}, input, "request-public-integration")
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "published" || created.Visibility != "public" {
		t.Fatalf("public visibility was not normalized: %+v", created)
	}
	page, err := service.List(context.Background(), Actor{}, ListOptions{Query: input.Title, Page: 1, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("public problem is not immediately listed: %+v", page)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "导入样例", "菲波那契数.zip"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackageZIP(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := service.ImportPackage(context.Background(), Actor{ID: 1, Role: "super_admin"}, pkg, "request-wrapped-zip")
	if err != nil {
		t.Fatalf("wrapped ZIP import failed: %v", err)
	}
	if imported.Title != "菲波那契数" || imported.DataVersion != 1 || imported.TimeLimitMS != 1000 || imported.MemoryLimitMB != 128 || len(pkg.Preview.Cases) != 10 {
		t.Fatalf("wrapped ZIP import result: %+v", imported)
	}
}
