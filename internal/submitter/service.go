package submitter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	client *http.Client
}

// New constructs a new submitter service.
func New(cfg config.Config, log *slog.Logger, repo storage.Repository, broker queue.Broker) *Service {
	return &Service{
		cfg:    cfg,
		log:    log,
		repo:   repo,
		broker: broker,
		client: &http.Client{Timeout: cfg.FlagSubmitter.RequestTimeout},
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

	reqBody, err := json.Marshal(map[string]string{"flag": payload.FlagValue})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.FlagSubmitter.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		s.recordAttempt(ctx, payload.FlagID, 0, err.Error(), time.Now().Add(s.cfg.FlagSubmitter.RetryBackoff))
		return err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.log.Info("flag accepted", slog.String("flag_id", payload.FlagID), slog.Int("status", resp.StatusCode))
		submittedAt := s.recordAttempt(ctx, payload.FlagID, resp.StatusCode, bodyStr, time.Time{})
		flag, err := s.repo.GetFlag(ctx, payload.FlagID)
		if err == nil {
			flag.Status = domain.FlagStatusAccepted
			flag.LastSubmittedAt = submittedAt
			_ = s.repo.UpdateFlag(ctx, &flag)
		}
		_ = s.repo.AppendLog(ctx, domain.LogEntry{
			Level:   "info",
			Source:  "flag-submitter",
			Message: fmt.Sprintf("flag %s submitted successfully", payload.FlagID),
			FlagID:  payload.FlagID,
			Fields:  map[string]any{"response": bodyStr},
			RunID:   "",
		})
		return nil
	}

	message := bodyStr
	if message == "" {
		message = resp.Status
	}
	retryAt := time.Now().Add(s.cfg.FlagSubmitter.RetryBackoff)
	submittedAt := s.recordAttempt(ctx, payload.FlagID, resp.StatusCode, message, retryAt)
	flag, err := s.repo.GetFlag(ctx, payload.FlagID)
	if err == nil {
		flag.Status = domain.FlagStatusRetry
		flag.LastSubmittedAt = submittedAt
		_ = s.repo.UpdateFlag(ctx, &flag)
	}
	_ = s.repo.AppendLog(ctx, domain.LogEntry{
		Level:   "warn",
		Source:  "flag-submitter",
		Message: fmt.Sprintf("flag %s submission failed", payload.FlagID),
		FlagID:  payload.FlagID,
		Fields:  map[string]any{"status": resp.StatusCode, "response": bodyStr},
	})
	return fmt.Errorf("submit status %d", resp.StatusCode)
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
