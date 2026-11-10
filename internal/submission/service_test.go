package submission

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"biue/internal/judge"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

var permanentActor = Actor{ID: 7, AccountType: "permanent", Role: "user"}
var persistenceNow = time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)

type persistenceFixture struct {
	service *Service
	mock    sqlmock.Sqlmock
}

func newPersistenceFixture(t *testing.T) persistenceFixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(sqlx.NewDb(db, "sqlmock"), Config{QueueCapacity: 2}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return persistenceNow }
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return persistenceFixture{service: service, mock: mock}
}

func persistenceInput() CreateInput {
	return CreateInput{ProblemID: 42, Language: "cpp17", SourceCode: "int main() { return 0; }\n"}
}

func expectAdmission(mock sqlmock.Sqlmock, pending int) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM judge_admission WHERE id = 1 FOR UPDATE")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM judge_tasks WHERE status IN ('Pending','Queued','Compiling','Running')")).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(pending))
}

func expectAccount(mock sqlmock.Sqlmock, accountType, status string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT account_type, status FROM users WHERE id = ? FOR UPDATE")).WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"account_type", "status"}).AddRow(accountType, status))
}

func pinnedRows(dataVersion uint64, count int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "statement_version", "data_version", "snapshot_json", "case_count"}).AddRow(42, 8, dataVersion, `{"sample_input":"1 2\r\n"}`, count)
}

func expectPinned(mock sqlmock.Sqlmock, dataVersion uint64, count int) {
	mock.ExpectQuery("SELECT .* FROM problems p JOIN problem_versions .* WHERE p.id = \\? AND p.status = 'published' AND p.visibility = 'public' FOR SHARE").WithArgs(uint64(42)).WillReturnRows(pinnedRows(dataVersion, count))
}

func expectSubmissionInsert(mock sqlmock.Sqlmock, input CreateInput, kind string, dataVersion uint64, stdin any, count int) {
	var data any
	if dataVersion != 0 {
		data = dataVersion
	}
	mock.ExpectExec("INSERT INTO submissions").WithArgs(uint64(7), uint64(42), uint64(8), data, kind, input.Language, input.SourceCode, stdin, count, persistenceNow, persistenceNow).WillReturnResult(sqlmock.NewResult(101, 1))
}

func expectTaskAndOutbox(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO judge_tasks (submission_id, generation, attempt, status, lease_token, lease_expires_at, error_message, created_at, updated_at) VALUES (?, 1, 1, 'Pending', NULL, NULL, '', ?, ?)")).WithArgs(uint64(101), persistenceNow, persistenceNow).WillReturnResult(sqlmock.NewResult(202, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO judge_task_outbox (task_id, submission_id, generation, attempt, created_at) VALUES (?, ?, 1, 1, ?)")).WithArgs(uint64(202), uint64(101), persistenceNow).WillReturnResult(sqlmock.NewResult(303, 1))
}

func expectCreationAudit(mock sqlmock.Sqlmock, kind string) *sqlmock.ExpectedExec {
	action := "submission.create"
	if kind == "Run" {
		action = "run.create"
	}
	return mock.ExpectExec("INSERT INTO audit_logs").WithArgs(uint64(7), action, uint64(101), "server-request-id", persistenceNow)
}

func TestSubmitPersistsPinnedVersionTaskAndOutboxAtomically(t *testing.T) {
	f := newPersistenceFixture(t)
	input := persistenceInput()
	expectAdmission(f.mock, 1)
	expectAccount(f.mock, "permanent", "active")
	expectPinned(f.mock, 3, 2)
	expectSubmissionInsert(f.mock, input, "Submit", 3, nil, 2)
	expectTaskAndOutbox(f.mock)
	expectCreationAudit(f.mock, "Submit").WillReturnResult(sqlmock.NewResult(404, 1))
	f.mock.ExpectCommit()
	got, err := f.service.Create(context.Background(), permanentActor, "Submit", input, "server-request-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 101 || got.UserID != 7 || got.ProblemID != 42 || got.StatementVersion != 8 || got.DataVersion != 3 || got.Generation != 1 || got.Status != judge.Pending || got.TotalCases != 2 || got.Score != 0 || got.Stdin != nil || got.SourceCode != input.SourceCode {
		t.Fatalf("bad initial submission: %+v", got)
	}
}

