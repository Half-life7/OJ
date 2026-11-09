package problem

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"biue/internal/testcase"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

var testAdmin = Actor{ID: 7, Role: "super_admin"}
var testNow = time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)

type recordingStore struct {
	calls   int
	dataset testcase.Dataset
	err     error
}

func (d *recordingStore) Write(_ context.Context, dataset testcase.Dataset) (string, error) {
	d.calls++
	d.dataset = dataset
	return strings.Repeat("a", 32), d.err
}

type serviceFixture struct {
	service *Service
	mock    sqlmock.Sqlmock
	files   *recordingStore
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	files := &recordingStore{}
	service, err := New(sqlx.NewDb(db, "sqlmock"), files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return testNow }
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return serviceFixture{service: service, mock: mock, files: files}
}

func validInput() Input {
	return Input{Title: "A + B", Description: "Compute the sum.", InputFormat: "Two integers", OutputFormat: "The sum", SampleInput: "1 2\n", SampleOutput: "3\n", TimeLimitMS: 1000, MemoryLimitMB: 256, Difficulty: "easy", Tags: []string{}}
}

func validDataset() testcase.Dataset {
	items := []testcase.Case{{ID: "1", Input: []byte("secret input\x00\n"), Output: []byte{}, Score: 40}, {ID: "2", Input: nil, Output: []byte("secret output\r\n"), Score: 60}}
	var count int64
	for _, item := range items {
		count += int64(len(item.Input) + len(item.Output))
	}
	return testcase.Dataset{Cases: items, Digest: testcase.Fingerprint(items), TotalBytes: count, TotalScore: 100}
}

func rowsForProblem(t *testing.T, revision, statement, data uint64) *sqlmock.Rows {
	t.Helper()
	input, _ := normalizeInput(validInput())
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return sqlmock.NewRows([]string{"id", "snapshot_json", "revision", "statement_version", "data_version", "creator_id", "created_at", "updated_at"}).AddRow(42, body, revision, statement, data, 7, testNow.Add(-time.Hour), testNow)
}

type snapshotArgument struct{ input Input }

func (s snapshotArgument) Match(value driver.Value) bool {
	var data []byte
	switch value := value.(type) {
	case []byte:
		data = value
	case string:
		data = []byte(value)
	default:
		return false
	}
	var actual Input
	return json.Unmarshal(data, &actual) == nil && reflect.DeepEqual(actual, s.input)
}

func expectProblemAudit(mock sqlmock.Sqlmock, action string) *sqlmock.ExpectedExec {
	return mock.ExpectExec(regexp.QuoteMeta("INSERT INTO audit_logs")).WithArgs(testAdmin.ID, action, uint64(42), "request-id", testNow)
}

