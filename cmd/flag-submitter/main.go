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
	"awdinfra/internal/submitter"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	logger := logging.NewLogger(cfg.LogLevel)
	broker := createBroker(cfg, logger)
	cpClient, err := submitter.NewControlPanelClient(cfg, logger)
	if err != nil {
		logger.Error("failed to create control panel client", slog.Any("err", err))
		os.Exit(1)
	}
	service := submitter.New(cfg, logger, broker, cpClient, cfg.FlagSubmitter.ID)

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
