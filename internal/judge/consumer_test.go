package judge

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestConsumerReclaimsWithNewLease(t *testing.T) {
	ctx := context.Background()
	r := miniredis.RunT(t)
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	r.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
	defer client.Close()
	s, mock := leaseFixture(t)
	envelope := TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 1}
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: Stream, Values: envelope.StreamValues()}).Result()
	if err != nil {
		t.Fatal(err)
	}
	var oldToken string
	for index, worker := range []string{"first", "replacement"} {
		q, _ := NewQueue(client, worker, time.Second)
		c, err := NewConsumer(q, s)
		if err != nil {
			t.Fatal(err)
		}
		status := Pending
		if index == 1 {
			status = Queued
			r.SetTime(now.Add(2 * time.Minute))
		}
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(1, status))
		mock.ExpectQuery("SELECT attempt, status, .* FROM judge_tasks").WithArgs(8, 9, 1).WillReturnRows(sqlmock.NewRows([]string{"attempt", "status", "active"}).AddRow(1, status, false))
		mock.ExpectExec("UPDATE judge_tasks SET lease_token").WithArgs(sqlmock.AnyArg(), int64(60000000), 8).WillReturnResult(sqlmock.NewResult(0, 1))
		if index == 0 {
			mock.ExpectExec("UPDATE submissions SET status = 'Queued'").WithArgs(9, 1).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectCommit()
		claimed, ok, err := c.Next(ctx)
		if err != nil || !ok || claimed.State != ClaimAcquired || claimed.Delivery.ID != id || claimed.Lease.Task != envelope {
			t.Fatalf("claim=%+v ok=%v error=%v", claimed.State, ok, err)
		}
		if index == 0 {
			oldToken = claimed.Lease.Token
		} else if claimed.Lease.Token == oldToken {
			t.Fatal("recovery reused previous token")
		}
	}
	pending, err := client.XPending(ctx, Stream, ConsumerGroup).Result()
	if err != nil || pending.Count != 1 {
		t.Fatalf("task was acknowledged before result persistence: %v %v", pending, err)
	}
}
