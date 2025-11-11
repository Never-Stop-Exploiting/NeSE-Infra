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
	"awdinfra/internal/runner"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	logger := logging.NewLogger(cfg.LogLevel)
	broker, brokerStatus := createBroker(cfg, logger)

	var controlClient *runner.ControlPanelClient
	if cfg.Runner.ControlPanelURL != "" {
		client, err := runner.NewControlPanelClient(cfg, logger)
		if err != nil {
			logger.Warn("control panel client disabled", slog.Any("err", err))
		} else {
			controlClient = client
		}
	} else {
		logger.Warn("CONTROL_PANEL_URL not configured; runner will operate in offline mode")
	}

	agent := runner.New(cfg, logger, broker, controlClient, brokerStatus)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("runner exited", slog.Any("err", err))
		os.Exit(1)
	}
}

func createBroker(cfg config.Config, logger *slog.Logger) (queue.Broker, string) {
	broker, err := redisbroker.New(cfg.Redis)
	if err != nil {
		logger.Warn("redis broker unavailable, falling back to noop", slog.Any("err", err))
		return noop.New(), "disconnected"
	}
	logger.Info("redis broker enabled", slog.String("addr", cfg.Redis.Addr))
	return broker, "connected"
}
