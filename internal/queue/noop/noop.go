package noop

import (
	"context"
	"time"

	"awdinfra/internal/queue"
)

// Broker implements queue.Broker with no-op behavior to enable local scaffolding.
type Broker struct{}

func New() *Broker { return &Broker{} }

func (b *Broker) Publish(ctx context.Context, stream string, payload []byte) (string, error) {
	return "", ctx.Err()
}

func (b *Broker) Claim(ctx context.Context, stream string, group string, consumer string, count int, block time.Duration) ([]queue.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, queue.ErrNoMessages
	}
}

func (b *Broker) Ack(ctx context.Context, stream string, group string, messageID string) error {
	return ctx.Err()
}

func (b *Broker) Nack(ctx context.Context, stream string, group string, messageID string, delay time.Duration) error {
	return ctx.Err()
}

func (b *Broker) Reclaim(ctx context.Context, stream string, group string, consumer string, minIdle time.Duration, count int) ([]queue.Message, error) {
	return nil, ctx.Err()
}

func (b *Broker) Trim(ctx context.Context, stream string, strategy queue.TrimStrategy) error {
	return ctx.Err()
}

func (b *Broker) PendingMinID(ctx context.Context, stream string, group string) (string, error) {
	return "", ctx.Err()
}
