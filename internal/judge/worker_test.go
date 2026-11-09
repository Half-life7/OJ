package judge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type workerRunner struct {
	readyErr, executeErr error
	calls                int
}

func (r *workerRunner) Ready(context.Context) error { return r.readyErr }
func (r *workerRunner) Execute(ctx context.Context, job RunnerJob, phase func(Status) error) (Result, error) {
	r.calls++
	if r.executeErr != nil {
		return Result{}, r.executeErr
	}
	if !validLease(Lease{Task: job.Request.Task, Token: job.LeaseToken}) {
		return Result{}, errors.New("invalid forwarded lease")
	}
	if err := phase(Running); err != nil {
		return Result{}, err
	}
	return acceptedRun(), nil
}

func unconsumedQueue(t *testing.T) *Queue {
	t.Helper()
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
	t.Cleanup(func() { client.Close() })
	q, _ := NewQueue(client, "test-worker", time.Second)
	if client.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: runRequest().Task.StreamValues()}).Err() != nil {
		t.Fatal("enqueue")
	}
	return q
}
func workerOwned(mock sqlmock.Sqlmock, status Status) {
	mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(1, status))
	mock.ExpectQuery("SELECT status, .* AS owned FROM judge_tasks").WithArgs(1, sqlmock.AnyArg(), 8, 9, 1).WillReturnRows(sqlmock.NewRows([]string{"status", "owned"}).AddRow(status, true))
}
func TestWorkerRunsAndPersistsBeforeAck(t *testing.T) {
	for _, scenario := range []string{"success", "heartbeat unavailable", "runner busy"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q := unconsumedQueue(t)
			consumer, _ := NewConsumer(q, s)
			runner := &workerRunner{}
			worker, _ := NewWorker(consumer, store, runner)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(1, Pending))
			mock.ExpectQuery("SELECT attempt, status, .* AS active FROM judge_tasks").WithArgs(8, 9, 1).WillReturnRows(sqlmock.NewRows([]string{"attempt", "status", "active"}).AddRow(1, Pending, false))
			mock.ExpectExec("UPDATE judge_tasks SET lease_token").WithArgs(sqlmock.AnyArg(), int64(60000000), 8).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE submissions SET status = 'Queued'").WithArgs(9, 1).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			renew := mock.ExpectExec("UPDATE judge_tasks SET lease_expires_at").WithArgs(int64(60000000), 8, 9, 1, 1, sqlmock.AnyArg())
			if scenario == "heartbeat unavailable" {
				renew.WillReturnError(errors.New("database error"))
			} else {
				renew.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT s.kind, s.language.*FROM submissions s JOIN problem_versions").WithArgs(8, 9, 1, 1, sqlmock.AnyArg()).WillReturnRows(requestRows(runRequest()))
				mock.ExpectQuery("SELECT t.status FROM judge_tasks").WithArgs(8, 9, 1, 1, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Queued))
				if scenario == "runner busy" {
					runner.executeErr = ErrRunnerBusy
				} else {
					mock.ExpectBegin()
					workerOwned(mock, Queued)
					mock.ExpectExec("UPDATE judge_tasks SET status").WithArgs(Running, 8, 9, 1, 1, sqlmock.AnyArg(), Queued).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("UPDATE submissions SET status").WithArgs(Running, 9, 1, Queued).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
					mock.ExpectQuery("SELECT s.kind, s.language.*FROM submissions s JOIN problem_versions").WithArgs(8, 9, 1, 1, sqlmock.AnyArg()).WillReturnRows(requestRows(runRequest()))
					mock.ExpectBegin()
					workerOwned(mock, Running)
					mock.ExpectExec("UPDATE judge_tasks SET status").WithArgs(Accepted, 8, 9, 1, 1, sqlmock.AnyArg(), Running).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("INSERT INTO submission_results").WithArgs(9, 1, 1, Accepted, 0, 2, 0, 1, 1, "1\n", "").WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectExec("UPDATE submissions SET status").WithArgs(Accepted, 0, 2, 0, 1, 1, "1\n", "", 9, 1, Running).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
					expectHistory(mock).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(Accepted))
				}
			}
			worked, err := worker.Step(context.Background())
			if !worked {
				t.Fatal("did not receive task")
			}
			want := error(nil)
			if scenario == "heartbeat unavailable" {
				want = ErrLeaseStore
			}
			if scenario == "runner busy" {
				want = ErrRunnerBusy
			}
			if err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
			count := int64(1)
			if scenario == "success" {
				count = 0
			}
			if pendingCount(t, q) != count {
				t.Fatal("premature or missing ACK")
			}
			if scenario == "heartbeat unavailable" && runner.calls != 0 {
				t.Fatal("executed without renewed lease")
			}
		})
	}
}
func TestWorkerNotReadyDoesNotClaim(t *testing.T) {
	s, _ := leaseFixture(t)
	store, _ := NewExecutionStore(s, datasetStub{})
	q := unconsumedQueue(t)
	c, _ := NewConsumer(q, s)
	w, _ := NewWorker(c, store, &workerRunner{readyErr: ErrRunnerUnavailable})
	worked, err := w.Step(context.Background())
	if worked || err != ErrRunnerUnavailable {
		t.Fatal("unready runner admitted work")
	}
	groups, err := q.client.XInfoGroups(context.Background(), Stream).Result()
	if err != nil || len(groups) != 0 {
		t.Fatal("queue was claimed before readiness")
	}
}
