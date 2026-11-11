package operations

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"biue/internal/auth"
	"biue/internal/competition"
	"biue/internal/problem"
	"biue/internal/problemdata"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func integrationDatabase(t *testing.T) (*sqlx.DB, func()) {
	t.Helper()
	dsn := os.Getenv("BIUE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BIUE_TEST_MYSQL_DSN is not configured")
	}
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	var locked int
	if err := db.Get(&locked, `SELECT GET_LOCK('biue_integration_mysql',30)`); err != nil || locked != 1 {
		db.Close()
		t.Fatalf("integration lock: %v %d", err, locked)
	}
	clear := func() {
		_, _ = db.Exec(`SET FOREIGN_KEY_CHECKS=0`)
		for _, table := range []string{
			"judge_dead_letters", "judge_task_outbox", "submission_case_results",
			"submission_results", "judge_attempt_failures", "judge_tasks",
			"rejudge_requests", "submissions", "judge_message_quarantine",
			"problem_list_items", "problem_list_favorites", "discussion_likes",
			"community_write_limits", "community_documents", "contest_snapshots",
			"contest_points", "contest_freezes", "contest_imports",
			"contest_team_members", "contest_teams", "contest_problems",
			"contest_admins", "contests", "global_board_cache", "problem_tags",
			"testcases", "tags", "problem_versions", "problem_data_versions",
			"problems", "student_no_history", "auth_sessions", "person_accounts",
			"users", "persons", "audit_logs",
		} {
			_, _ = db.Exec("DELETE FROM " + table)
		}
		_, _ = db.Exec(`SET FOREIGN_KEY_CHECKS=1`)
	}
	clear()
	return db, func() {
		clear()
		_, _ = db.Exec(`SELECT RELEASE_LOCK('biue_integration_mysql')`)
		_ = db.Close()
	}
}

func TestSimulationAndResetWithRealMySQL(t *testing.T) {
	db, cleanup := integrationDatabase(t)
	defer cleanup()
	now := time.Now().UTC().Truncate(time.Microsecond)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO persons(id,display_name,student_no,student_no_status,created_at,updated_at) VALUES
		(1,'Admin',NULL,'none',?,?),(2,'Existing User','S-EXIST','verified',?,?)`, now, now, now, now)
	exec(`INSERT INTO users(id,username,password_hash,account_type,global_role,status,created_at,updated_at) VALUES
		(1,'admin_user','$2a$12$abcdefghijklmnopqrstuv','permanent','super_admin','active',?,?),
		(2,'existing_user','$2a$12$abcdefghijklmnopqrstuv','permanent','user','active',?,?)`,
		now, now, now, now)
	exec(`INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (1,1,?),(2,2,?)`, now, now)
	exec(`INSERT INTO auth_sessions(jti_hash,user_id,token_version,expires_at,created_at) VALUES
		(REPEAT('a',64),1,1,?,?),(REPEAT('b',64),2,1,?,?)`, now.Add(time.Hour), now, now.Add(time.Hour), now.Add(time.Second))

	storeDir := filepath.Join(t.TempDir(), "versions")
	store, err := problemdata.New(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	problems, err := problem.New(db, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	contests := competition.New(db)
	service := New(db, nil, store, problems, contests, 5000)
	admin := auth.User{ID: 1, Username: "admin_user", AccountType: "permanent", GlobalRole: "super_admin", Status: "active"}
	result, err := service.Simulate(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 36 || len(result.ProblemIDs) != 3 || result.PracticeSubmits < 432 || result.ContestSubmissions < 432 {
		t.Fatalf("unexpected simulation result: %+v", result)
	}
	var users, publicProblems, submissions, results, pending, points int
	countQueries := []struct {
		query  string
		target *int
	}{
		{`SELECT COUNT(*) FROM users`, &users},
		{`SELECT COUNT(*) FROM problems WHERE status='published' AND visibility='public'`, &publicProblems},
		{`SELECT COUNT(*) FROM submissions`, &submissions},
		{`SELECT COUNT(*) FROM submission_results`, &results},
		{`SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')`, &pending},
		{`SELECT COUNT(*) FROM contest_points`, &points},
	}
	for _, item := range countQueries {
		if err := db.Get(item.target, item.query); err != nil {
			t.Fatal(err)
		}
	}
	if users != 38 || publicProblems != 3 || submissions != result.PracticeSubmits+result.ContestSubmissions || results != submissions || pending != 0 || points != 36 {
		t.Fatalf("simulation counts users=%d problems=%d submissions=%d results=%d pending=%d points=%d", users, publicProblems, submissions, results, pending, points)
	}
	var settled bool
	if err := db.Get(&settled, `SELECT settled_at IS NOT NULL FROM contests WHERE id=?`, result.ContestID); err != nil || !settled {
		t.Fatalf("simulation contest was not settled: %v %v", settled, err)
	}

	exec(`UPDATE judge_tasks SET status='Pending' ORDER BY id LIMIT 1`)
	if _, err := service.Reset(context.Background(), admin, ResetInput{Confirmation: ResetConfirmation, Reason: "must reject active task"}, "reset-active"); err != ErrConflict {
		t.Fatalf("reset with active task error=%v", err)
	}
	exec(`UPDATE judge_tasks t JOIN submission_results r ON r.submission_id=t.submission_id AND r.generation=t.generation SET t.status=r.status`)

	reset, err := service.Reset(context.Background(), admin, ResetInput{Confirmation: ResetConfirmation, Reason: "integration reset"}, "reset-request")
	if err != nil {
		t.Fatal(err)
	}
	if reset.DataVersions != 3 || reset.Complete || len(reset.Warnings) != 1 {
		t.Fatalf("unexpected reset result: %+v", reset)
	}
	if err := db.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 1 {
		t.Fatalf("reset did not preserve only admin: %d %v", users, err)
	}
	var preserved struct {
		Username  string `db:"username"`
		Person    uint64 `db:"person_id"`
		Session   int    `db:"sessions"`
		Admission int    `db:"admission"`
	}
	if err := db.Get(&preserved, `SELECT u.username,pa.person_id,
		(SELECT COUNT(*) FROM auth_sessions s WHERE s.user_id=u.id) AS sessions,
		(SELECT COUNT(*) FROM judge_admission WHERE id=1) AS admission
		FROM users u JOIN person_accounts pa ON pa.user_id=u.id WHERE u.id=1`); err != nil {
		t.Fatal(err)
	}
	if preserved.Username != "admin_user" || preserved.Person != 1 || preserved.Session != 1 || preserved.Admission != 1 {
		t.Fatalf("reset preservation boundary changed: %+v", preserved)
	}
	if err := db.Get(&submissions, `SELECT COUNT(*) FROM submissions`); err != nil || submissions != 0 {
		t.Fatalf("reset left submissions: %d %v", submissions, err)
	}
	entries, err := os.ReadDir(storeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("reset left problem data directories: %v %v", entries, err)
	}
}

