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
}

// ErrNoMessages indicates that the queue has no messages available within the configured timeout.
var ErrNoMessages = errors.New("no messages available")