func TestRunSampleAndExplicitInputRemainDistinct(t *testing.T) {
	empty := ""
	custom := "a\x00b\r\n"
	for _, tc := range []struct {
		name    string
		input   *string
		want    string
		version uint64
	}{
		{"sample without judge data", nil, "1 2\r\n", 0},
		{"explicit empty input", &empty, "", 3},
		{"custom input", &custom, custom, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPersistenceFixture(t)
			input := persistenceInput()
			input.Stdin = tc.input
			expectAdmission(f.mock, 0)
			expectAccount(f.mock, "permanent", "active")
			expectPinned(f.mock, tc.version, 2)
			expectSubmissionInsert(f.mock, input, "Run", tc.version, tc.want, 1)
			expectTaskAndOutbox(f.mock)
			expectCreationAudit(f.mock, "Run").WillReturnResult(sqlmock.NewResult(404, 1))
			f.mock.ExpectCommit()
			got, err := f.service.Create(context.Background(), permanentActor, "Run", input, "server-request-id")
			if err != nil {
				t.Fatal(err)
			}
			if got.Stdin == nil || *got.Stdin != tc.want || got.Kind != "Run" || got.Score != 0 || got.TotalCases != 1 || got.DataVersion != tc.version {
				t.Fatalf("run semantics changed: %+v", got)
			}
		})
	}
}