func TestNormalizeInputDefaultsAndOriginalContent(t *testing.T) {
	in := validInput()
	in.Title = "  A + B  "
	in.Tags = []string{" DP ", "图论", "dp"}
	in.SampleInput = "\r\n  "
	got, err := normalizeInput(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "A + B" || got.Status != "draft" || got.Visibility != "hidden" || got.SampleInput != "\r\n  " || !reflect.DeepEqual(got.Tags, []string{"dp", "图论"}) {
		t.Fatalf("unexpected normalized input: %+v", got)
	}
}

func TestInputValidationBeforeDatabase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Input)
	}{
		{"blank title", func(i *Input) { i.Title = "  " }},
		{"long title", func(i *Input) { i.Title = strings.Repeat("题", 201) }},
		{"title controls", func(i *Input) { i.Title = "a\nb" }},
		{"description required", func(i *Input) { i.Description = " " }},
		{"oversized description", func(i *Input) { i.Description = strings.Repeat("x", (64<<10)+1) }},
		{"invalid utf8", func(i *Input) { i.SampleOutput = string([]byte{0xff}) }},
		{"nul in text", func(i *Input) { i.InputFormat = "a\x00b" }},
		{"zero time", func(i *Input) { i.TimeLimitMS = 0 }},
		{"excess memory", func(i *Input) { i.MemoryLimitMB = 4097 }},
		{"unknown difficulty", func(i *Input) { i.Difficulty = "expert" }},
		{"unknown status", func(i *Input) { i.Status = "deleted" }},
		{"unknown visibility", func(i *Input) { i.Visibility = "private" }},
		{"too many tags", func(i *Input) { i.Tags = make([]string, 17) }},
		{"long tag", func(i *Input) { i.Tags = []string{strings.Repeat("图", 33)} }},
		{"tag control", func(i *Input) { i.Tags = []string{"a\x00b"} }},
		{"create revision supplied", func(i *Input) { i.Revision = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			input := validInput()
			tc.mutate(&input)
			if _, err := f.service.Create(context.Background(), testAdmin, input, "request-id"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestMutationsAndDataMetadataRequireAdmin(t *testing.T) {
	for _, actor := range []Actor{{}, {ID: 7, Role: "user"}, {Role: "super_admin"}} {
		f := newServiceFixture(t)
		ctx := context.Background()
		_, createErr := f.service.Create(ctx, actor, validInput(), "request-id")
		_, updateErr := f.service.Update(ctx, actor, 42, validInput(), "request-id")
		_, importErr := f.service.Import(ctx, actor, 42, 1, validDataset(), "request-id")
		_, listErr := f.service.ListDataVersions(ctx, actor, 42, 1, 20)
		_, getErr := f.service.GetDataVersion(ctx, actor, 42, 1)
		for _, err := range []error{createErr, updateErr, importErr, listErr, getErr} {
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("actor %+v: %v", actor, err)
			}
		}
		if f.files.calls != 0 {
			t.Fatal("unauthorized import reached file storage")
		}
	}
}

func TestPublicVisibilityIsEnforcedInSQL(t *testing.T) {
	for _, actor := range []Actor{{}, {ID: 8, Role: "user"}} {
		f := newServiceFixture(t)
		where := " FROM problems p WHERE p.id = ? AND p.status = 'published' AND p.visibility = 'public'"
		f.mock.ExpectQuery(regexp.QuoteMeta("SELECT " + problemColumns + where)).WithArgs(uint64(42)).WillReturnError(sql.ErrNoRows)
		if _, err := f.service.Get(context.Background(), actor, 42); !errors.Is(err, ErrNotFound) {
			t.Fatalf("restricted problem did not return not found: %v", err)
		}
	}
}

func TestPublicListFiltersAreParameterized(t *testing.T) {
	f := newServiceFixture(t)
	where := " FROM problems p WHERE 1=1 AND p.status = 'published' AND p.visibility = 'public' AND p.title LIKE ? ESCAPE '=' AND p.difficulty = ? AND EXISTS (SELECT 1 FROM problem_tags pt JOIN tags t ON t.id = pt.tag_id WHERE pt.problem_id = p.id AND t.name = ?)"
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)"+where)).WithArgs("%100=%=_%", "easy", "dp").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	f.mock.ExpectQuery(regexp.QuoteMeta("SELECT "+problemColumns+where+" ORDER BY p.id DESC LIMIT ? OFFSET ?")).WithArgs("%100=%=_%", "easy", "dp", 20, 0).WillReturnRows(rowsForProblem(t, 3, 2, 1))
	page, err := f.service.List(context.Background(), Actor{}, ListOptions{Query: "100%_", Difficulty: "easy", Tag: " DP "})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Page != 1 || page.Limit != 20 || len(page.Items) != 1 || page.Items[0].Revision != 3 || page.Items[0].DataVersion != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestPaginationAndFilterValidationBeforeDatabase(t *testing.T) {
	for _, opts := range []ListOptions{{Page: -1}, {Page: 100001}, {Limit: -1}, {Limit: 101}, {Difficulty: "invalid"}, {Status: "invalid"}, {Visibility: "invalid"}, {Query: string([]byte{0xff})}, {Tag: "\x00"}} {
		f := newServiceFixture(t)
		if _, err := f.service.List(context.Background(), Actor{}, opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("opts %+v: %v", opts, err)
		}
	}
}

func expectCreatePrefix(t *testing.T, mock sqlmock.Sqlmock) Input {
	t.Helper()
	input, _ := normalizeInput(validInput())
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO problems").WithArgs(input.Title, input.Difficulty, "draft", "hidden", snapshotArgument{input}, testAdmin.ID, testNow, testNow).WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectExec("INSERT INTO problem_versions").WithArgs(uint64(42), snapshotArgument{input}, testAdmin.ID, testNow).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE problems SET statement_version = 1").WithArgs(uint64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM problem_tags").WithArgs(uint64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
	return input
}

func TestCreateStoresInitialImmutableStatementAndAudit(t *testing.T) {
	f := newServiceFixture(t)
	expectCreatePrefix(t, f.mock)
	expectProblemAudit(f.mock, "problem.create").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()
	created, err := f.service.Create(context.Background(), testAdmin, validInput(), "request-id")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 42 || created.Revision != 1 || created.StatementVersion != 1 || created.DataVersion != 0 || created.Status != "draft" || created.Visibility != "hidden" || created.CreatorID != testAdmin.ID {
		t.Fatalf("unexpected created problem: %+v", created)
	}
}

func TestCreateAuditFailureRollsBackEntireObject(t *testing.T) {
	f := newServiceFixture(t)
	expectCreatePrefix(t, f.mock)
	expectProblemAudit(f.mock, "problem.create").WillReturnError(errors.New("audit database down"))
	f.mock.ExpectRollback()
	got, err := f.service.Create(context.Background(), testAdmin, validInput(), "request-id")
	if !errors.Is(err, ErrUnavailable) || got.ID != 0 {
		t.Fatalf("returned partial problem: %+v %v", got, err)
	}
}

func TestUpdateRevisionConflictDoesNotWrite(t *testing.T) {
	f := newServiceFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT .* FROM problems p WHERE p.id = \\? FOR UPDATE").WithArgs(uint64(42)).WillReturnRows(rowsForProblem(t, 4, 2, 2))
	f.mock.ExpectRollback()
	input := validInput()
	input.Revision = 3
	if _, err := f.service.Update(context.Background(), testAdmin, 42, input, "request-id"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestUpdateAppendsStatementKeepsDataAndNormalizesTags(t *testing.T) {
	f := newServiceFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT .* FOR UPDATE").WithArgs(uint64(42)).WillReturnRows(rowsForProblem(t, 4, 2, 2))
	input := validInput()
	input.Revision = 4
	input.Status = "archived"
	input.Tags = []string{"Graph", " DP ", "dp"}
	snapshot, _ := normalizeInput(input)
	snapshot.Revision = 0
	f.mock.ExpectExec("INSERT INTO problem_versions").WithArgs(uint64(42), uint64(3), snapshotArgument{snapshot}, testAdmin.ID, testNow).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectExec("UPDATE problems SET title").WithArgs(snapshot.Title, snapshot.Difficulty, snapshot.Status, snapshot.Visibility, snapshotArgument{snapshot}, uint64(5), uint64(3), testNow, uint64(42), uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectExec("DELETE FROM problem_tags").WithArgs(uint64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
	for _, tag := range []string{"dp", "graph"} {
		f.mock.ExpectExec("INSERT INTO tags").WithArgs(tag, tag).WillReturnResult(sqlmock.NewResult(2, 1))
		f.mock.ExpectExec("INSERT INTO problem_tags").WithArgs(uint64(42), tag).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	expectProblemAudit(f.mock, "problem.update").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()
	got, err := f.service.Update(context.Background(), testAdmin, 42, input, "request-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatementVersion != 3 || got.Revision != 5 || got.DataVersion != 2 || got.Status != "archived" || !reflect.DeepEqual(got.Tags, []string{"dp", "graph"}) {
		t.Fatalf("updated problem: %+v", got)
	}
}

func expectImportPrefix(t *testing.T, mock sqlmock.Sqlmock, dataset testcase.Dataset) {
	t.Helper()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FOR UPDATE").WithArgs(uint64(42)).WillReturnRows(rowsForProblem(t, 4, 2, 2))
	mock.ExpectExec("INSERT INTO problem_data_versions").WithArgs(uint64(42), uint64(3), strings.Repeat("a", 32), hex.EncodeToString(dataset.Digest[:]), len(dataset.Cases), dataset.TotalBytes, dataset.TotalScore, uint64(5), testAdmin.ID, testNow).WillReturnResult(sqlmock.NewResult(0, 1))
	for _, item := range dataset.Cases {
		mock.ExpectExec("INSERT INTO testcases").WithArgs(uint64(42), uint64(3), item.ID, item.Score, int64(len(item.Input)), int64(len(item.Output))).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("UPDATE problems SET data_version").WithArgs(uint64(3), uint64(5), testNow, uint64(42), uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestImportAppendsVersionAndReturnsOnlyMetadata(t *testing.T) {
	f := newServiceFixture(t)
	dataset := validDataset()
	expectImportPrefix(t, f.mock, dataset)
	expectProblemAudit(f.mock, "problem.data_import").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()
	version, err := f.service.Import(context.Background(), testAdmin, 42, 4, dataset, "request-id")
	if err != nil {
		t.Fatal(err)
	}
	if version.Version != 3 || version.Revision != 5 || len(version.Cases) != 2 || version.TotalScore != 100 || f.files.calls != 1 || !reflect.DeepEqual(f.files.dataset, dataset) {
		t.Fatalf("unexpected version: %+v", version)
	}
	encoded, err := json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"secret input", "secret output", "storage_key", strings.Repeat("a", 32)} {
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("response exposed hidden data: %s", encoded)
		}
	}
}

func TestImportCommitUnknownKeepsPublishedData(t *testing.T) {
	f := newServiceFixture(t)
	dataset := validDataset()
	expectImportPrefix(t, f.mock, dataset)
	expectProblemAudit(f.mock, "problem.data_import").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit().WillReturnError(errors.New("commit acknowledgement lost"))
	version, err := f.service.Import(context.Background(), testAdmin, 42, 4, dataset, "request-id")
	if !errors.Is(err, ErrUnavailable) || version.Version != 0 || f.files.calls != 1 {
		t.Fatalf("unknown commit outcome: %+v %v writes=%d", version, err, f.files.calls)
	}
	// DataStore deliberately exposes no deletion operation. An uncertain commit
	// can be retried only after reading the current revision, never by removing data.
}

func TestImportAuditFailureRollsBackDatabaseButRetainsFiles(t *testing.T) {
	f := newServiceFixture(t)
	dataset := validDataset()
	expectImportPrefix(t, f.mock, dataset)
	expectProblemAudit(f.mock, "problem.data_import").WillReturnError(errors.New("audit rejected"))
	f.mock.ExpectRollback()
	version, err := f.service.Import(context.Background(), testAdmin, 42, 4, dataset, "request-id")
	if !errors.Is(err, ErrUnavailable) || version.Version != 0 || f.files.calls != 1 {
		t.Fatalf("partial import: %+v %v", version, err)
	}
}

func TestImportStaleRevisionNeverPublishesFiles(t *testing.T) {
	f := newServiceFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT .* FOR UPDATE").WithArgs(uint64(42)).WillReturnRows(rowsForProblem(t, 5, 2, 3))
	f.mock.ExpectRollback()
	if _, err := f.service.Import(context.Background(), testAdmin, 42, 4, validDataset(), "request-id"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if f.files.calls != 0 {
		t.Fatal("stale upload reached file store")
	}
}

func TestImportStorageFailureNeverAdvancesPointer(t *testing.T) {
	f := newServiceFixture(t)
	f.files.err = errors.New("disk full: hidden/path")
	f.mock.ExpectBegin()
	f.mock.ExpectQuery("SELECT .* FOR UPDATE").WithArgs(uint64(42)).WillReturnRows(rowsForProblem(t, 4, 2, 2))
	f.mock.ExpectRollback()
	if _, err := f.service.Import(context.Background(), testAdmin, 42, 4, validDataset(), "request-id"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("file failure leaked internals: %v", err)
	}
}

func TestMalformedDatasetsNeverTouchDatabase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testcase.Dataset)
	}{
		{"empty", func(d *testcase.Dataset) { d.Cases = nil }},
		{"negative score", func(d *testcase.Dataset) { d.Cases[0].Score = -1 }},
		{"wrong total", func(d *testcase.Dataset) { d.TotalScore++ }},
		{"wrong bytes", func(d *testcase.Dataset) { d.TotalBytes++ }},
		{"wrong fingerprint", func(d *testcase.Dataset) { d.Digest[0]++ }},
		{"unsafe case id", func(d *testcase.Dataset) { d.Cases[0].ID = "../a" }},
		{"case insensitive conflict", func(d *testcase.Dataset) { d.Cases[0].ID = "A"; d.Cases[1].ID = "a" }},
		{"out of order", func(d *testcase.Dataset) { d.Cases[0], d.Cases[1] = d.Cases[1], d.Cases[0] }},
		{"score overflow", func(d *testcase.Dataset) { d.Cases[0].Score = math.MaxInt64 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t)
			dataset := validDataset()
			tc.mutate(&dataset)
			if _, err := f.service.Import(context.Background(), testAdmin, 42, 4, dataset, "request-id"); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			if f.files.calls != 0 {
				t.Fatal("invalid dataset published")
			}
		})
	}
}

func TestGetDataVersionContainsMetadataAndChecksCaseCount(t *testing.T) {
	for _, missingCase := range []bool{false, true} {
		f := newServiceFixture(t)
		f.mock.ExpectQuery("SELECT .* FROM problem_data_versions WHERE problem_id = \\? AND data_version = \\?").WithArgs(uint64(42), uint64(3)).WillReturnRows(sqlmock.NewRows([]string{"data_version", "digest", "case_count", "total_bytes", "total_score", "revision", "created_by", "created_at"}).AddRow(3, strings.Repeat("f", 64), 1, 4, 100, 5, 7, testNow))
		rows := sqlmock.NewRows([]string{"case_id", "score", "input_bytes", "output_bytes"})
		if !missingCase {
			rows.AddRow("1", 100, 2, 2)
		}
		f.mock.ExpectQuery("SELECT case_id, score, input_bytes, output_bytes FROM testcases").WithArgs(uint64(42), uint64(3)).WillReturnRows(rows)
		got, err := f.service.GetDataVersion(context.Background(), testAdmin, 42, 3)
		if missingCase {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		} else if err != nil || len(got.Cases) != 1 || got.Cases[0].ID != "1" || got.Revision != 5 {
			t.Fatalf("metadata: %+v %v", got, err)
		}
	}
}
