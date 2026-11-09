package judge

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"

	"biue/internal/problemdata"
	"biue/internal/testcase"

	"github.com/DATA-DOG/go-sqlmock"
)

type datasetStub struct {
	data testcase.Dataset
	err  error
}

func (d datasetStub) Read(context.Context, string) (testcase.Dataset, error) { return d.data, d.err }

func ownedLease() Lease {
	return Lease{Task: runRequest().Task, Token: "0123456789abcdef0123456789abcdef"}
}

func requestRows(request Request) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"kind", "language", "source_code", "stdin", "total_cases", "snapshot_json", "storage_key", "digest", "case_count", "total_bytes", "total_score"}).AddRow(request.Kind, request.Language, request.SourceCode, request.Stdin, 1, `{"time_limit_ms":1000,"memory_limit_mb":128,"sample_input":"new input"}`, "", "", 0, 0, 0)
}

func expectLoad(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	l := ownedLease()
	mock.ExpectQuery(regexp.QuoteMeta(requestQuery)).WithArgs(l.Task.TaskID, l.Task.SubmissionID, l.Task.Generation, l.Task.Attempt, l.Token).WillReturnRows(rows)
}

func expectOwned(mock sqlmock.Sqlmock, status Status, generation uint64, owned bool) {
	l := ownedLease()
	mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(l.Task.SubmissionID).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(generation, status))
	if generation != l.Task.Generation || Terminal(status) {
		return
	}
	mock.ExpectQuery("SELECT status, .* AS owned FROM judge_tasks").WithArgs(l.Task.Attempt, l.Token, l.Task.TaskID, l.Task.SubmissionID, l.Task.Generation).WillReturnRows(sqlmock.NewRows([]string{"status", "owned"}).AddRow(status, owned))
}

func TestLoadPinnedRequest(t *testing.T) {
	s, mock := leaseFixture(t)
	store, _ := NewExecutionStore(s, datasetStub{err: errors.New("Run must not read data")})
	expectLoad(mock, requestRows(runRequest()))
	r, err := store.LoadRequest(context.Background(), ownedLease())
	if err != nil || string(r.Stdin) != "saved input" || r.TimeLimitMS != 1000 || r.MemoryLimitMB != 128 {
		t.Fatalf("request input=%q error=%v", r.Stdin, err)
	}
	// Exact requestQuery expectation verifies that joins use s.statement_version
	// and s.data_version rather than the current problem's mutable pointers.
}

func TestLoadSubmitChecksPublishedDataset(t *testing.T) {
	ctx := context.Background()
	data := testcase.Dataset{Cases: []testcase.Case{{ID: "a", Input: []byte("1"), Output: []byte("1"), Score: 40}, {ID: "b", Score: 60}}, TotalBytes: 2, TotalScore: 100}
	data.Digest = testcase.Fingerprint(data.Cases)
	files, err := problemdata.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	key, err := files.Write(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "digest", "bytes", "score", "count", "total cases"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, files)
			digest, bytes, score, count, total := hex.EncodeToString(data.Digest[:]), int64(2), int64(100), 2, 2
			switch scenario {
			case "digest":
				digest = "wrong"
			case "bytes":
				bytes++
			case "score":
				score++
			case "count":
				count++
			case "total cases":
				total++
			}
			rows := sqlmock.NewRows([]string{"kind", "language", "source_code", "stdin", "total_cases", "snapshot_json", "storage_key", "digest", "case_count", "total_bytes", "total_score"}).AddRow(Submit, "python3", "print(1)", nil, total, `{"time_limit_ms":1000,"memory_limit_mb":128}`, key, digest, count, bytes, score)
			expectLoad(mock, rows)
			r, err := store.LoadRequest(ctx, ownedLease())
			if scenario == "valid" {
				if err != nil || len(r.Cases) != 2 || r.Cases[0].Score != 40 {
					t.Fatalf("cases=%d error=%v", len(r.Cases), err)
				}
			} else if err != ErrRequestData || r.Task != (TaskEnvelope{}) || len(r.Cases) != 0 {
				t.Fatalf("bad data accepted: %v", err)
			}
		})
	}
}

func TestAdvanceFencesAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"success", "replay", "backwards", "old generation", "expired or replaced token", "task CAS failed", "submission update failed", "commit failed"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			mock.ExpectBegin()
			from, generation, owned := Queued, uint64(1), true
			if scenario == "replay" {
				from = Compiling
			}
			if scenario == "backwards" {
				from = Running
			}
			if scenario == "old generation" {
				generation = 2
			}
			if scenario == "expired or replaced token" {
				owned = false
			}
			expectOwned(mock, from, generation, owned)
			want := error(nil)
			switch scenario {
			case "replay":
				mock.ExpectRollback()
			case "backwards":
				want = ErrInvalidRequest
				mock.ExpectRollback()
			case "old generation", "expired or replaced token":
				want = ErrLeaseLost
				mock.ExpectRollback()
			default:
				count := int64(1)
				if scenario == "task CAS failed" {
					count = 0
				}
				mock.ExpectExec("UPDATE judge_tasks SET status").WithArgs(Compiling, 8, 9, 1, 1, ownedLease().Token, Queued).WillReturnResult(sqlmock.NewResult(0, count))
				if count == 0 {
					want = ErrLeaseLost
					mock.ExpectRollback()
				} else {
					u := mock.ExpectExec("UPDATE submissions SET status").WithArgs(Compiling, 9, 1, Queued)
					if scenario == "submission update failed" {
						u.WillReturnError(errors.New("failure"))
						want = ErrExecutionStore
						mock.ExpectRollback()
					} else {
						u.WillReturnResult(sqlmock.NewResult(0, 1))
						c := mock.ExpectCommit()
						if scenario == "commit failed" {
							c.WillReturnError(errors.New("unknown"))
							want = ErrExecutionStore
						}
					}
				}
			}
			if err := store.Advance(context.Background(), ownedLease(), Compiling); err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestFinishAtomicity(t *testing.T) {
	for _, scenario := range []string{"success", "history insert failed", "summary failed", "commit unknown", "old generation", "old attempt or token", "expired during update", "illegal phase", "bad result"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			expectLoad(mock, requestRows(runRequest()))
			result := acceptedRun()
			if scenario == "bad result" {
				result.Score = 1
				if err := store.Finish(context.Background(), ownedLease(), result); err != ErrInvalidResult {
					t.Fatal(err)
				}
				return
			}
			mock.ExpectBegin()
			generation, owned, status := uint64(1), true, Running
			if scenario == "old generation" {
				generation = 2
			}
			if scenario == "old attempt or token" {
				owned = false
			}
			if scenario == "illegal phase" {
				status = Queued
			}
			expectOwned(mock, status, generation, owned)
			want := error(nil)
			switch scenario {
			case "old generation", "old attempt or token":
				want = ErrLeaseLost
				mock.ExpectRollback()
			case "illegal phase":
				want = ErrInvalidResult
				mock.ExpectRollback()
			default:
				count := int64(1)
				if scenario == "expired during update" {
					count = 0
				}
				mock.ExpectExec("UPDATE judge_tasks SET status = .*lease_token = NULL, lease_expires_at = NULL").WithArgs(Accepted, 8, 9, 1, 1, ownedLease().Token, Running).WillReturnResult(sqlmock.NewResult(0, count))
				if count == 0 {
					want = ErrLeaseLost
					mock.ExpectRollback()
				} else {
					u := mock.ExpectExec("INSERT INTO submission_results").WithArgs(9, 1, 1, Accepted, 0, 2, 0, 1, 1, "1\n", "")
					if scenario == "history insert failed" {
						u.WillReturnError(errors.New("failure"))
						want = ErrExecutionStore
						mock.ExpectRollback()
					} else {
						u.WillReturnResult(sqlmock.NewResult(0, 1))
						u = mock.ExpectExec("UPDATE submissions SET status").WithArgs(Accepted, 0, 2, 0, 1, 1, "1\n", "", 9, 1, Running)
						if scenario == "summary failed" {
							u.WillReturnError(errors.New("failure"))
							want = ErrExecutionStore
							mock.ExpectRollback()
						} else {
							u.WillReturnResult(sqlmock.NewResult(0, 1))
							c := mock.ExpectCommit()
							if scenario == "commit unknown" {
								c.WillReturnError(errors.New("unknown"))
								want = ErrExecutionStore
							}
						}
					}
				}
			}
			if err := store.Finish(context.Background(), ownedLease(), result); err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestLoadRequestRejectsLostLease(t *testing.T) {
	s, mock := leaseFixture(t)
	store, _ := NewExecutionStore(s, datasetStub{})
	mock.ExpectQuery(regexp.QuoteMeta(requestQuery)).WillReturnError(sql.ErrNoRows)
	if _, err := store.LoadRequest(context.Background(), ownedLease()); err != ErrLeaseLost {
		t.Fatal(err)
	}
}
