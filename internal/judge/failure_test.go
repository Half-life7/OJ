package judge

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectFailureOwnership(mock sqlmock.Sqlmock, lease Lease, owned bool) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(lease.Task.SubmissionID).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(lease.Task.Generation, Running))
	mock.ExpectQuery("SELECT status, .* AS owned FROM judge_tasks").WithArgs(lease.Task.Attempt, lease.Token, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation).WillReturnRows(sqlmock.NewRows([]string{"status", "owned"}).AddRow(Running, owned))
}

func TestFailureRetryBudgetAndRollback(t *testing.T) {
	for attempt := uint32(1); attempt < MaxAttempts; attempt++ {
		for _, scenario := range []string{"success", "lease lost", "task CAS lost", "summary failed", "outbox failed", "failure log failed", "commit unknown"} {
			t.Run(string(rune('0'+attempt))+"/"+scenario, func(t *testing.T) {
				s, mock := leaseFixture(t)
				store, _ := NewExecutionStore(s, datasetStub{err: errors.New("data unavailable")})
				lease := ownedLease()
				lease.Task.Attempt = attempt
				expectFailureOwnership(mock, lease, scenario != "lease lost")
				want := error(nil)
				if scenario == "lease lost" {
					want = ErrLeaseLost
					mock.ExpectRollback()
				} else {
					count := int64(1)
					if scenario == "task CAS lost" {
						count = 0
					}
					mock.ExpectExec("UPDATE judge_tasks SET attempt = attempt \\+ 1").WithArgs(8, 9, 1, attempt, lease.Token, Running).WillReturnResult(sqlmock.NewResult(0, count))
					if count == 0 {
						want = ErrLeaseLost
						mock.ExpectRollback()
					} else {
						u := mock.ExpectExec("UPDATE submissions SET status = 'Pending'").WithArgs(9, 1, Running)
						if scenario == "summary failed" {
							u.WillReturnError(errors.New("failed"))
							want = ErrExecutionStore
							mock.ExpectRollback()
						} else {
							u.WillReturnResult(sqlmock.NewResult(0, 1))
							u = mock.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(8, 9, 1, attempt+1, int64(1<<(attempt-1)))
							if scenario == "outbox failed" {
								u.WillReturnError(errors.New("failed"))
								want = ErrExecutionStore
								mock.ExpectRollback()
							} else {
								u.WillReturnResult(sqlmock.NewResult(0, 1))
								u = mock.ExpectExec("INSERT INTO judge_attempt_failures").WithArgs(8, 9, 1, attempt, FailureDataUnavailable, FailureRetry)
								if scenario == "failure log failed" {
									u.WillReturnError(errors.New("failed"))
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
				}
				disposition, err := store.Fail(context.Background(), lease, FailureDataUnavailable)
				if err != want {
					t.Fatalf("error=%v want=%v", err, want)
				}
				if want == nil {
					if disposition != FailureRetry {
						t.Fatal(disposition)
					}
				} else if disposition != "" {
					t.Fatal("uncommitted failure reported successful")
				}
			})
		}
	}
}

func TestFourthFailureCreatesAtomicDeadLetter(t *testing.T) {
	for _, scenario := range []string{"success", "history failed", "summary failed", "failure log failed", "dead letter failed", "commit unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{err: errors.New("dataset unreadable")})
			lease := ownedLease()
			lease.Task.Attempt = MaxAttempts
			expectFailureOwnership(mock, lease, true)
			mock.ExpectExec("UPDATE judge_tasks SET status = .*lease_token = NULL, lease_expires_at = NULL").WithArgs(SystemError, 8, 9, 1, MaxAttempts, lease.Token, Running).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT total_cases FROM submissions").WithArgs(9, 1).WillReturnRows(sqlmock.NewRows([]string{"total_cases"}).AddRow(2))
			// Each step is a separate failure boundary, all inside one transaction.
			steps := []struct {
				query   string
				args    []driver.Value
				failure string
			}{
				{"INSERT INTO submission_results", []driver.Value{9, 1, MaxAttempts, 2}, "history failed"},
				{"UPDATE submissions SET status = 'SystemError'", []driver.Value{9, 1, Running}, "summary failed"},
				{"INSERT INTO judge_attempt_failures", []driver.Value{8, 9, 1, MaxAttempts, FailureRunnerUnavailable, FailureDead}, "failure log failed"},
				{"INSERT INTO judge_dead_letters", []driver.Value{8, 9, 1, MaxAttempts, FailureRunnerUnavailable}, "dead letter failed"},
			}
			failed := false
			for _, step := range steps {
				u := mock.ExpectExec(step.query).WithArgs(step.args...)
				if scenario == step.failure {
					u.WillReturnError(errors.New("failed"))
					mock.ExpectRollback()
					failed = true
					break
				}
				u.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if !failed {
				c := mock.ExpectCommit()
				if scenario == "commit unknown" {
					c.WillReturnError(errors.New("unknown"))
				}
			}
			disposition, err := store.Fail(context.Background(), lease, FailureRunnerUnavailable)
			if scenario == "success" {
				if err != nil || disposition != FailureDead {
					t.Fatalf("disposition=%s error=%v", disposition, err)
				}
			} else if err != ErrExecutionStore || disposition != "" {
				t.Fatalf("disposition=%s error=%v", disposition, err)
			}
		})
	}
}

func TestFailureRejectsUncontrolledReason(t *testing.T) {
	s, _ := leaseFixture(t)
	store, _ := NewExecutionStore(s, datasetStub{})
	if _, err := store.Fail(context.Background(), ownedLease(), FailureReason("private compiler error or source")); err != ErrInvalidResult {
		t.Fatal(err)
	}
	l := ownedLease()
	l.Task.Attempt = 5
	if _, err := store.Fail(context.Background(), l, FailureRunnerSystem); err != ErrLeaseLost {
		t.Fatal(err)
	}
	if err := store.Finish(context.Background(), ownedLease(), Result{Status: SystemError}); err != ErrInvalidResult {
		t.Fatal("SystemError bypassed retry policy")
	}
}

func expectFailedProof(mock sqlmock.Sqlmock, attempt uint32) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("SELECT disposition FROM judge_attempt_failures").WithArgs(8, 9, 1, attempt)
}

func TestFailedAckNeedsDurableDecision(t *testing.T) {
	for _, scenario := range []string{"retry committed", "not committed", "invalid disposition", "Redis failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q, d, r := pendingQueue(t)
			u := expectFailedProof(mock, 1)
			want := error(nil)
			switch scenario {
			case "not committed":
				u.WillReturnError(sql.ErrNoRows)
				want = ErrLeaseLost
			case "invalid disposition":
				u.WillReturnRows(sqlmock.NewRows([]string{"disposition"}).AddRow("dead"))
				want = ErrExecutionStore
			default:
				u.WillReturnRows(sqlmock.NewRows([]string{"disposition"}).AddRow("retry"))
			}
			if scenario == "Redis failure" {
				r.SetError("unavailable")
				want = ErrQueue
			}
			if err := store.AcknowledgeFailed(context.Background(), q, d); err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
			r.SetError("")
			count := int64(1)
			if want == nil {
				count = 0
			}
			if pendingCount(t, q) != count {
				t.Fatal("uncommitted failure was acknowledged")
			}
			if scenario == "Redis failure" {
				expectFailedProof(mock, 1).WillReturnRows(sqlmock.NewRows([]string{"disposition"}).AddRow("retry"))
				if err := store.AcknowledgeFailed(context.Background(), q, d); err != nil {
					t.Fatal(err)
				}
				if pendingCount(t, q) != 0 {
					t.Fatal("failed ACK did not recover")
				}
			}
		})
	}
}
