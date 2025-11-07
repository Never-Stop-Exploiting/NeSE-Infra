package submitter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/queue"
	"awdinfra/internal/storage"
)

// Service processes captured flags and forwards them using pluggable adapters.
type Service struct {
	cfg    config.Config
	log    *slog.Logger
	repo   storage.Repository
	broker queue.Broker
	plugin Plugin
}

// New constructs a new submitter service.
func New(cfg config.Config, log *slog.Logger, repo storage.Repository, broker queue.Broker) *Service {
	plugin := NewHTTPPlugin(cfg.FlagSubmitter)
	return &Service{
		cfg:    cfg,
		log:    log,
		repo:   repo,
		broker: broker,
		plugin: plugin,
	}
}

// Run begins polling the flag queue until context cancellation.
func (s *Service) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			s.log.Info("flag submitter stopping")
			return ctx.Err()
		default:
			if err := s.poll(ctx); err != nil && err != queue.ErrNoMessages {
				s.log.Error("submitter poll failed", slog.Any("err", err))
				time.Sleep(time.Second)
			}
			time.Sleep(time.Millisecond * 200)
		}
	}
}

func (s *Service) poll(ctx context.Context) error {
	msgs, err := s.broker.Claim(ctx, s.cfg.FlagQueue.Name, "submitter", "submitter", 1, s.cfg.FlagSubmitter.RetryBackoff)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		if err := s.processMessage(ctx, msg); err != nil {
			s.log.Error("flag submission failed", slog.Any("err", err), slog.String("message_id", msg.ID))
			delay := s.cfg.FlagSubmitter.RetryBackoff
			if delay <= 0 {
				delay = time.Second * 10
			}
			_ = s.broker.Nack(ctx, s.cfg.FlagQueue.Name, "submitter", msg.ID, delay)
			continue
		}
		if err := s.broker.Ack(ctx, s.cfg.FlagQueue.Name, "submitter", msg.ID); err != nil {
			s.log.Error("ack failed", slog.Any("err", err), slog.String("msg", msg.ID))
		}
	}
	return nil
}

func (s *Service) processMessage(ctx context.Context, msg queue.Message) error {
	payload, err := DecodeMessage(msg.Body)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if payload.FlagID == "" || payload.FlagValue == "" {
		return fmt.Errorf("invalid payload")
	}

	request := SubmissionRequest{
		FlagID:      payload.FlagID,
		FlagValue:   payload.FlagValue,
		ChallengeID: payload.ChallengeID,
		TargetID:    payload.TargetID,
		Metadata:    payload.Metadata,
	}

	result := s.plugin.Submit(ctx, request)
	if result.Status == "" {
		result.Status = SubmissionStatusUnknown
	}
	statusCode := result.StatusCode
	message := result.Message
	if message == "" && result.Err != nil {
		message = result.Err.Error()
	}
	s.log.Info(
		"flag submitted",
		slog.String("flag_id", payload.FlagID),
		slog.String("status", string(result.Status)),
		slog.Int("status_code", statusCode),
		slog.String("message", message),
	)

	retryAfter := time.Time{}
	if result.RetryAfter != nil {
		retryAfter = time.Now().Add(time.Duration(*result.RetryAfter) * time.Second)
	}

	submittedAt := s.recordAttempt(ctx, payload.FlagID, statusCode, message, retryAfter)

	flag, err := s.repo.GetFlag(ctx, payload.FlagID)
	s.log.Info("fetched flag for update", slog.String("flag_id", payload.FlagID), slog.Any("err", err))
	if err == nil {
		switch result.Status {
		case SubmissionStatusAccept:
			flag.Status = domain.FlagStatusAccepted
		case SubmissionStatusDuplicate:
			flag.Status = domain.FlagStatusDuplicate
		case SubmissionStatusFail:
			flag.Status = domain.FlagStatusRejected
		default:
			flag.Status = domain.FlagStatusRetry
		}
		flag.LastSubmittedAt = submittedAt
		// s.log.Info("updating flag after submission", slog.String("flag_id", payload.FlagID), slog.String("status", string(flag.Status)), slog.Any("flag", flag))
		_ = s.repo.UpdateFlag(ctx, &flag)
	}

	logEntry := domain.LogEntry{
		Level:   "info",
		Source:  "flag-submitter",
		Message: fmt.Sprintf("flag %s submitted", payload.FlagID),
		FlagID:  payload.FlagID,
		Fields:  map[string]any{"status": string(result.Status), "status_code": statusCode, "message": message},
	}
	switch result.Status {
	case SubmissionStatusAccept:
		// keep default info level
	case SubmissionStatusFail:
		logEntry.Level = "warn"
	case SubmissionStatusDuplicate:
		logEntry.Level = "info"
	default:
		logEntry.Level = "error"
	}
	if result.Err != nil {
		logEntry.Fields["error"] = result.Err.Error()
	}
	_ = s.repo.AppendLog(ctx, logEntry)

	if result.Status == SubmissionStatusUnknown {
		return fmt.Errorf("submit status unknown for flag %s", payload.FlagID)
	}
	return nil
}

func (s *Service) recordAttempt(ctx context.Context, flagID string, status int, message string, next time.Time) time.Time {
	attempt := domain.SubmissionAttempt{
		FlagID:      flagID,
		SubmitterID: "http",
		StatusCode:  status,
		Error:       message,
		NextRetryAt: next,
	}
	attempt.SubmittedAt = time.Now().UTC()
	if err := s.repo.AddFlagAttempt(ctx, flagID, attempt); err != nil {
		s.log.Warn("store attempt failed", slog.Any("err", err), slog.String("flag_id", flagID))
	}
	return attempt.SubmittedAt
}
