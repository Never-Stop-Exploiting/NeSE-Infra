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

// Reclaim transfers idle pending messages to the specified consumer for reprocessing.
func (b *Broker) Reclaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]queue.Message, error) {
	if stream == "" || group == "" || consumer == "" {
		return nil, errors.New("stream, group, and consumer are required")
	}
	if count <= 0 {
		count = 10
	}
	if minIdle < 0 {
		minIdle = time.Second
	}
	if err := b.ensureGroup(ctx, stream, group); err != nil {
		return nil, err
	}

	var result []queue.Message
	start := "0-0"
	remaining := count

	for remaining > 0 {
		xmsgs, nextID, err := b.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   stream,
			Group:    group,
			Consumer: consumer,
			MinIdle:  minIdle,
			Start:    start,
			Count:    int64(remaining),
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				break
			}
			return nil, err
		}
		for _, msg := range xmsgs {
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
			result = append(result, queue.Message{
				ID:     msg.ID,
				Stream: stream,
				Body:   body,
			})
		}
		if nextID == "0-0" || len(xmsgs) == 0 {
			break
		}
		start = nextID
		remaining = count - len(result)
	}
	return result, nil
}

func (b *Broker) ensureGroup(ctx context.Context, stream, group string) error {
	err := b.client.XGroupCreateMkStream(ctx, stream, group, "$").Err()
	if err != nil && !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Trim applies the provided trimming strategy to a stream.
func (b *Broker) Trim(ctx context.Context, stream string, strategy queue.TrimStrategy) error {
	if stream == "" {
		return errors.New("stream required")
	}
	if strategy.MinID == "" && strategy.MaxLen <= 0 {
		return nil
	}
	if strategy.MinID != "" {
		return b.client.Do(ctx, "XTRIM", stream, "MINID", "~", strategy.MinID).Err()
	}
	return b.client.Do(ctx, "XTRIM", stream, "MAXLEN", "~", strategy.MaxLen).Err()
}

// PendingMinID returns the smallest pending ID for the specified group.
func (b *Broker) PendingMinID(ctx context.Context, stream, group string) (string, error) {
	if stream == "" || group == "" {
		return "", errors.New("stream and group required")
	}
	if err := b.ensureGroup(ctx, stream, group); err != nil {
		return "", err
	}
	entries, err := b.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  1,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return entries[0].ID, nil
}

// DeleteStream removes the entire stream key.
func (b *Broker) DeleteStream(ctx context.Context, stream string) error {
	if stream == "" {
		return errors.New("stream required")
	}
	return b.client.Del(ctx, stream).Err()
}
