package judge

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Worker struct {
	consumer *Consumer
	store    *ExecutionStore
	runner   Runner
}

func NewWorker(consumer *Consumer, store *ExecutionStore, runner Runner) (*Worker, error) {
	if consumer == nil || store == nil || consumer.leases != store.leases || runner == nil {
		return nil, ErrExecutionStore
	}
	return &Worker{consumer: consumer, store: store, runner: runner}, nil
}

func (s *ExecutionStore) Phase(ctx context.Context, lease Lease) (Status, error) {
	if !validLease(lease) {
		return "", ErrLeaseLost
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var phase Status
	err := s.leases.db.GetContext(ctx, &phase, `SELECT t.status FROM judge_tasks t JOIN submissions s ON s.id = t.submission_id AND s.generation = t.generation WHERE t.id = ? AND t.submission_id = ? AND t.generation = ? AND t.attempt = ? AND t.lease_token = ? AND t.lease_expires_at > UTC_TIMESTAMP(6) AND t.status = s.status`, lease.Task.TaskID, lease.Task.SubmissionID, lease.Task.Generation, lease.Task.Attempt, lease.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", ErrExecutionStore
	}
	if phase != Queued && phase != Compiling && phase != Running {
		return "", ErrLeaseLost
	}
	return phase, nil
}

// Step is serial per worker. Runner readiness is checked before queue admission.
func (w *Worker) Step(ctx context.Context) (bool, error) {
	if err := w.runner.Ready(ctx); err != nil {
		return false, err
	}
	claimed, ok, err := w.consumer.Next(ctx)
	if err != nil || !ok {
		return false, err
	}
	if claimed.Delivery.Invalid {
		return true, w.store.Quarantine(ctx, w.consumer.queue, claimed.Delivery)
	}
	if claimed.State == ClaimBusy {
		return true, nil
	}
	if claimed.State == ClaimObsolete {
		err := w.store.AcknowledgeCompleted(ctx, w.consumer.queue, claimed.Delivery)
		if errors.Is(err, ErrLeaseLost) {
			err = w.store.AcknowledgeFailed(ctx, w.consumer.queue, claimed.Delivery)
		}
		if errors.Is(err, ErrLeaseLost) {
			err = w.store.QuarantineUnresolved(ctx, w.consumer.queue, claimed.Delivery)
		}
		return true, err
	}
	var result Result
	err = w.store.leases.WithHeartbeat(ctx, claimed.Lease, func(work context.Context) error {
		request, err := w.store.LoadRequest(work, claimed.Lease)
		if err != nil {
			return err
		}
		phase, err := w.store.Phase(work, claimed.Lease)
		if err != nil {
			return err
		}
		result, err = w.runner.Execute(work, RunnerJob{Request: request, LeaseToken: claimed.Lease.Token}, func(to Status) error {
			if phase == Running || phase == to {
				return nil
			} // replay after a lease takeover
			if err := w.store.Advance(work, claimed.Lease, to); err != nil {
				return err
			}
			phase = to
			return nil
		})
		return err
	})
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err == nil {
		return true, w.consumer.Complete(ctx, w.store, claimed, result)
	}
	var reason FailureReason
	switch {
	case errors.Is(err, ErrRunnerUnavailable):
		reason = FailureRunnerUnavailable
	case errors.Is(err, ErrRunnerProtocol):
		reason = FailureRunnerProtocol
	case errors.Is(err, ErrRequestData):
		reason = FailureDataUnavailable
	default:
		return true, err // lost lease or unknown database write: leave pending
	}
	return true, w.consumer.Fail(ctx, w.store, claimed, reason)
}
