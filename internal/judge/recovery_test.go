package judge

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/redis/go-redis/v9"
)

func TestReconcileReopensLostMessages(t *testing.T) {
	for _, scenario := range []string{"present", "lost", "wrong payload", "Redis unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			q, delivery, r := pendingQueue(t)
			dispatcher, _ := NewDispatcher(s.db, q.client)
			mock.ExpectQuery("SELECT o.id, o.stream_id.*FROM judge_task_outbox").WithArgs(uint64(0)).WillReturnRows(sqlmock.NewRows([]string{"id", "stream_id", "task_id", "submission_id", "generation", "attempt"}).AddRow(7, delivery.ID, 8, 9, 1, 1))
			if scenario == "lost" || scenario == "wrong payload" {
				q.client.XDel(context.Background(), Stream, delivery.ID)
			}
			if scenario == "wrong payload" { // a reused stream ID must not block recovery
				if err := q.client.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, ID: delivery.ID, Values: map[string]any{"bad": "payload"}}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "Redis unavailable" {
				r.SetError("unavailable")
			} else if scenario != "present" {
				mock.ExpectExec("UPDATE judge_task_outbox SET dispatched_at = NULL, stream_id = NULL").WithArgs(7, delivery.ID).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			scanned, err := dispatcher.ReconcileOne(context.Background(), false)
			if scenario == "Redis unavailable" {
				if err != ErrDispatch || scanned {
					t.Fatal("Redis outage reopened intent")
				}
			} else if err != nil || !scanned {
				t.Fatal(err)
			}
		})
	}
}

func TestQuarantineCommitsDigestBeforeAck(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "database unavailable"}[fail], func(t *testing.T) {
			s, mock := leaseFixture(t)
			store, _ := NewExecutionStore(s, datasetStub{})
			q, _, _ := pendingQueue(t)
			id, err := q.client.XAdd(context.Background(), &redis.XAddArgs{Stream: Stream, Values: map[string]any{"source_code": "do not retain raw payload"}}).Result()
			if err != nil {
				t.Fatal(err)
			}
			d, ok, err := q.Receive(context.Background())
			if err != nil || !ok || !d.Invalid || d.ID != id {
				t.Fatal("invalid delivery")
			}
			u := mock.ExpectExec("INSERT INTO judge_message_quarantine").WithArgs(d.ID, d.Fingerprint, "invalid_envelope")
			if fail {
				u.WillReturnError(errors.New("commit unknown"))
			} else {
				u.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			err = store.Quarantine(context.Background(), q, d)
			if fail {
				if err != ErrExecutionStore || pendingCount(t, q) != 2 {
					t.Fatal("quarantine ACK before durable save")
				}
			} else if err != nil || pendingCount(t, q) != 1 {
				t.Fatal(err)
			}
		})
	}
}
