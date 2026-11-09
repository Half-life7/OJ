package judge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatCancelsWork(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := withHeartbeat(ctx, time.Millisecond, func(context.Context) error {
		if calls.Add(1) >= 2 {
			return ErrLeaseLost
		}
		return nil
	}, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if err != ErrLeaseLost || calls.Load() < 2 {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
}

func TestHeartbeatDoesNotStartWithoutLease(t *testing.T) {
	started := false
	err := withHeartbeat(context.Background(), time.Second, func(context.Context) error { return ErrLeaseLost }, func(context.Context) error { started = true; return nil })
	if started || err != ErrLeaseLost {
		t.Fatal("work started without lease")
	}
}

func TestHeartbeatReturnsWorkError(t *testing.T) {
	want := errors.New("execution failed")
	err := withHeartbeat(context.Background(), time.Hour, func(context.Context) error { return nil }, func(context.Context) error { return want })
	if err != want {
		t.Fatalf("error=%v", err)
	}
}

func TestHeartbeatParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := withHeartbeat(ctx, time.Hour, func(context.Context) error { return nil }, func(ctx context.Context) error { cancel(); <-ctx.Done(); return nil })
	if err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
}
