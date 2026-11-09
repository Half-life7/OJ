package judge

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"biue/internal/testcase"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func pendingQueue(t *testing.T) (*Queue, Delivery, *miniredis.Miniredis) {
	t.Helper()
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
	t.Cleanup(func() { client.Close() })
	q, err := NewQueue(client, "worker", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: runRequest().Task.StreamValues()}).Err(); err != nil {
		t.Fatal(err)
	}
	d, ok, err := q.Receive(context.Background())
	if err != nil || !ok {
		t.Fatalf("receive: %v", err)
	}
	return q, d, r
}

func expectHistory(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("SELECT r.status FROM submission_results r JOIN judge_tasks t").WithArgs(8, 9, 1, 1)
}

func pendingCount(t *testing.T, q *Queue) int64 {
	t.Helper()
	p, err := q.client.XPending(context.Background(), Stream, ConsumerGroup).Result()
	if err != nil {
		t.Fatal(err)
	}
	return p.Count
}

func TestAcknowledgeRequiresPersistedResult(t *testing.T) {
	for _, scenario := range []string{"success", "not persisted", "database unavailable", "nonterminal", "wrong payload", "Redis unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q, d, r := pendingQueue(t)
			want := error(nil)
			query := expectHistory(mock)
			switch scenario {
			case "not persisted":
				query.WillReturnError(sql.ErrNoRows)
				want = ErrLeaseLost
			case "database unavailable":
				query.WillReturnError(errors.New("private error"))
				want = ErrExecutionStore
			case "nonterminal":
				query.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Running))
				want = ErrExecutionStore
			default:
				query.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Accepted))
			}
			if scenario == "wrong payload" {
				id, err := q.client.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: map[string]any{"not": "the same task"}}).Result()
				if err != nil {
					t.Fatal(err)
				}
				d.ID = id
				want = ErrInvalidEnvelope
			}
			if scenario == "Redis unavailable" {
				r.SetError("unavailable")
				want = ErrQueue
			}
			if err := store.AcknowledgeCompleted(context.Background(), q, d); err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
			r.SetError("")
			count := int64(1)
			if scenario == "success" {
				count = 0
			}
			if got := pendingCount(t, q); got != count {
				t.Fatalf("pending=%d want=%d", got, count)
			}
			if scenario == "success" || scenario == "Redis unavailable" {
				// An already-ACKed delivery and a failed ACK are both retryable.
				expectHistory(mock).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Accepted))
				if err := store.AcknowledgeCompleted(context.Background(), q, d); err != nil {
					t.Fatal(err)
				}
				if pendingCount(t, q) != 0 {
					t.Fatal("ACK recovery failed")
				}
			}
		})
	}
}

func TestCompleteCommitsBeforeAck(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit succeeded", true: "commit outcome unknown"}[unknown], func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q, d, _ := pendingQueue(t)
			c, _ := NewConsumer(q, s)
			expectLoad(mock, requestRows(runRequest()))
			mock.ExpectBegin()
			expectOwned(mock, Running, 1, true)
			mock.ExpectExec("UPDATE judge_tasks SET status").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO submission_results").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE submissions SET status").WillReturnResult(sqlmock.NewResult(0, 1))
			commit := mock.ExpectCommit()
			if unknown {
				commit.WillReturnError(errors.New("commit response lost"))
			} else {
				expectHistory(mock).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Accepted))
			}
			err := c.Complete(context.Background(), store, ClaimedDelivery{Delivery: d, Lease: ownedLease(), State: ClaimAcquired}, acceptedRun())
			if unknown {
				if err != ErrExecutionStore || pendingCount(t, q) != 1 {
					t.Fatalf("unknown commit acknowledged: %v", err)
				}
				// On recovery MySQL reveals that the commit actually succeeded.
				expectHistory(mock).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Accepted))
				if err := store.AcknowledgeCompleted(context.Background(), q, d); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if pendingCount(t, q) != 0 {
				t.Fatal("committed result not acknowledged")
			}
		})
	}
}

func TestFinishSubmitCasesAreAtomicAndPrivate(t *testing.T) {
	data := submitRequest()
	for _, scenario := range []string{"success", "case insert failed", "compile error"} {
		t.Run(scenario, func(t *testing.T) {
			dataset := datasetStub{}
			for _, c := range data.Cases {
				dataset.data.Cases = append(dataset.data.Cases, testcase.Case{ID: c.ID, Input: c.Input, Output: c.Expected, Score: c.Score})
			}
			dataset.data.TotalBytes, dataset.data.TotalScore = 2, 100
			dataset.data.Digest = testcase.Fingerprint(dataset.data.Cases)
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, dataset)
			rows := sqlmock.NewRows([]string{"kind", "language", "source_code", "stdin", "total_cases", "snapshot_json", "storage_key", "digest", "case_count", "total_bytes", "total_score"}).AddRow(Submit, "python3", "print(1)", nil, 2, `{"time_limit_ms":1000,"memory_limit_mb":128}`, "key", hex.EncodeToString(dataset.data.Digest[:]), 2, 2, 100)
			expectLoad(mock, rows)
			result := acceptedSubmit()
			result.Output = "hidden output"
			result.ErrorMessage = "hidden diagnostic"
			phase := Running
			if scenario == "compile error" {
				phase = Compiling
				result = Result{Task: data.Task, Status: CompileError, TotalCases: 2, ErrorMessage: "compiler diagnostic", Output: "hidden output"}
			}
			mock.ExpectBegin()
			expectOwned(mock, phase, 1, true)
			mock.ExpectExec("UPDATE judge_tasks SET status").WillReturnResult(sqlmock.NewResult(0, 1))
			diagnostic := ""
			if scenario == "compile error" {
				diagnostic = "compiler diagnostic"
			}
			mock.ExpectExec("INSERT INTO submission_results").WithArgs(9, 1, 1, result.Status, result.Score, result.ExecutionTimeMS, 0, result.PassedCases, 2, "", diagnostic).WillReturnResult(sqlmock.NewResult(0, 1))
			for _, c := range result.Cases {
				u := mock.ExpectExec("INSERT INTO submission_case_results").WithArgs(9, 1, c.ID, c.Status, c.Score, c.ExecutionTimeMS, 0)
				if scenario == "case insert failed" {
					u.WillReturnError(errors.New("failed"))
					break
				}
				u.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if scenario == "case insert failed" {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE submissions SET status").WithArgs(result.Status, result.Score, result.ExecutionTimeMS, 0, result.PassedCases, 2, "", diagnostic, 9, 1, phase).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			err := store.Finish(context.Background(), ownedLease(), result)
			if scenario == "case insert failed" {
				if err != ErrExecutionStore {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
