package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"awdinfra/internal/config"
	"awdinfra/internal/logging"
	"awdinfra/internal/queue"
	"awdinfra/internal/queue/noop"
	redisbroker "awdinfra/internal/queue/redis"
	"awdinfra/internal/storage/memory"
	"awdinfra/internal/submitter"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	logger := logging.NewLogger(cfg.LogLevel)
	repo := memory.New()
	broker := createBroker(cfg, logger)
	service := submitter.New(cfg, logger, repo, broker)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := service.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("flag submitter exited", slog.Any("err", err))
		os.Exit(1)
	}
}

func createBroker(cfg config.Config, logger *slog.Logger) queue.Broker {
	broker, err := redisbroker.New(cfg.Redis)
	if err != nil {
		logger.Warn("redis broker unavailable, falling back to noop", slog.Any("err", err))
		return noop.New()
	}
	logger.Info("redis broker enabled", slog.String("addr", cfg.Redis.Addr))
	return broker
}