func TestProblemRejudgeSkipsSettledContestWithRealMySQL(t *testing.T) {
	db, cleanup := integrationDatabase(t)
	defer cleanup()
	now := time.Now().UTC().Truncate(time.Microsecond)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO persons(id,display_name,student_no,student_no_status,created_at,updated_at) VALUES
		(1,'Admin',NULL,'none',?,?),(2,'Participant','S-JUDGE','verified',?,?)`, now, now, now, now)
	exec(`INSERT INTO users(id,username,password_hash,account_type,global_role,status,created_at,updated_at) VALUES
		(1,'admin_user','$2a$12$abcdefghijklmnopqrstuv','permanent','super_admin','active',?,?),
		(2,'participant','$2a$12$abcdefghijklmnopqrstuv','permanent','user','active',?,?)`,
		now, now, now, now)
	exec(`INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (1,1,?),(2,2,?)`, now, now)

	store, err := problemdata.New(filepath.Join(t.TempDir(), "versions"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	problems, err := problem.New(db, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	definition := simulationProblems("judge")[0]
	created, err := problems.ImportPackage(context.Background(), problem.Actor{ID: 1, Role: "super_admin"}, problem.ProblemPackage{
		Input: definition.input, Dataset: definition.dataset,
	}, "rejudge-problem")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO contests(id,title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,created_by,created_at,settled_at) VALUES
		(1,'Running','OI','admin_add',0,0,?,?,1,?,NULL),
		(2,'Settled','OI','admin_add',0,0,?,?,1,?,?)`,
		now.Add(-time.Hour), now.Add(time.Hour), now,
		now.Add(-2*time.Hour), now.Add(-time.Hour), now, now)
	exec(`INSERT INTO contest_teams(id,contest_id,name,participant_type,status,created_at) VALUES
		(1,1,'Running Team','official','active',?),(2,2,'Settled Team','official','active',?)`, now, now)
	exec(`INSERT INTO contest_team_members(contest_id,team_id,user_id,person_id,identity_snapshot) VALUES
		(1,1,2,2,'{}'),(2,2,2,2,'{}')`)
	exec(`INSERT INTO contest_problems(contest_id,problem_id,statement_version,data_version) VALUES
		(1,?,?,?),(2,?,?,?)`, created.ID, created.StatementVersion, created.DataVersion, created.ID, created.StatementVersion, created.DataVersion)
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, item := range []simulatedSubmission{
		{UserID: 2, ProblemID: created.ID, StatementVersion: created.StatementVersion, DataVersion: created.DataVersion, ContestID: nil, TeamID: nil, Source: "int main(){}", Status: "Accepted", Score: 100, Passed: 2, Total: 2, Created: actualTime(now, -30)},
		{UserID: 2, ProblemID: created.ID, StatementVersion: created.StatementVersion, DataVersion: created.DataVersion, ContestID: uint64(1), TeamID: uint64(1), Source: "int main(){}", Status: "Accepted", Score: 100, Passed: 2, Total: 2, Created: actualTime(now, -20)},
		{UserID: 2, ProblemID: created.ID, StatementVersion: created.StatementVersion, DataVersion: created.DataVersion, ContestID: uint64(2), TeamID: uint64(2), Source: "int main(){}", Status: "Accepted", Score: 100, Passed: 2, Total: 2, Created: actualTime(now, -100)},
	} {
		if _, err := insertSimulatedSubmission(context.Background(), tx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	adminProblem := problem.Actor{ID: 1, Role: "super_admin"}
	updatedInput := created.Input
	updatedInput.Title += " v2"
	updatedInput.Revision = created.Revision
	updated, err := problems.Update(context.Background(), adminProblem, created.ID, updatedInput, "rejudge-statement-v2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := problems.Import(context.Background(), adminProblem, created.ID, updated.Revision, definition.dataset, problem.ImportReplace, "rejudge-data-v2"); err != nil {
		t.Fatal(err)
	}
	service := New(db, nil, store, problems, competition.New(db), 100)
	admin := auth.User{ID: 1, Username: "admin_user", AccountType: "permanent", GlobalRole: "super_admin", Status: "active"}
	if _, err := service.RejudgeContest(context.Background(), admin, ScopeRejudgeInput{
		Key: "settled-contest-key", Reason: "must be rejected", ContestID: 2,
	}); err != ErrConflict {
		t.Fatalf("settled contest rejudge error=%v", err)
	}
	result, err := service.RejudgeProblem(context.Background(), admin, ScopeRejudgeInput{
		Key: "problem-batch-key", Reason: "integration rejudge", ProblemID: created.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Queued != 2 || result.Skipped != 1 || result.Batches != 1 || len(result.Tasks) != 2 {
		t.Fatalf("unexpected scoped rejudge: %+v", result)
	}
	var practice struct {
		StatementVersion uint64 `db:"statement_version"`
		DataVersion      uint64 `db:"data_version"`
		Generation       uint64 `db:"generation"`
	}
	if err := db.Get(&practice, `SELECT statement_version,data_version,generation FROM submissions WHERE contest_id IS NULL`); err != nil ||
		practice.StatementVersion != 2 || practice.DataVersion != 2 || practice.Generation != 2 {
		t.Fatalf("practice rejudge did not use latest problem versions: %+v err=%v", practice, err)
	}
	var contestPinned struct {
		StatementVersion uint64 `db:"statement_version"`
		DataVersion      uint64 `db:"data_version"`
		Generation       uint64 `db:"generation"`
	}
	if err := db.Get(&contestPinned, `SELECT statement_version,data_version,generation FROM submissions WHERE contest_id=1 ORDER BY id LIMIT 1`); err != nil ||
		contestPinned.StatementVersion != 1 || contestPinned.DataVersion != 1 || contestPinned.Generation != 2 {
		t.Fatalf("contest rejudge did not preserve pinned versions: %+v err=%v", contestPinned, err)
	}
	tx, err = db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertSimulatedSubmission(context.Background(), tx, simulatedSubmission{
		UserID: 2, ProblemID: created.ID, StatementVersion: created.StatementVersion, DataVersion: created.DataVersion,
		ContestID: uint64(1), TeamID: uint64(1), Source: "int main(){return 1;}", Status: "WrongAnswer",
		Score: 0, Passed: 0, Total: 2, Created: actualTime(now, -10),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	contestResult, err := service.RejudgeContest(context.Background(), admin, ScopeRejudgeInput{
		Key: "contest-batch-key", Reason: "integration contest rejudge", ContestID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if contestResult.Queued != 1 || contestResult.Skipped != 1 || contestResult.Batches != 1 {
		t.Fatalf("unexpected contest scoped rejudge: %+v", contestResult)
	}
	var pending int
	if err := db.Get(&pending, `SELECT COUNT(*) FROM judge_tasks WHERE status='Pending'`); err != nil || pending != 3 {
		t.Fatalf("pending tasks=%d err=%v", pending, err)
	}
	var settledGeneration uint64
	if err := db.Get(&settledGeneration, `SELECT generation FROM submissions WHERE contest_id=2`); err != nil || settledGeneration != 1 {
		t.Fatalf("settled contest submission changed: %d %v", settledGeneration, err)
	}
}

func actualTime(now time.Time, minutes int) time.Time {
	return now.Add(time.Duration(minutes) * time.Minute)
}
