package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"awdinfra/internal/config"
	"awdinfra/internal/controlplane"
	"awdinfra/internal/logging"
	"awdinfra/internal/queue"
	"awdinfra/internal/queue/noop"
	redisbroker "awdinfra/internal/queue/redis"
	"awdinfra/internal/storage/postgres"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if strings.TrimSpace(cfg.ControlPlane.AdminToken) == "" {
		log.Fatalf("CONTROL_PLANE_TOKEN cannot be empty")
	}

	logger := logging.NewLogger(cfg.LogLevel)
	repo, err := postgres.New(postgres.Config{
		DSN:             cfg.Postgres.DSN,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ArtifactDir:     cfg.Postgres.ArtifactDir,
	})
	if err != nil {
		logger.Error("failed to connect postgres", slog.Any("err", err))
		os.Exit(1)
	}
	defer repo.Close()
	broker := createBroker(cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := controlplane.New(cfg, logger, repo, broker)
	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("control plane terminated", slog.Any("err", err))
		os.Exit(1)
	}
}

func createBroker(cfg config.Config, logger *slog.Logger) queue.Broker {
	broker, err := redisbroker.New(cfg.Redis)
	if err != nil {
		logger.Warn("redis broker unavailable for control-plane, falling back to noop", slog.Any("err", err))
		return noop.New()
	}
	logger.Info("redis broker enabled", slog.String("addr", cfg.Redis.Addr))
	return broker
}
