package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"awdinfra/internal/config"
	"awdinfra/internal/dispatcher"
	"awdinfra/internal/logging"
	redisbroker "awdinfra/internal/queue/redis"
	"awdinfra/internal/storage/postgres"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logger := logging.NewLogger(cfg.LogLevel).With("component", "dispatcher")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	broker, err := redisbroker.New(cfg.Redis)
	if err != nil {
		logger.Error("connect redis", slog.Any("err", err))
		os.Exit(1)
	}
	repo, err := postgres.New(postgres.Config{
		DSN:             cfg.Postgres.DSN,
		MaxIdleConns:    cfg.Postgres.MaxIdleConns,
		MaxOpenConns:    cfg.Postgres.MaxOpenConns,
		ConnMaxLifetime: cfg.Postgres.ConnMaxLifetime,
		ArtifactDir:     cfg.Postgres.ArtifactDir,
	})
	if err != nil {
		logger.Error("connect postgres", slog.Any("err", err))
		os.Exit(1)
	}
	defer repo.Close()

	d := dispatcher.New(cfg, logger, broker, repo)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("dispatcher stopped", slog.Any("err", err))
		os.Exit(1)
	}
	logger.Info("dispatcher exiting")
}
