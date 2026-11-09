package judge

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestQueueDeliveryRecovery(t *testing.T) {
	r := miniredis.RunT(t)
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	r.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	envelope := TaskEnvelope{TaskID: 1, SubmissionID: 2, Generation: 3, Attempt: 1}
	// Publishing before group creation must not strand the message.
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: Stream, Values: envelope.StreamValues()}).Result()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := NewQueue(client, "worker-1", time.Second)
	second, _ := NewQueue(client, "worker-2", time.Second)
	d, ok, err := first.Receive(ctx)
	if err != nil || !ok || d.ID != id || d.Task != envelope || d.Invalid {
		t.Fatalf("delivery=%+v ok=%v err=%v", d, ok, err)
	}
	if _, ok, err := second.Receive(ctx); err != nil || ok {
		t.Fatalf("fresh delivery reclaimed: ok=%v err=%v", ok, err)
	}
	// miniredis uses its clock for pending idle time.
	r.SetTime(now.Add(2 * time.Second))
	d, ok, err = second.Receive(ctx)
	if err != nil || !ok || d.ID != id || d.Task != envelope {
		t.Fatalf("reclaimed=%+v ok=%v err=%v", d, ok, err)
	}
	pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: Stream, Group: ConsumerGroup, Start: "-", End: "+", Count: 10}).Result()
	if err != nil || len(pending) != 1 || pending[0].Consumer != "worker-2" {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	// Receive never acknowledges either valid or invalid messages.
	badID, err := client.XAdd(ctx, &redis.XAddArgs{Stream: Stream, Values: map[string]any{"source_code": "private"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	d, ok, err = second.Receive(ctx)
	if err != nil || !ok || !d.Invalid || d.ID != badID || d.Task != (TaskEnvelope{}) {
		t.Fatalf("invalid=%+v ok=%v err=%v", d, ok, err)
	}
	count, err := client.XPending(ctx, Stream, ConsumerGroup).Result()
	if err != nil || count.Count != 2 {
		t.Fatalf("pending=%v err=%v", count, err)
	}
	r.SetError("unavailable")
	if _, _, err := second.Receive(ctx); err != ErrQueue {
		t.Fatalf("redis error=%v", err)
	}
	r.SetError("")
	if _, ok, err := second.Receive(ctx); err != nil || ok {
		t.Fatalf("recovery: ok=%v err=%v", ok, err)
	}
}

func TestQueueConfiguration(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	for _, name := range []string{"", "worker name", "中文", "worker\n"} {
		if _, err := NewQueue(client, name, time.Second); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := NewQueue(nil, "worker", time.Second); err == nil {
		t.Fatal("nil Redis accepted")
	}
	if _, err := NewQueue(client, "worker", 0); err == nil {
		t.Fatal("zero idle accepted")
	}
}
