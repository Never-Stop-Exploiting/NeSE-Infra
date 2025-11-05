package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/queue"
	"github.com/redis/go-redis/v9"
)

const payloadField = "data"

// Broker implements queue.Broker using Redis Streams with consumer groups.
type Broker struct {
	client redis.UniversalClient
}

// New creates a Redis-backed broker from configuration.
func New(cfg config.RedisConfig) (*Broker, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLSEnabled {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &Broker{
		client: client,
	}, nil
}

// Publish appends a message to the specified stream.
func (b *Broker) Publish(ctx context.Context, stream string, payload []byte) (string, error) {
	if stream == "" {
		return "", errors.New("stream name required")
	}
	res := b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			payloadField: payload,
		},
	})
	return res.Result()
}

// Claim reads messages for the given consumer group, blocking up to block time.
func (b *Broker) Claim(ctx context.Context, stream, group, consumer string, count int, block time.Duration) ([]queue.Message, error) {
	if stream == "" || group == "" || consumer == "" {
		return nil, errors.New("stream, group, and consumer are required")
	}
	if count <= 0 {
		count = 1
	}
	if err := b.ensureGroup(ctx, stream, group); err != nil {
		return nil, err
	}

	args := &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    int64(count),
	}
	if block > 0 {
		args.Block = block
	}

	streams, err := b.client.XReadGroup(ctx, args).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, queue.ErrNoMessages
		}
		return nil, err
	}
	if len(streams) == 0 {
		return nil, queue.ErrNoMessages
	}

	var messages []queue.Message
	for _, xstream := range streams {
		for _, msg := range xstream.Messages {
			raw, ok := msg.Values[payloadField]
			if !ok {
				continue
			}
			var body []byte
			switch v := raw.(type) {
			case string:
				body = []byte(v)
			case []byte:
				body = v
			default:
				body = []byte(fmt.Sprint(v))
			}
			messages = append(messages, queue.Message{
				ID:     msg.ID,
				Stream: xstream.Stream,
				Body:   body,
			})
		}
	}
	if len(messages) == 0 {
		return nil, queue.ErrNoMessages
	}
	return messages, nil
}

// Ack acknowledges a message.
func (b *Broker) Ack(ctx context.Context, stream, group, messageID string) error {
	if stream == "" || group == "" || messageID == "" {
		return errors.New("stream, group, and messageID are required")
	}
	return b.client.XAck(ctx, stream, group, messageID).Err()
}

// Nack requeues a message after a delay by acknowledging and re-adding the payload.
func (b *Broker) Nack(ctx context.Context, stream, group, messageID string, delay time.Duration) error {
	messages, err := b.client.XRange(ctx, stream, messageID, messageID).Result()
	if err != nil {
		return err
	}
	var payload []byte
	if len(messages) > 0 {
		if raw, ok := messages[0].Values[payloadField]; ok {
			switch v := raw.(type) {
			case string:
				payload = []byte(v)
			case []byte:
				payload = v
			default:
				payload = []byte(fmt.Sprint(v))
			}
		}
	}
	if err := b.Ack(ctx, stream, group, messageID); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	go func(data []byte) {
		if delay > 0 {
			time.Sleep(delay)
		}
		_ = b.client.XAdd(context.Background(), &redis.XAddArgs{
			Stream: stream,
			Values: map[string]any{payloadField: data},
		}).Err()
	}(append([]byte(nil), payload...))
	return nil
}

func (b *Broker) ensureGroup(ctx context.Context, stream, group string) error {
	err := b.client.XGroupCreateMkStream(ctx, stream, group, "$").Err()
	if err != nil && !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}
