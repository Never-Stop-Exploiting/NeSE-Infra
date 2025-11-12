package queue

import (
	"context"
	"errors"
	"time"
)

// Message represents a generic queue payload with acknowledgement semantics.
type Message struct {
	ID        string
	Stream    string
	Body      []byte
	VisibleAt time.Time
	Attempts  int
}

// Broker defines common queue operations leveraged by scheduler, runners, and submitters.
type Broker interface {
	Publish(ctx context.Context, stream string, payload []byte) (string, error)
	Claim(ctx context.Context, stream string, group string, consumer string, count int, block time.Duration) ([]Message, error)
	Ack(ctx context.Context, stream string, group string, messageID string) error
	Nack(ctx context.Context, stream string, group string, messageID string, delay time.Duration) error
	Reclaim(ctx context.Context, stream string, group string, consumer string, minIdle time.Duration, count int) ([]Message, error)
	Trim(ctx context.Context, stream string, strategy TrimStrategy) error
	PendingMinID(ctx context.Context, stream string, group string) (string, error)
}

// ErrNoMessages indicates that the queue has no messages available within the configured timeout.
var ErrNoMessages = errors.New("no messages available")

// TrimStrategy specifies how a broker should trim a stream.
type TrimStrategy struct {
	// MinID removes entries strictly older than the provided ID using MINID semantics.
	MinID string
	// MaxLen retains only the most recent MaxLen entries (approximate behavior).
	MaxLen int64
}
