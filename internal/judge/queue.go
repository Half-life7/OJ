package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrQueue = errors.New("judge queue operation failed")

// Delivery retains the message ID even when its envelope is malformed. Invalid
// messages must be quarantined durably before a future consumer acknowledges them.
type Delivery struct {
	ID          string
	Task        TaskEnvelope
	Invalid     bool
	Fingerprint string
}

// Queue serializes reads for one consumer, including the XAUTOCLAIM scan cursor.
// Each worker process should use a unique consumer name.
type Queue struct {
	client   *redis.Client
	consumer string
	idle     time.Duration
	mu       sync.Mutex
	cursor   string
}

func NewQueue(client *redis.Client, consumer string, idle time.Duration) (*Queue, error) {
	if client == nil || len(consumer) == 0 || len(consumer) > 128 || idle < time.Second || idle > time.Hour {
		return nil, errors.New("queue requires Redis, consumer name and bounded reclaim interval")
	}
	for _, c := range consumer {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil, errors.New("invalid judge consumer name")
		}
	}
	return &Queue{client: client, consumer: consumer, idle: idle, cursor: "0-0"}, nil
}

// EnsureGroup starts at 0 so messages published before worker startup are read.
func (q *Queue) EnsureGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, Stream, ConsumerGroup, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP ") {
		return ErrQueue
	}
	return nil
}

// Receive first scans pending messages for abandoned deliveries, then reads one
// new message without blocking. No ACK is issued by this transport. Database
// leases, rather than Redis delivery ownership, decide who may execute a task.
func (q *Queue) Receive(ctx context.Context) (Delivery, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := q.EnsureGroup(ctx); err != nil {
		return Delivery{}, false, err
	}
	messages, cursor, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: Stream, Group: ConsumerGroup, Consumer: q.consumer,
		MinIdle: q.idle, Start: q.cursor, Count: 1,
	}).Result()
	if err != nil {
		return Delivery{}, false, ErrQueue
	}
	q.cursor = cursor
	if len(messages) > 0 {
		return delivery(messages[0]), true, nil
	}
	streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: ConsumerGroup, Consumer: q.consumer, Streams: []string{Stream, ">"}, Count: 1, Block: -1,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, ErrQueue
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return Delivery{}, false, nil
	}
	return delivery(streams[0].Messages[0]), true, nil
}

func delivery(message redis.XMessage) Delivery {
	envelope, err := ParseEnvelope(message.Values)
	raw, _ := json.Marshal(message.Values)
	digest := sha256.Sum256(raw)
	return Delivery{ID: message.ID, Task: envelope, Invalid: err != nil, Fingerprint: hex.EncodeToString(digest[:])}
}