func TestCreateRejectsInvalidInputBeforeDatabase(t *testing.T) {
	stdin := "custom"
	badUTF8 := string([]byte{0xff})
	oversized := strings.Repeat("x", (64<<10)+1)
	for _, tc := range []struct {
		name, kind string
		mutate     func(*CreateInput)
	}{
		{"invalid kind", "Unknown", func(*CreateInput) {}},
		{"missing problem", "Submit", func(i *CreateInput) { i.ProblemID = 0 }},
		{"unknown language", "Submit", func(i *CreateInput) { i.Language = "bash" }},
		{"empty source", "Submit", func(i *CreateInput) { i.SourceCode = " \r\n" }},
		{"source too large", "Submit", func(i *CreateInput) { i.SourceCode = oversized }},
		{"source invalid utf8", "Submit", func(i *CreateInput) { i.SourceCode = badUTF8 }},
		{"source contains nul", "Submit", func(i *CreateInput) { i.SourceCode = "a\x00b" }},
		{"submit with stdin", "Submit", func(i *CreateInput) { i.Stdin = &stdin }},
		{"run input too large", "Run", func(i *CreateInput) { i.Stdin = &oversized }},
		{"run input invalid utf8", "Run", func(i *CreateInput) { i.Stdin = &badUTF8 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPersistenceFixture(t)
			input := persistenceInput()
			tc.mutate(&input)
			if _, err := f.service.Create(context.Background(), permanentActor, tc.kind, input, "server-request-id"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestExactSourceAndRunInputByteLimits(t *testing.T) {
	input := persistenceInput()
	input.SourceCode = strings.Repeat("x", 64<<10)
	stdin := strings.Repeat("输入", (64<<10)/6) + strings.Repeat(" ", (64<<10)%6)
	input.Stdin = &stdin
	if len(stdin) != 64<<10 {
		t.Fatal("fixture is not at exact byte limit")
	}
	if err := validateCreate("Run", input); err != nil {
		t.Fatal(err)
	}
	stdin += " "
	if err := validateCreate("Run", input); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized multibyte stdin accepted")
	}
}

func TestCreateRequiresPermanentAuthenticatedAccount(t *testing.T) {
	for _, actor := range []Actor{{}, {ID: 7, AccountType: "contest"}, {ID: 7, Role: "super_admin", AccountType: "contest"}} {
		f := newPersistenceFixture(t)
		if _, err := f.service.Create(context.Background(), actor, "Submit", persistenceInput(), "server-request-id"); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	}
}

func TestCreateRechecksDatabaseAccountUnderLock(t *testing.T) {
	for _, account := range []struct{ kind, status string }{{"permanent", "disabled"}, {"permanent", "retired"}, {"contest", "active"}} {
		f := newPersistenceFixture(t)
		expectAdmission(f.mock, 0)
		expectAccount(f.mock, account.kind, account.status)
		f.mock.ExpectRollback()
		if _, err := f.service.Create(context.Background(), permanentActor, "Submit", persistenceInput(), "server-request-id"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("account %+v: %v", account, err)
		}
	}
}

func TestAdmissionCapacityStopsBeforeSourceOrTaskInsert(t *testing.T) {
	f := newPersistenceFixture(t)
	expectAdmission(f.mock, 2)
	f.mock.ExpectRollback()
	if _, err := f.service.Create(context.Background(), permanentActor, "Submit", persistenceInput(), "server-request-id"); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
}

func TestNoJudgeDataRejectsSubmitWithoutPublishingAnOutbox(t *testing.T) {
	f := newPersistenceFixture(t)
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "permanent", "active")
	expectPinned(f.mock, 0, 0)
	f.mock.ExpectRollback()
	if _, err := f.service.Create(context.Background(), permanentActor, "Submit", persistenceInput(), "server-request-id"); !errors.Is(err, ErrNoData) {
		t.Fatal(err)
	}
}

func TestHiddenOrArchivedProblemCannotBeSubmitted(t *testing.T) {
	f := newPersistenceFixture(t)
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "permanent", "active")
	f.mock.ExpectQuery("SELECT .* FROM problems p JOIN problem_versions .* p.status = 'published' AND p.visibility = 'public' FOR SHARE").WithArgs(uint64(42)).WillReturnError(sql.ErrNoRows)
	f.mock.ExpectRollback()
	if _, err := f.service.Create(context.Background(), permanentActor, "Run", persistenceInput(), "server-request-id"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestOutboxFailureRollsBackSubmissionAndTask(t *testing.T) {
	f := newPersistenceFixture(t)
	input := persistenceInput()
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "permanent", "active")
	expectPinned(f.mock, 3, 2)
	expectSubmissionInsert(f.mock, input, "Submit", 3, nil, 2)
	f.mock.ExpectExec("INSERT INTO judge_tasks").WithArgs(uint64(101), persistenceNow, persistenceNow).WillReturnResult(sqlmock.NewResult(202, 1))
	f.mock.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(uint64(202), uint64(101), persistenceNow).WillReturnError(errors.New("outbox disk full"))
	f.mock.ExpectRollback()
	got, err := f.service.Create(context.Background(), permanentActor, "Submit", input, "server-request-id")
	if !errors.Is(err, ErrUnavailable) || got.ID != 0 {
		t.Fatalf("partial durable submission returned: %+v %v", got, err)
	}
}

func TestAuditFailureRollsBackAllSubmissionFacts(t *testing.T) {
	f := newPersistenceFixture(t)
	input := persistenceInput()
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "permanent", "active")
	expectPinned(f.mock, 3, 2)
	expectSubmissionInsert(f.mock, input, "Submit", 3, nil, 2)
	expectTaskAndOutbox(f.mock)
	expectCreationAudit(f.mock, "Submit").WillReturnError(errors.New("audit failure"))
	f.mock.ExpectRollback()
	got, err := f.service.Create(context.Background(), permanentActor, "Submit", input, "server-request-id")
	if !errors.Is(err, ErrUnavailable) || got.ID != 0 {
		t.Fatalf("partial durable submission returned: %+v %v", got, err)
	}
}

func TestUncertainCommitDoesNotReturnOrRepeatCreation(t *testing.T) {
	f := newPersistenceFixture(t)
	input := persistenceInput()
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "permanent", "active")
	expectPinned(f.mock, 3, 2)
	expectSubmissionInsert(f.mock, input, "Submit", 3, nil, 2)
	expectTaskAndOutbox(f.mock)
	expectCreationAudit(f.mock, "Submit").WillReturnResult(sqlmock.NewResult(404, 1))
	f.mock.ExpectCommit().WillReturnError(errors.New("unknown commit outcome"))
	got, err := f.service.Create(context.Background(), permanentActor, "Submit", input, "server-request-id")
	if !errors.Is(err, ErrUnavailable) || got.ID != 0 {
		t.Fatalf("uncertain commit: %+v %v", got, err)
	}
}

func summaryRows(kind string, status judge.Status, details bool) *sqlmock.Rows {
	columns := []string{"id", "problem_id", "user_id", "statement_version", "data_version", "generation", "kind", "language", "status", "score", "execution_time_ms", "memory_usage_kb", "passed_cases", "total_cases", "created_at", "updated_at"}
	values := []driver.Value{101, 42, 7, 8, 3, 1, kind, "cpp17", string(status), 0, 10, 1000, 0, 1, persistenceNow, persistenceNow}
	if details {
		columns = append(columns, "source_code", "stdin", "output", "error_message")
		values = append(values, "my-private-source", "hidden-input", "hidden-test-output", "diagnostic-message")
	}
	return sqlmock.NewRows(columns).AddRow(values...)
}

func TestOwnSubmissionListNeverSelectsSourceOrOutput(t *testing.T) {
	f := newPersistenceFixture(t)
	where := " FROM submissions s WHERE s.user_id = ? AND s.problem_id = ? AND s.kind = ?"
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)"+where)).WithArgs(uint64(7), uint64(42), "Submit").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT "+summaryColumns+where+" ORDER BY s.id DESC LIMIT ? OFFSET ?")).WithArgs(uint64(7), uint64(42), "Submit", 20, 0).WillReturnRows(summaryRows("Submit", judge.Pending, false))
	page, err := f.service.List(context.Background(), Actor{ID: 7, Role: "super_admin"}, ListOptions{ProblemID: 42, Kind: "Submit"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != 101 {
		t.Fatalf("bad page: %+v", page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "source_code") || strings.Contains(string(encoded), "output") {
		t.Fatalf("summary exposed details: %s", encoded)
	}
}

func TestAnotherUsersSubmissionReturnsNotFound(t *testing.T) {
	f := newPersistenceFixture(t)
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT "+summaryColumns+", s.source_code, s.stdin, s.output, s.error_message FROM submissions s WHERE s.id = ? AND (s.user_id = ? OR EXISTS (SELECT 1 FROM contest_admins ca WHERE ca.contest_id=s.contest_id AND ca.user_id=?))")).WithArgs(uint64(101), uint64(8), uint64(8)).WillReturnError(sql.ErrNoRows)
	if _, err := f.service.Get(context.Background(), Actor{ID: 8, AccountType: "permanent"}, 101); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFormalOutputsAlwaysHiddenAndRunOutputOwned(t *testing.T) {
	for _, tc := range []struct {
		kind           string
		status         judge.Status
		wantDiagnostic bool
	}{
		{"Submit", judge.RuntimeError, false}, {"Submit", judge.CompileError, true}, {"Run", judge.RuntimeError, true},
	} {
		f := newPersistenceFixture(t)
		f.mock.ExpectQuery("SELECT .* FROM submissions s WHERE s.id = \\? AND \\(s.user_id = \\? OR EXISTS").WithArgs(uint64(101), uint64(7), uint64(7)).WillReturnRows(summaryRows(tc.kind, tc.status, true))
		got, err := f.service.Get(context.Background(), permanentActor, 101)
		if err != nil {
			t.Fatal(err)
		}
		if got.SourceCode != "my-private-source" {
			t.Fatal("owner lost original source")
		}
		if tc.kind == "Submit" && (got.Stdin != nil || got.Output != "") {
			t.Fatalf("formal hidden data leaked: %+v", got)
		}
		if tc.kind == "Run" && (got.Stdin == nil || got.Output != "hidden-test-output") {
			t.Fatalf("run result missing: %+v", got)
		}
		if (got.ErrorMessage != "") != tc.wantDiagnostic {
			t.Fatalf("diagnostic rule violated: %+v", got)
		}
	}
}

func TestSuperAdminCanReadAnotherSubmissionButNotFormalOutput(t *testing.T) {
	f := newPersistenceFixture(t)
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT " + summaryColumns + ", s.source_code, s.stdin, s.output, s.error_message FROM submissions s WHERE s.id = ?")).WithArgs(uint64(101)).WillReturnRows(summaryRows("Submit", judge.Accepted, true))
	got, err := f.service.Get(context.Background(), Actor{ID: 99, AccountType: "permanent", Role: "super_admin"}, 101)
	if err != nil || got.UserID != 7 || got.Output != "" || got.Stdin != nil || got.ErrorMessage != "" {
		t.Fatalf("admin view: %+v %v", got, err)
	}
}

func TestAnonymousReadAndInvalidPaginationDoNotTouchDatabase(t *testing.T) {
	f := newPersistenceFixture(t)
	if _, err := f.service.List(context.Background(), Actor{}, ListOptions{}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := f.service.Get(context.Background(), Actor{}, 101); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	for _, opts := range []ListOptions{{Page: -1}, {Page: 100001}, {Limit: -1}, {Limit: 101}, {Kind: "Unknown"}} {
		if _, err := f.service.List(context.Background(), permanentActor, opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad pagination accepted: %+v %v", opts, err)
		}
	}
}
