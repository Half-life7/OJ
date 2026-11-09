package judge

import (
	"context"
	"time"
)

// WithHeartbeat runs cooperative work under a renewing lease. Work must honor
// cancellation; its caller must still fence all phase/result mutations.
func (s *LeaseStore) WithHeartbeat(ctx context.Context, lease Lease, work func(context.Context) error) error {
	if work == nil || !validLease(lease) {
		return ErrLeaseLost
	}
	return withHeartbeat(ctx, s.ttl/3, func(ctx context.Context) error { return s.Renew(ctx, lease) }, work)
}

func withHeartbeat(ctx context.Context, interval time.Duration, renew func(context.Context) error, work func(context.Context) error) error {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Verify ownership before handing control to potentially expensive work.
	if err := renew(ctx); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := renew(ctx); err != nil {
					if ctx.Err() != nil {
						done <- nil
						return
					}
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	err := work(ctx)
	cancel()
	if renewalError := <-done; renewalError != nil {
		return renewalError
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return err
}
