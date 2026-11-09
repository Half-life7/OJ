package judge

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestConsumerFailureCommitsBeforeAck(t *testing.T) {
	for _, scenario := range []string{"failure committed", "commit unknown", "runner SystemError"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q, d, _ := pendingQueue(t)
			consumer, _ := NewConsumer(q, s)
			claimed := ClaimedDelivery{Delivery: d, Lease: ownedLease(), State: ClaimAcquired}
			reason := FailureDataUnavailable
			if scenario == "runner SystemError" {
				expectLoad(mock, requestRows(runRequest()))
				reason = FailureRunnerSystem
			}
			expectFailureOwnership(mock, ownedLease(), true)
			mock.ExpectExec("UPDATE judge_tasks SET attempt").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE submissions SET status = 'Pending'").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(8, 9, 1, 2, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO judge_attempt_failures").WithArgs(8, 9, 1, 1, reason, FailureRetry).WillReturnResult(sqlmock.NewResult(0, 1))
			commit := mock.ExpectCommit()
			if scenario == "commit unknown" {
				commit.WillReturnError(errors.New("commit response lost"))
			} else {
				expectFailedProof(mock, 1).WillReturnRows(sqlmock.NewRows([]string{"disposition"}).AddRow(FailureRetry))
			}
			var err error
			if scenario == "runner SystemError" {
				err = consumer.Complete(context.Background(), store, claimed, Result{Task: ownedLease().Task, Status: SystemError, TotalCases: 1})
			} else {
				err = consumer.Fail(context.Background(), store, claimed, reason)
			}
			if scenario == "commit unknown" {
				if err != ErrExecutionStore || pendingCount(t, q) != 1 {
					t.Fatalf("unknown commit acknowledged: %v", err)
				}
				// Recovery observes attempt 2 and must not create attempt 3 by
				// replaying attempt 1's failure or accept its late result.
				expectFailureOwnership(mock, ownedLease(), false)
				mock.ExpectRollback()
				if _, err := store.Fail(context.Background(), ownedLease(), reason); err != ErrLeaseLost {
					t.Fatalf("old attempt retried again: %v", err)
				}
				expectFailedProof(mock, 1).WillReturnRows(sqlmock.NewRows([]string{"disposition"}).AddRow(FailureRetry))
				if err := store.AcknowledgeFailed(context.Background(), q, d); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if pendingCount(t, q) != 0 {
				t.Fatal("committed failure was not acknowledged")
			}
		})
	}
}

func TestInvalidSystemResultDoesNotConsumeRetry(t *testing.T) {
	s, mock := leaseFixture(t)
	store, _ := NewExecutionStore(s, datasetStub{})
	q, d, _ := pendingQueue(t)
	consumer, _ := NewConsumer(q, s)
	expectLoad(mock, requestRows(runRequest()))
	bad := Result{Task: ownedLease().Task, Status: SystemError, Score: 1, TotalCases: 1}
	err := consumer.Complete(context.Background(), store, ClaimedDelivery{Delivery: d, Lease: ownedLease(), State: ClaimAcquired}, bad)
	if err != ErrInvalidResult || pendingCount(t, q) != 1 {
		t.Fatalf("bad SystemError consumed retry or ACKed: %v", err)
	}
}
