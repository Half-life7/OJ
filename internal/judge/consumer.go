package judge

import "context"

// ClaimedDelivery couples Redis delivery identity with the authoritative lease.
// Busy/obsolete/invalid deliveries remain pending; only a later durable result
// or quarantine operation can authorize acknowledging the Redis message.
type ClaimedDelivery struct {
	Delivery Delivery
	Lease    Lease
	State    ClaimState
}

type Consumer struct {
	queue  *Queue
	leases *LeaseStore
}

func NewConsumer(queue *Queue, leases *LeaseStore) (*Consumer, error) {
	if queue == nil || leases == nil {
		return nil, ErrLeaseStore
	}
	return &Consumer{queue: queue, leases: leases}, nil
}

// Next makes one bounded receive/claim pass. It never executes user code and
// never acknowledges a message. Only ClaimAcquired carries a usable lease.
func (c *Consumer) Next(ctx context.Context) (ClaimedDelivery, bool, error) {
	d, ok, err := c.queue.Receive(ctx)
	if err != nil || !ok {
		return ClaimedDelivery{}, false, err
	}
	claimed := ClaimedDelivery{Delivery: d}
	if d.Invalid {
		return claimed, true, nil
	}
	lease, state, err := c.leases.Claim(ctx, d.Task)
	if err != nil {
		return ClaimedDelivery{}, false, err
	}
	claimed.Lease, claimed.State = lease, state
	return claimed, true, nil
}

// Complete commits the result before ACK. A failed/unknown commit never ACKs;
// callers recovering an obsolete delivery can use AcknowledgeCompleted instead.
func (c *Consumer) Complete(ctx context.Context, store *ExecutionStore, claimed ClaimedDelivery, result Result) error {
	if store == nil || store.leases != c.leases || claimed.State != ClaimAcquired || claimed.Delivery.Invalid || claimed.Delivery.Task != claimed.Lease.Task {
		return ErrLeaseLost
	}
	if result.Status == SystemError {
		request, err := store.LoadRequest(ctx, claimed.Lease)
		if err != nil {
			return err
		}
		if ValidateResult(request, result) != nil {
			return ErrInvalidResult
		}
		return c.Fail(ctx, store, claimed, FailureRunnerSystem)
	}
	if err := store.Finish(ctx, claimed.Lease, result); err != nil {
		return err
	}
	return store.AcknowledgeCompleted(ctx, c.queue, claimed.Delivery)
}

// Fail commits a retry/dead decision before ACKing the failed attempt. The next
// attempt or dead-letter stream publication proceeds independently via outbox.
func (c *Consumer) Fail(ctx context.Context, store *ExecutionStore, claimed ClaimedDelivery, reason FailureReason) error {
	if store == nil || store.leases != c.leases || claimed.State != ClaimAcquired || claimed.Delivery.Invalid || claimed.Delivery.Task != claimed.Lease.Task {
		return ErrLeaseLost
	}
	if _, err := store.Fail(ctx, claimed.Lease, reason); err != nil {
		return err
	}
	return store.AcknowledgeFailed(ctx, c.queue, claimed.Delivery)
}
