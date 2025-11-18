package controlpanel

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/events"
	"awdinfra/internal/jobs"
	"awdinfra/internal/queue"
	"awdinfra/internal/scheduler"
	"awdinfra/internal/storage"
	"awdinfra/internal/submitter"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

// Server exposes REST endpoints and orchestrates runner coordination.
type Server struct {
	cfg        config.Config
	logger     *slog.Logger
	repository storage.Repository
	broker     queue.Broker
	httpServer *http.Server
	publisher  redis.UniversalClient
}

var errFlagCancelNotAllowed = errors.New("flag cannot be canceled")

// New returns a configured control panel server.
func New(cfg config.Config, logger *slog.Logger, repository storage.Repository, broker queue.Broker) *Server {
	mux := http.NewServeMux()
	protectedMux := authMiddleware(mux, cfg.ControlPanel.AdminToken, cfg.RunnerSecret, cfg.SubmitterSecret)
	server := &Server{
		cfg:        cfg,
		logger:     logger,
		repository: repository,
		broker:     broker,
		httpServer: &http.Server{
			Addr:              cfg.ControlPanel.BindAddr,
			ReadHeaderTimeout: 5 * time.Second,
			Handler:           protectedMux,
		},
	}
	server.publisher = newRedisPublisher(cfg, logger)

	server.registerRoutes(mux)
	return server
}

// Run starts the HTTP server and listens until context cancellation.
func (s *Server) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		s.logger.Info("control panel listening", slog.String("addr", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		s.logger.Info("shutting down control panel http server")
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	g.Go(func() error {
		return s.maintenanceLoop(ctx)
	})

	return g.Wait()
}

func (s *Server) maintenanceLoop(ctx context.Context) error {
	sweepInterval := s.cfg.Runner.HeartbeatInterval
	if sweepInterval <= 0 {
		sweepInterval = 10 * time.Second
	}
	visibility := s.cfg.RunQueue.Visibility
	if visibility <= 0 {
		visibility = 30 * time.Second
	}
	reclaimInterval := visibility / 2
	if reclaimInterval <= 0 {
		reclaimInterval = 5 * time.Second
	}

	sweepTicker := time.NewTicker(sweepInterval)
	defer sweepTicker.Stop()
	reclaimTicker := time.NewTicker(reclaimInterval)
	defer reclaimTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sweepTicker.C:
			if err := s.sweepStaleRunners(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("runner sweep failed", slog.Any("err", err))
			}
			if err := s.sweepStaleSubmitters(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("submitter sweep failed", slog.Any("err", err))
			}
			if err := s.expireTimedOutRuns(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("expire timed out runs failed", slog.Any("err", err))
			}
		case <-reclaimTicker.C:
			runners, err := s.reclaimStalledRuns(ctx)
			if err != nil && ctx.Err() == nil {
				s.logger.Warn("reclaim stalled runs failed", slog.Any("err", err))
				continue
			}
			s.trimRunQueues(ctx, runners)
		}
	}
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v1/overview", s.handleOverview)
	mux.HandleFunc("/api/v1/overview/matrix", s.handleOverviewMatrix)
	mux.HandleFunc("/api/v1/challenges", s.handleChallenges)
	mux.HandleFunc("/api/v1/challenges/", s.handleChallengeDetail)
	mux.HandleFunc("/api/v1/targets", s.handleTargets)
	mux.HandleFunc("/api/v1/targets/import", s.handleImportTargets)
	mux.HandleFunc("/api/v1/targets/", s.handleTargetDetail)
	mux.HandleFunc("/api/v1/exploits", s.handleExploits)
	mux.HandleFunc("/api/v1/exploits/", s.handleExploitActions)
	mux.HandleFunc("/api/v1/flags", s.handleFlags)
	mux.HandleFunc("/api/v1/flags/cancel", s.handleFlagBulkCancel)
	mux.HandleFunc("/api/v1/flags/retry", s.handleFlagBulkRetry)
	mux.HandleFunc("/api/v1/flags/", s.handleFlagDetail)
	mux.HandleFunc("/api/v1/runs", s.handleRuns)
	mux.HandleFunc("/api/v1/runs/cancel", s.handleRunBulkCancel)
	mux.HandleFunc("/api/v1/runs/", s.handleRunDetail)
	mux.HandleFunc("/api/v1/logs", s.handleLogs)
	mux.HandleFunc("/api/v1/runners/heartbeat", s.handleRunnerHeartbeat)
	mux.HandleFunc("/api/v1/runners", s.handleRunners)
	mux.HandleFunc("/api/v1/submitters/register", s.handleSubmitterRegister)
	mux.HandleFunc("/api/v1/submitters/heartbeat", s.handleSubmitterHeartbeat)
	mux.HandleFunc("/api/v1/submitters", s.handleSubmitters)
	mux.HandleFunc("/api/v1/submitters/", s.handleSubmitterDetail)
	mux.HandleFunc("/api/v1/scheduler/settings", s.handleSchedulerSettings)
	mux.HandleFunc("/api/v1/scheduler/targets", s.handleSchedulerTargets)
	mux.HandleFunc("/api/v1/scheduler/targets/", s.handleSchedulerTargetDetail)
	mux.HandleFunc("/api/v1/submitter/package", s.handleSubmitterPackage)
	mux.HandleFunc("/api/v1/submitter/package/", s.handleSubmitterPackage)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC(),
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stats, err := s.repository.DashboardStats(r.Context())
	if err != nil {
		s.logger.Error("load overview stats failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	flagStatuses := make(map[string]int, len(stats.FlagStatusCount))
	for status, count := range stats.FlagStatusCount {
		flagStatuses[string(status)] = count
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"overview": map[string]any{
			"exploitCount": stats.ExploitCount,
			"targetCount":  stats.TargetCount,
			"pendingFlags": stats.PendingFlags,
			"runningRuns":  stats.RunningRuns,
			"flagStatuses": flagStatuses,
		},
	})
}

func (s *Server) handleOverviewMatrix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := r.URL.Query()
	end := time.Now().UTC()
	if raw := strings.TrimSpace(query.Get("end")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid end timestamp")
			return
		}
		end = parsed.UTC()
	}
	windowMinutes := 15
	if raw := strings.TrimSpace(query.Get("minutes")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			windowMinutes = value
		}
	}
	start := end.Add(-time.Duration(windowMinutes) * time.Minute)
	if raw := strings.TrimSpace(query.Get("start")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid start timestamp")
			return
		}
		start = parsed.UTC()
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "end must be after start")
		return
	}
	matrix, err := s.repository.OverviewMatrix(r.Context(), start, end)
	if err != nil {
		s.logger.Error("load overview matrix failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to load overview matrix")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matrix": matrix})
}

func (s *Server) handleChallenges(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		challenges, err := s.repository.Challenges(r.Context())
		if err != nil {
			s.logger.Error("list challenges failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list challenges")
			return
		}
		items := make([]map[string]any, 0, len(challenges))
		for _, challenge := range challenges {
			items = append(items, marshalChallenge(challenge))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var req struct {
			Name        string         `json:"name"`
			Category    string         `json:"category"`
			ServiceType string         `json:"serviceType"`
			Description string         `json:"description"`
			FlagRegex   *string        `json:"flagRegex"`
			Metadata    map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		flagRegex := s.defaultFlagRegex(req.FlagRegex)
		created, err := s.repository.CreateChallenge(r.Context(), domain.Challenge{
			Name:        req.Name,
			Category:    req.Category,
			ServiceType: req.ServiceType,
			Description: req.Description,
			FlagRegex:   flagRegex,
			Metadata:    req.Metadata,
		})
		if err != nil {
			s.logger.Error("create challenge failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to create challenge")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"challenge": marshalChallenge(created)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleChallengeDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/challenges/")
	if id == "" {
		writeError(w, http.StatusNotFound, "missing challenge id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		challenge, err := s.repository.GetChallenge(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "challenge not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"challenge": marshalChallenge(challenge)})
	case http.MethodPatch, http.MethodPut:
		var req struct {
			Name        string         `json:"name"`
			Category    string         `json:"category"`
			ServiceType string         `json:"serviceType"`
			Description string         `json:"description"`
			FlagRegex   *string        `json:"flagRegex"`
			Metadata    map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		flagRegex := ""
		if req.FlagRegex != nil {
			flagRegex = s.defaultFlagRegex(req.FlagRegex)
		}
		updated, err := s.repository.UpdateChallenge(r.Context(), domain.Challenge{
			ID:          id,
			Name:        req.Name,
			Category:    req.Category,
			ServiceType: req.ServiceType,
			Description: req.Description,
			FlagRegex:   flagRegex,
			Metadata:    req.Metadata,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "challenge not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"challenge": marshalChallenge(updated)})
	case http.MethodDelete:
		if err := s.repository.DeleteChallenge(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "challenge not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) defaultFlagRegex(input *string) string {
	if input != nil {
		return domain.ResolveFlagRegex(*input, s.cfg.Runner.DefaultFlagRegex)
	}
	return domain.ResolveFlagRegex(s.cfg.Runner.DefaultFlagRegex)
}

func (s *Server) handleImportTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ChallengeID string   `json:"challengeId"`
		BaseIP      string   `json:"baseIp"`
		Port        int      `json:"port"`
		TeamNames   []string `json:"teamNames"`
		Count       int      `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if req.Count <= 0 {
		writeError(w, http.StatusBadRequest, "count must be > 0")
		return
	}
	addresses, err := generateTargetAddresses(req.BaseIP, req.Port, req.Count)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	teamNames := req.TeamNames
	if len(teamNames) == 0 {
		teamNames = make([]string, req.Count)
		for i := range teamNames {
			teamNames[i] = fmt.Sprintf("Team %02d", i+1)
		}
	}
	targets := make([]domain.Target, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		name := teamNames[i%len(teamNames)]
		addr := addresses[i]
		targets = append(targets, domain.Target{
			ChallengeID:      req.ChallengeID,
			TeamName:         name,
			IP:               addr.IP,
			Port:             addr.Port,
			Status:           "active",
			ConcurrencyLimit: 1,
		})
	}
	if err := s.repository.BulkInsertTargets(r.Context(), targets); err != nil {
		s.logger.Error("bulk insert targets failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to import targets")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": len(targets)})
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		targets, err := s.repository.ListTargets(r.Context())
		if err != nil {
			s.logger.Error("list targets failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list targets")
			return
		}
		items := make([]map[string]any, 0, len(targets))
		for _, target := range targets {
			items = append(items, marshalTarget(target))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var req struct {
			ChallengeID      string `json:"challengeId"`
			TeamName         string `json:"teamName"`
			IP               string `json:"ip"`
			Port             int    `json:"port"`
			ConcurrencyLimit int    `json:"concurrencyLimit"`
			Status           string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if req.TeamName == "" || req.IP == "" || req.Port <= 0 {
			writeError(w, http.StatusBadRequest, "missing target fields")
			return
		}
		if req.ConcurrencyLimit <= 0 {
			req.ConcurrencyLimit = 1
		}
		if req.Status == "" {
			req.Status = "active"
		}
		created, err := s.repository.CreateTarget(r.Context(), domain.Target{
			ChallengeID:      req.ChallengeID,
			TeamName:         req.TeamName,
			IP:               req.IP,
			Port:             req.Port,
			Status:           req.Status,
			ConcurrencyLimit: req.ConcurrencyLimit,
		})
		if err != nil {
			s.logger.Error("create target failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to create target")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"target": marshalTarget(created)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleTargetDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/targets/")
	if path == "" {
		writeError(w, http.StatusNotFound, "missing target id")
		return
	}
	parts := strings.Split(path, "/")
	targetID := strings.TrimSpace(parts[0])
	if targetID == "" {
		writeError(w, http.StatusNotFound, "missing target id")
		return
	}
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		writeError(w, http.StatusNotFound, "unknown target action")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		var req struct {
			ConcurrencyLimit *int    `json:"concurrencyLimit"`
			Status           *string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if req.ConcurrencyLimit == nil && req.Status == nil {
			writeError(w, http.StatusBadRequest, "no fields to update")
			return
		}
		update := domain.Target{ID: targetID}
		if req.ConcurrencyLimit != nil {
			if *req.ConcurrencyLimit <= 0 {
				writeError(w, http.StatusBadRequest, "concurrencyLimit must be > 0")
				return
			}
			update.ConcurrencyLimit = *req.ConcurrencyLimit
		}
		if req.Status != nil {
			update.Status = strings.TrimSpace(*req.Status)
		}
		target, err := s.repository.UpdateTarget(r.Context(), update)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeError(w, http.StatusNotFound, "target not found")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to update target")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"target": marshalTarget(target)})
	case http.MethodDelete:
		if err := s.repository.DeleteTarget(r.Context(), targetID); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeError(w, http.StatusNotFound, "target not found")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to delete target")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleExploits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		exploits, err := s.repository.ListExploits(r.Context())
		if err != nil {
			s.logger.Error("list exploits failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list exploits")
			return
		}
		responses := make([]map[string]any, 0, len(exploits))
		for _, exploit := range exploits {
			responses = append(responses, marshalExploit(exploit))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": responses})
	case http.MethodPost:
		contentType := r.Header.Get("Content-Type")
		var req struct {
			ChallengeID    string   `json:"challengeId"`
			Name           string   `json:"name"`
			Description    string   `json:"description"`
			RunnerTags     []string `json:"runnerTags"`
			ScheduleWeight int      `json:"scheduleWeight"`
			TimeoutSeconds int      `json:"timeoutSeconds"`
			Entrypoint     string   `json:"entrypoint"`
			RunScript      string   `json:"runScript"`
			ArtifactBase64 string   `json:"artifactBase64"`
			ArtifactName   string   `json:"artifactName"`
		}
		var artifact []byte
		var artifactName string
		if strings.HasPrefix(contentType, "multipart/form-data") {
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				writeError(w, http.StatusBadRequest, "failed to parse form")
				return
			}
			req.ChallengeID = r.FormValue("challengeId")
			req.Name = r.FormValue("name")
			req.Description = r.FormValue("description")
			req.Entrypoint = r.FormValue("entrypoint")
			req.RunScript = r.FormValue("runScript")
			if weight := r.FormValue("scheduleWeight"); weight != "" {
				if v, err := strconv.Atoi(weight); err == nil {
					req.ScheduleWeight = v
				}
			}
			if timeout := r.FormValue("timeoutSeconds"); timeout != "" {
				if v, err := strconv.Atoi(timeout); err == nil {
					req.TimeoutSeconds = v
				}
			}
			tags := strings.TrimSpace(r.FormValue("runnerTags"))
			if tags != "" {
				req.RunnerTags = splitCSV(tags)
			}
			file, header, err := r.FormFile("artifact")
			if err == nil && header != nil {
				defer file.Close()
				artifact, err = io.ReadAll(file)
				if err != nil {
					writeError(w, http.StatusBadRequest, "failed to read artifact")
					return
				}
				artifactName = header.Filename
			} else if err != nil && !errors.Is(err, http.ErrMissingFile) {
				writeError(w, http.StatusBadRequest, "failed to read artifact")
				return
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid payload")
				return
			}
			if req.ArtifactBase64 != "" {
				data, err := base64.StdEncoding.DecodeString(req.ArtifactBase64)
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid artifact base64")
					return
				}
				artifact = data
				artifactName = req.ArtifactName
			}
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		metadata := map[string]any{"timeout_seconds": req.TimeoutSeconds}
		if req.Entrypoint != "" {
			metadata["entrypoint"] = req.Entrypoint
		}
		if req.ScheduleWeight == 0 {
			req.ScheduleWeight = 1
		}
		exploit := domain.Exploit{
			ChallengeID:    req.ChallengeID,
			Name:           req.Name,
			Description:    req.Description,
			Enabled:        false,
			RunnerTags:     req.RunnerTags,
			ScheduleWeight: req.ScheduleWeight,
			Metadata:       metadata,
		}
		created, err := s.repository.CreateExploit(r.Context(), exploit)
		if err != nil {
			s.logger.Error("create exploit failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to create exploit")
			return
		}
		if len(artifact) > 0 {
			if strings.TrimSpace(artifactName) == "" {
				writeError(w, http.StatusBadRequest, "artifact filename required")
				return
			}
			if err := s.repository.SaveExploitFile(r.Context(), created.ID, artifactName, artifact); err != nil {
				s.logger.Warn("store exploit file failed", slog.Any("err", err), slog.String("exploit_id", created.ID))
			}
		}
		if strings.TrimSpace(req.RunScript) != "" {
			if err := s.repository.SaveExploitRunScript(r.Context(), created.ID, req.RunScript); err != nil {
				s.logger.Warn("save run script failed", slog.Any("err", err), slog.String("exploit_id", created.ID))
			}
		} else {
			if _, err := s.repository.GetExploitRunScript(r.Context(), created.ID); err != nil {
				s.logger.Warn("ensure default run script failed", slog.Any("err", err), slog.String("exploit_id", created.ID))
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"exploit": marshalExploit(created)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleExploitActions(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/exploits/")
	parts := strings.Split(path, "/")
	if parts[0] == "" {
		writeError(w, http.StatusNotFound, "invalid exploit path")
		return
	}
	id := parts[0]
	rest := parts[1:]

	if len(rest) == 0 || (len(rest) == 1 && rest[0] == "") {
		switch r.Method {
		case http.MethodDelete:
			if err := s.repository.DeleteExploit(r.Context(), id); err != nil {
				if errors.Is(err, storage.ErrExploitNotFound) {
					writeError(w, http.StatusNotFound, "exploit not found")
				} else {
					writeError(w, http.StatusInternalServerError, "failed to delete exploit")
				}
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	action := parts[1]
	switch action {
	case "files":
		s.handleExploitFiles(w, r, id, rest[1:])
		return
	case "enable", "disable":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		enabled := action == "enable"
		if err := s.repository.SetExploitEnabled(r.Context(), id, enabled); err != nil {
			writeError(w, http.StatusNotFound, "exploit not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"exploit_id": id, "enabled": enabled})
	case "timeout":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			TimeoutSeconds int `json:"timeoutSeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if req.TimeoutSeconds <= 0 {
			writeError(w, http.StatusBadRequest, "timeoutSeconds must be > 0")
			return
		}
		if err := s.repository.SetExploitTimeout(r.Context(), id, time.Duration(req.TimeoutSeconds)*time.Second); err != nil {
			writeError(w, http.StatusNotFound, "exploit not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"exploit_id": id, "timeoutSeconds": req.TimeoutSeconds})
	case "run-script":
		switch r.Method {
		case http.MethodGet:
			script, err := s.repository.GetExploitRunScript(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusNotFound, "run script not found")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"exploit_id": id, "runScript": script})
		case http.MethodPost:
			var req struct {
				RunScript string `json:"runScript"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid payload")
				return
			}
			if strings.TrimSpace(req.RunScript) == "" {
				writeError(w, http.StatusBadRequest, "runScript cannot be empty")
				return
			}
			if err := s.repository.SaveExploitRunScript(r.Context(), id, req.RunScript); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to update run script")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"exploit_id": id, "runScript": req.RunScript})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	case "test-run":
		s.handleExploitTestRun(w, r, id)
		return
	case "revisions":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		artifactName, artifact, err := readExploitFilePayload(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.repository.SaveExploitFile(r.Context(), id, artifactName, artifact); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store file")
			return
		}
		files, err := s.repository.ListExploitFiles(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list files")
			return
		}
		var fileInfo storage.ExploitFile
		for _, f := range files {
			if f.Name == artifactName {
				fileInfo = f
				break
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"file": marshalExploitFile(fileInfo)})
	case "artifact":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.logger.Info("artifact download requested",
			slog.String("exploit_id", id),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()))
		data, err := s.repository.LatestExploitArtifact(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", id))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(data); err != nil {
			s.logger.Warn("stream artifact failed", slog.Any("err", err), slog.String("exploit_id", id))
		}
	default:
		writeError(w, http.StatusNotFound, "unknown exploit action")
	}
}

func (s *Server) handleExploitFiles(w http.ResponseWriter, r *http.Request, exploitID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			files, err := s.repository.ListExploitFiles(r.Context(), exploitID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list files")
				return
			}
			result := make([]map[string]any, 0, len(files))
			for _, file := range files {
				result = append(result, marshalExploitFile(file))
			}
			writeJSON(w, http.StatusOK, map[string]any{"files": result})
		case http.MethodPost:
			name, data, err := readExploitFilePayload(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := s.repository.SaveExploitFile(r.Context(), exploitID, name, data); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to store file")
				return
			}
			files, err := s.repository.ListExploitFiles(r.Context(), exploitID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list files")
				return
			}
			var fileInfo storage.ExploitFile
			for _, f := range files {
				if f.Name == name {
					fileInfo = f
					break
				}
			}
			writeJSON(w, http.StatusCreated, map[string]any{"file": marshalExploitFile(fileInfo)})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	fileName := strings.Join(rest, "/")
	switch r.Method {
	case http.MethodGet:
		data, err := s.repository.GetExploitFile(r.Context(), exploitID, fileName)
		if err != nil {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(fileName)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(data); err != nil {
			s.logger.Warn("stream exploit file failed", slog.Any("err", err), slog.String("exploit_id", exploitID), slog.String("file", fileName))
		}
	case http.MethodDelete:
		if err := s.repository.DeleteExploitFile(r.Context(), exploitID, fileName); err != nil {
			switch {
			case errors.Is(err, storage.ErrExploitNotFound):
				writeError(w, http.StatusNotFound, "exploit not found")
			case strings.Contains(err.Error(), "not found"):
				writeError(w, http.StatusNotFound, "file not found")
			case strings.Contains(err.Error(), "cannot be deleted"):
				writeError(w, http.StatusBadRequest, err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "failed to delete file")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleExploitTestRun(w http.ResponseWriter, r *http.Request, exploitID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		TargetID string `json:"targetId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	targetID := strings.TrimSpace(req.TargetID)
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "targetId is required")
		return
	}
	ctx := r.Context()
	exploit, err := s.repository.GetExploit(ctx, exploitID)
	if err != nil {
		writeError(w, http.StatusNotFound, "exploit not found")
		return
	}
	target, err := s.repository.GetTarget(ctx, targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	if strings.TrimSpace(target.ChallengeID) != strings.TrimSpace(exploit.ChallengeID) {
		writeError(w, http.StatusBadRequest, "target does not belong to exploit challenge")
		return
	}
	run, err := s.createRunForTarget(ctx, exploit, target)
	if err != nil {
		s.logger.Warn("test run enqueue failed",
			slog.Any("err", err),
			slog.String("exploit_id", exploitID),
			slog.String("target_id", targetID))
		writeError(w, http.StatusInternalServerError, "failed to enqueue test run")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"run": marshalRunSummary(run, exploit.Name, target.TeamName),
	})
}

func (s *Server) createRunForTarget(ctx context.Context, exploit domain.Exploit, target domain.Target) (domain.Run, error) {
	if s.broker == nil {
		return domain.Run{}, errors.New("run queue unavailable")
	}
	challenge, err := s.repository.GetChallenge(ctx, target.ChallengeID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load challenge %s: %w", target.ChallengeID, err)
	}
	artifact, err := s.repository.LatestExploitArtifact(ctx, exploit.ID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("package exploit %s: %w", exploit.ID, err)
	}
	flagRegex, flagFile, timeout := jobs.HintsFromMetadata(exploit.Metadata)
	flagRegex = domain.ResolveFlagRegex(flagRegex, challenge.FlagRegex, s.cfg.Runner.DefaultFlagRegex)
	run := domain.Run{
		ExploitID: exploit.ID,
		TargetID:  target.ID,
		Status:    domain.RunStatusPending,
		QueuedAt:  time.Now().UTC(),
	}
	if err := s.repository.CreateRun(ctx, &run); err != nil {
		return domain.Run{}, fmt.Errorf("create run: %w", err)
	}
	item := domain.RunWorkItem{
		RunID:     run.ID,
		Exploit:   exploit,
		Target:    target,
		Artifact:  artifact,
		FlagRegex: flagRegex,
		FlagFile:  flagFile,
		Timeout:   timeout,
	}
	if err := scheduler.EnqueueRun(ctx, s.cfg, s.broker, item); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func (s *Server) handleFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	flags, err := s.repository.ListFlags(ctx)
	if err != nil {
		s.logger.Error("list flags failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list flags")
		return
	}

	challengeNames := s.loadChallengeNames(ctx)
	targetNames := s.loadTargetNames(ctx)

	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter != "" {
		filtered := flags[:0]
		for _, flag := range flags {
			if strings.EqualFold(string(flag.Status), statusFilter) {
				filtered = append(filtered, flag)
			}
		}
		flags = filtered
	}

	challengeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("challengeName")))
	targetFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("targetName")))
	if challengeFilter != "" || targetFilter != "" {
		filtered := flags[:0]
		for _, flag := range flags {
			if challengeFilter != "" {
				name := strings.ToLower(challengeNames[flag.ChallengeID])
				if name == "" {
					name = strings.ToLower(flag.ChallengeID)
				}
				if !strings.Contains(name, challengeFilter) {
					continue
				}
			}
			if targetFilter != "" {
				label := strings.ToLower(targetNames[flag.TargetID])
				if label == "" {
					label = strings.ToLower(flag.TargetID)
				}
				if !strings.Contains(label, targetFilter) {
					continue
				}
			}
			filtered = append(filtered, flag)
		}
		flags = filtered
	}

	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			if value > 200 {
				value = 200
			}
			limit = value
		}
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	start := 0
	if cursor != "" {
		for idx, flag := range flags {
			if flag.ID == cursor {
				start = idx + 1
				break
			}
		}
		if start > len(flags) {
			start = len(flags)
		}
	}
	end := start + limit
	if end > len(flags) {
		end = len(flags)
	}
	page := flags[start:end]

	items := make([]map[string]any, 0, len(page))
	for _, flag := range page {
		items = append(items, map[string]any{
			"id":              flag.ID,
			"value":           flag.Value,
			"challengeId":     flag.ChallengeID,
			"challengeName":   challengeNames[flag.ChallengeID],
			"targetId":        flag.TargetID,
			"targetLabel":     targetNames[flag.TargetID],
			"status":          flag.Status,
			"submissionCount": flag.SubmissionCount,
			"runId":           flag.RunID,
			"createdAt":       flag.CreatedAt,
			"lastSubmittedAt": flag.LastSubmittedAt,
		})
	}

	var nextCursor any = nil
	if end < len(flags) && len(page) > 0 {
		nextCursor = page[len(page)-1].ID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"nextCursor": nextCursor,
	})
}

func (s *Server) handleFlagDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/flags/")
	if path == "" {
		writeError(w, http.StatusNotFound, "invalid flag path")
		return
	}
	parts := strings.Split(path, "/")
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeError(w, http.StatusNotFound, "invalid flag path")
		return
	}
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		flag, err := s.repository.GetFlag(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "flag not found")
			return
		}
		attempts, err := s.repository.ListFlagAttempts(r.Context(), id)
		if err != nil {
			s.logger.Warn("list attempts failed", slog.Any("err", err), slog.String("flag_id", id))
			attempts = nil
		}
		ctx := r.Context()
		challengeName := s.lookupChallengeName(ctx, flag.ChallengeID)
		targetLabel := s.lookupTargetLabel(ctx, flag.TargetID)
		writeJSON(w, http.StatusOK, map[string]any{"flag": marshalFlag(flag, attempts, challengeName, targetLabel)})
		return
	}
	action := parts[1]
	switch action {
	case "requeue":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		flag, err := s.repository.GetFlag(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "flag not found")
			return
		}
		flag.Status = domain.FlagStatusRetry
		if err := s.repository.UpdateFlag(r.Context(), &flag); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to requeue flag")
			return
		}
		attempt := domain.SubmissionAttempt{
			FlagID:      id,
			SubmitterID: "manual",
			StatusCode:  0,
			Error:       "manual requeue",
		}
		if err := s.repository.AddFlagAttempt(r.Context(), id, attempt); err != nil {
			s.logger.Warn("add manual attempt failed", slog.Any("err", err))
		}
		s.logger.Info("flag requeued", slog.String("flag_id", id))
		if s.broker != nil {
			payload := submitter.QueueMessage{
				FlagID:      flag.ID,
				FlagValue:   flag.Value,
				ChallengeID: flag.ChallengeID,
				TargetID:    flag.TargetID,
			}
			data, err := submitter.EncodeMessage(payload)
			if err != nil {
				s.logger.Warn("encode flag message failed", slog.Any("err", err))
			} else {
				if _, err := s.broker.Publish(r.Context(), s.cfg.FlagQueue.Name, data); err != nil {
					s.logger.Warn("enqueue flag failed", slog.Any("err", err))
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"flag_id": id, "status": flag.Status})
	case "logs":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		logs, err := s.repository.GetFlagLogs(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "flag not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
	case "cancel":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid payload")
				return
			}
		}
		flag, err := s.cancelFlag(r.Context(), id, req.Reason)
		if err != nil {
			if errors.Is(err, errFlagCancelNotAllowed) {
				writeError(w, http.StatusConflict, "flag cannot be canceled")
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "flag not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to cancel flag")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"flag_id": flag.ID, "status": flag.Status})
	case "submit":
		if len(parts) < 3 {
			writeError(w, http.StatusBadRequest, "missing submit action")
			return
		}
		switch strings.TrimSpace(parts[2]) {
		case "start":
			s.handleFlagSubmitStart(w, r, id)
		case "finish":
			s.handleFlagSubmitFinish(w, r, id)
		default:
			writeError(w, http.StatusNotFound, "unknown submit action")
		}
	default:
		writeError(w, http.StatusNotFound, "unknown flag action")
	}
}

func (s *Server) handleFlagSubmitStart(w http.ResponseWriter, r *http.Request, flagID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Secret      string `json:"secret"`
		SubmitterID string `json:"submitterId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.SubmitterSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(req.SubmitterID) == "" {
		writeError(w, http.StatusBadRequest, "submitterId is required")
		return
	}
	if _, err := s.repository.GetSubmitter(r.Context(), req.SubmitterID); err != nil {
		writeError(w, http.StatusUnauthorized, "submitter not registered")
		return
	}
	flag, err := s.repository.GetFlag(r.Context(), flagID)
	if err != nil {
		writeError(w, http.StatusNotFound, "flag not found")
		return
	}
	switch flag.Status {
	case domain.FlagStatusAccepted, domain.FlagStatusDuplicated, domain.FlagStatusFailed, domain.FlagStatusDropped:
		writeError(w, http.StatusConflict, "flag already finalized")
		return
	}
	if flag.Status != domain.FlagStatusSubmitting {
		flag.Status = domain.FlagStatusSubmitting
		if err := s.repository.UpdateFlag(r.Context(), &flag); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update flag")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flag": map[string]any{
			"id":          flag.ID,
			"value":       flag.Value,
			"challengeId": flag.ChallengeID,
			"targetId":    flag.TargetID,
			"runId":       flag.RunID,
			"status":      flag.Status,
		},
	})
}

func (s *Server) handleFlagSubmitFinish(w http.ResponseWriter, r *http.Request, flagID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Secret            string         `json:"secret"`
		SubmitterID       string         `json:"submitterId"`
		Status            string         `json:"status"`
		StatusCode        int            `json:"statusCode"`
		Message           string         `json:"message"`
		LatencyMs         float64        `json:"latencyMs"`
		RetryAfterSeconds int            `json:"retryAfterSeconds"`
		Payload           map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.SubmitterSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(req.SubmitterID) == "" {
		writeError(w, http.StatusBadRequest, "submitterId is required")
		return
	}
	if _, err := s.repository.GetSubmitter(r.Context(), req.SubmitterID); err != nil {
		writeError(w, http.StatusUnauthorized, "submitter not registered")
		return
	}
	flag, err := s.repository.GetFlag(r.Context(), flagID)
	if err != nil {
		writeError(w, http.StatusNotFound, "flag not found")
		return
	}
	now := time.Now().UTC()
	resultStatus := strings.ToLower(strings.TrimSpace(req.Status))
	retry := false
	var finalStatus domain.FlagStatus
	switch resultStatus {
	case string(submitter.SubmissionStatusAccepted):
		finalStatus = domain.FlagStatusAccepted
	case string(submitter.SubmissionStatusDuplicated):
		finalStatus = domain.FlagStatusDuplicated
	case string(submitter.SubmissionStatusFailed):
		finalStatus = domain.FlagStatusFailed
	default:
		finalStatus = domain.FlagStatusRetry
		retry = true
	}

	attempt := domain.SubmissionAttempt{
		FlagID:      flagID,
		SubmitterID: req.SubmitterID,
		StatusCode:  req.StatusCode,
		Error:       req.Message,
		SubmittedAt: now,
		Payload:     req.Payload,
	}
	if retry && req.RetryAfterSeconds > 0 {
		attempt.NextRetryAt = now.Add(time.Duration(req.RetryAfterSeconds) * time.Second)
	}
	if err := s.repository.AddFlagAttempt(r.Context(), flagID, attempt); err != nil {
		s.logger.Warn("store flag attempt failed", slog.Any("err", err), slog.String("flag_id", flagID))
	}

	flag.Status = finalStatus
	flag.LastSubmittedAt = now
	if err := s.repository.UpdateFlag(r.Context(), &flag); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update flag")
		return
	}

	level := "info"
	switch finalStatus {
	case domain.FlagStatusFailed, domain.FlagStatusDuplicated:
		level = "warn"
	case domain.FlagStatusRetry:
		level = "error"
	}
	logArgs := []any{
		slog.String("flag_id", flagID),
		slog.String("status", string(finalStatus)),
		slog.String("submitter_id", req.SubmitterID),
		slog.Int("status_code", req.StatusCode),
	}
	if req.Message != "" {
		logArgs = append(logArgs, slog.String("message", req.Message))
	}
	if req.LatencyMs > 0 {
		logArgs = append(logArgs, slog.Float64("latency_ms", req.LatencyMs))
	}
	if req.RetryAfterSeconds > 0 {
		logArgs = append(logArgs, slog.Int("retry_after_seconds", req.RetryAfterSeconds))
	}
	s.logWithLevel(level, fmt.Sprintf("flag %s submitted", flagID), logArgs...)

	writeJSON(w, http.StatusOK, map[string]any{
		"flag": map[string]any{
			"id":     flag.ID,
			"status": flag.Status,
		},
		"retry": retry,
	})
}

func (s *Server) handleFlagBulkCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		FlagIDs []string `json:"flagIds"`
		Reason  string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(req.FlagIDs) == 0 {
		writeError(w, http.StatusBadRequest, "flagIds is required")
		return
	}
	ctx := r.Context()
	results := make([]map[string]any, 0, len(req.FlagIDs))
	seen := make(map[string]struct{})
	for _, raw := range req.FlagIDs {
		flagID := strings.TrimSpace(raw)
		if flagID == "" {
			continue
		}
		if _, exists := seen[flagID]; exists {
			continue
		}
		seen[flagID] = struct{}{}

		flag, err := s.cancelFlag(ctx, flagID, req.Reason)
		if err != nil {
			switch {
			case errors.Is(err, sql.ErrNoRows):
				results = append(results, map[string]any{"flagId": flagID, "error": "flag not found"})
			case errors.Is(err, errFlagCancelNotAllowed):
				results = append(results, map[string]any{"flagId": flagID, "error": "flag cannot be canceled"})
			default:
				results = append(results, map[string]any{"flagId": flagID, "error": "failed to cancel flag"})
			}
			continue
		}
		results = append(results, map[string]any{
			"flagId": flag.ID,
			"status": flag.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			if value > 200 {
				value = 200
			}
			limit = value
		}
	}
	query := r.URL.Query()
	cursor := strings.TrimSpace(query.Get("cursor"))
	filter := domain.RunFilter{
		Status:      strings.TrimSpace(query.Get("status")),
		ExploitID:   strings.TrimSpace(query.Get("exploitId")),
		TargetID:    strings.TrimSpace(query.Get("targetId")),
		ChallengeID: strings.TrimSpace(query.Get("challengeId")),
		ExploitName: strings.TrimSpace(query.Get("exploitName")),
		TargetName:  strings.TrimSpace(query.Get("targetName")),
	}
	if raw := strings.TrimSpace(query.Get("start")); raw != "" {
		if value, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			filter.QueuedAfter = value
		}
	}
	if raw := strings.TrimSpace(query.Get("end")); raw != "" {
		if value, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			filter.QueuedBefore = value
		}
	}
	runs, nextCursor, err := s.repository.ListRuns(r.Context(), filter, limit, cursor)
	if err != nil {
		s.logger.Error("list runs failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	exploitNames := make(map[string]string)
	if exploits, err := s.repository.ListExploits(r.Context()); err != nil {
		s.logger.Warn("list exploits for runs failed", slog.Any("err", err))
	} else {
		for _, exploit := range exploits {
			exploitNames[exploit.ID] = exploit.Name
		}
	}
	targetNames := make(map[string]string)
	if targets, err := s.repository.ListTargets(r.Context()); err != nil {
		s.logger.Warn("list targets for runs failed", slog.Any("err", err))
	} else {
		for _, target := range targets {
			targetNames[target.ID] = target.TeamName
		}
	}
	responses := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		responses = append(responses, marshalRunSummary(run, exploitNames[run.ExploitID], targetNames[run.TargetID]))
	}
	var result = map[string]any{"items": responses}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	} else {
		result["nextCursor"] = nil
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runs/")
	parts := strings.Split(path, "/")
	if parts[0] == "" {
		writeError(w, http.StatusNotFound, "missing run id")
		return
	}
	runID := parts[0]
	rest := parts[1:]

	if len(rest) == 0 || rest[0] == "" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		run, err := s.repository.GetRun(r.Context(), runID)
		if err != nil {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		ctx := r.Context()
		exploitName := s.lookupExploitName(ctx, run.ExploitID)
		targetLabel := s.lookupTargetLabel(ctx, run.TargetID)
		writeJSON(w, http.StatusOK, map[string]any{
			"run": marshalRunDetail(s.populateRunOutput(ctx, run), exploitName, targetLabel),
		})
		return
	}

	action := rest[0]
	switch action {
	case "start":
		s.handleRunStart(w, r, runID)
	case "finish":
		s.handleRunFinish(w, r, runID)
	case "cancel":
		s.handleRunCancel(w, r, runID)
	default:
		writeError(w, http.StatusNotFound, "unknown run action")
	}
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Secret     string `json:"secret"`
		RunnerID   string `json:"runnerId"`
		InstanceID string `json:"instanceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.RunnerSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := r.Context()
	run, err := s.repository.GetRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == domain.RunStatusCanceled {
		writeError(w, http.StatusConflict, "run canceled")
		return
	}
	if run.Status != domain.RunStatusPending && run.Status != domain.RunStatusRunning {
		writeError(w, http.StatusConflict, "run already completed")
		return
	}
	now := time.Now().UTC()
	run.Status = domain.RunStatusRunning
	if id := strings.TrimSpace(req.RunnerID); id != "" {
		run.RunnerID = id
	}
	if instance := strings.TrimSpace(req.InstanceID); instance != "" {
		run.RunnerInstanceID = instance
	}
	if run.QueuedAt.IsZero() {
		run.QueuedAt = now
	}
	run.StartedAt = now
	if err := s.repository.UpdateRun(ctx, &run); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update run")
		return
	}
	attrs := []any{
		slog.String("run_id", runID),
	}
	if strings.TrimSpace(run.RunnerID) != "" {
		attrs = append(attrs, slog.String("runner_id", run.RunnerID))
	}
	s.logger.Info(fmt.Sprintf("run %s started", runID), attrs...)
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "status": run.Status})
}

func (s *Server) handleRunFinish(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Secret     string `json:"secret"`
		RunnerID   string `json:"runnerId"`
		InstanceID string `json:"instanceId"`
		Status     string `json:"status"`
		Error      string `json:"error"`
		FlagValue  string `json:"flagValue"`
		FlagSource string `json:"flagSource"`
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.RunnerSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	statusValue := strings.ToLower(strings.TrimSpace(req.Status))
	var status domain.RunStatus
	switch statusValue {
	case "succeeded", "success":
		status = domain.RunStatusSucceeded
	case "warning":
		status = domain.RunStatusWarning
	case "failed", "failure":
		status = domain.RunStatusFailed
	case "timeout":
		status = domain.RunStatusTimeout
	default:
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}

	ctx := r.Context()
	run, err := s.repository.GetRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == domain.RunStatusCanceled {
		writeError(w, http.StatusConflict, "run canceled")
		return
	}

	now := time.Now().UTC()
	run.Status = status
	if id := strings.TrimSpace(req.RunnerID); id != "" {
		run.RunnerID = id
	}
	if instance := strings.TrimSpace(req.InstanceID); instance != "" {
		run.RunnerInstanceID = instance
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.FinishedAt = now
	run.ErrorMessage = strings.TrimSpace(req.Error)

	var createdFlag *domain.Flag
	flagValue := strings.TrimSpace(req.FlagValue)
	flagSource := strings.TrimSpace(req.FlagSource)
	if flagValue != "" {
		target, err := s.repository.GetTarget(ctx, run.TargetID)
		if err != nil {
			s.logger.Warn("load target for flag failed", slog.Any("err", err), slog.String("run_id", runID), slog.String("target_id", run.TargetID))
		} else {
			flag := domain.Flag{
				Value:       flagValue,
				ChallengeID: target.ChallengeID,
				TargetID:    target.ID,
				RunID:       run.ID,
			}
			if err := s.repository.InsertFlag(ctx, &flag); err != nil {
				s.logger.Warn("store flag failed", slog.Any("err", err), slog.String("run_id", runID))
			} else {
				createdFlag = &flag
				run.FlagID = flag.ID
			}
		}
	}

	if flagValue != "" {
		run.FlagValue = flagValue
	}
	if flagSource != "" {
		run.FlagSource = flagSource
	}

	if strings.TrimSpace(req.Stdout) != "" {
		trimmed := s.truncateLogOutput(req.Stdout)
		s.logger.Debug(fmt.Sprintf("run %s stdout", runID),
			slog.String("run_id", runID),
			slog.String("output", trimmed))
		run.Stdout = trimmed
	}

	if strings.TrimSpace(req.Stderr) != "" {
		trimmed := s.truncateLogOutput(req.Stderr)
		s.logger.Debug(fmt.Sprintf("run %s stderr", runID),
			slog.String("run_id", runID),
			slog.String("output", trimmed))
		run.Stderr = trimmed
	}

	if err := s.repository.UpdateRun(ctx, &run); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update run")
		return
	}

	logLevel := "info"
	if status != domain.RunStatusSucceeded {
		logLevel = "warn"
	}
	finishArgs := []any{
		slog.String("run_id", runID),
		slog.String("status", string(status)),
	}
	if strings.TrimSpace(run.RunnerID) != "" {
		finishArgs = append(finishArgs, slog.String("runner_id", run.RunnerID))
	}
	if run.ErrorMessage != "" {
		finishArgs = append(finishArgs, slog.String("error", run.ErrorMessage))
	}
	if createdFlag != nil {
		finishArgs = append(finishArgs, slog.String("flag_id", createdFlag.ID))
		if flagSource != "" {
			finishArgs = append(finishArgs, slog.String("flag_source", flagSource))
		}
	}
	s.logWithLevel(logLevel, fmt.Sprintf("run %s finished", runID), finishArgs...)

	if createdFlag != nil {
		flagArgs := []any{
			slog.String("run_id", runID),
			slog.String("flag_id", createdFlag.ID),
			slog.String("flag", flagValue),
		}
		if flagSource != "" {
			flagArgs = append(flagArgs, slog.String("source", flagSource))
		}
		s.logger.Info(fmt.Sprintf("run %s captured flag", runID), flagArgs...)
	}

	if createdFlag != nil {
		s.enqueueFlag(ctx, *createdFlag)
	}

	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "status": run.Status})
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
	}

	ctx := r.Context()
	run, err := s.repository.GetRun(ctx, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	switch run.Status {
	case domain.RunStatusPending, domain.RunStatusRunning:
	default:
		writeError(w, http.StatusConflict, "run cannot be canceled")
		return
	}

	run, err = s.cancelRunInternal(ctx, run, req.Reason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update run")
		return
	}

	exploitName := s.lookupExploitName(ctx, run.ExploitID)
	targetLabel := s.lookupTargetLabel(ctx, run.TargetID)
	run = s.populateRunOutput(ctx, run)
	writeJSON(w, http.StatusOK, map[string]any{
		"run": marshalRunDetail(run, exploitName, targetLabel),
	})
}

func (s *Server) handleRunBulkCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		RunIDs []string `json:"runIds"`
		Reason string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(req.RunIDs) == 0 {
		writeError(w, http.StatusBadRequest, "runIds is required")
		return
	}

	ctx := r.Context()
	results := make([]map[string]any, 0, len(req.RunIDs))
	seen := make(map[string]struct{})
	for _, rawID := range req.RunIDs {
		runID := strings.TrimSpace(rawID)
		if runID == "" {
			continue
		}
		if _, exists := seen[runID]; exists {
			continue
		}
		seen[runID] = struct{}{}

		run, err := s.repository.GetRun(ctx, runID)
		if err != nil {
			results = append(results, map[string]any{
				"runId": runID,
				"error": "run not found",
			})
			continue
		}
		switch run.Status {
		case domain.RunStatusPending, domain.RunStatusRunning:
		default:
			results = append(results, map[string]any{
				"runId": runID,
				"error": "run cannot be canceled",
			})
			continue
		}
		updated, err := s.cancelRunInternal(ctx, run, req.Reason)
		if err != nil {
			s.logger.Error("bulk cancel run failed", slog.Any("err", err), slog.String("run_id", runID))
			results = append(results, map[string]any{
				"runId": runID,
				"error": "failed to cancel run",
			})
			continue
		}
		exploitName := s.lookupExploitName(ctx, updated.ExploitID)
		targetLabel := s.lookupTargetLabel(ctx, updated.TargetID)
		results = append(results, map[string]any{
			"run": marshalRunSummary(updated, exploitName, targetLabel),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) cancelRunInternal(ctx context.Context, run domain.Run, reason string) (domain.Run, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		if run.Status == domain.RunStatusPending {
			reason = "canceled before execution"
		} else {
			reason = "canceled during execution"
		}
	}

	now := time.Now().UTC()
	run.Status = domain.RunStatusCanceled
	if run.StartedAt.IsZero() {
		if !run.QueuedAt.IsZero() {
			run.StartedAt = run.QueuedAt
		} else {
			run.StartedAt = now
		}
	}
	run.FinishedAt = now
	run.ErrorMessage = reason

	if err := s.repository.UpdateRun(ctx, &run); err != nil {
		return run, err
	}

	warnArgs := []any{
		slog.String("run_id", run.ID),
		slog.String("status", string(run.Status)),
		slog.String("reason", reason),
	}
	if strings.TrimSpace(run.RunnerID) != "" {
		warnArgs = append(warnArgs, slog.String("runner_id", run.RunnerID))
	}
	s.logger.Warn(fmt.Sprintf("run %s canceled", run.ID), warnArgs...)
	return run, nil
}

func (s *Server) cancelFlag(ctx context.Context, flagID string, reason string) (domain.Flag, error) {
	flag, err := s.repository.GetFlag(ctx, flagID)
	if err != nil {
		return domain.Flag{}, err
	}
	switch flag.Status {
	case domain.FlagStatusAccepted, domain.FlagStatusDropped, domain.FlagStatusFailed, domain.FlagStatusDuplicated:
		return domain.Flag{}, errFlagCancelNotAllowed
	case domain.FlagStatusSubmitting:
		// allow cancel but requeue if needed
	}
	flag.Status = domain.FlagStatusDropped
	if err := s.repository.UpdateFlag(ctx, &flag); err != nil {
		return domain.Flag{}, err
	}
	warnArgs := []any{
		slog.String("flag_id", flag.ID),
		slog.String("status", string(flag.Status)),
	}
	if strings.TrimSpace(reason) != "" {
		warnArgs = append(warnArgs, slog.String("reason", reason))
	}
	s.logger.Warn(fmt.Sprintf("flag %s dropped", flag.ID), warnArgs...)
	return flag, nil
}

func (s *Server) enqueueFlag(ctx context.Context, flag domain.Flag) {
	if strings.TrimSpace(flag.ID) == "" || strings.TrimSpace(flag.Value) == "" {
		return
	}
	queued := false
	if s.broker != nil {
		payload := submitter.QueueMessage{
			FlagID:      flag.ID,
			FlagValue:   flag.Value,
			ChallengeID: flag.ChallengeID,
			TargetID:    flag.TargetID,
		}
		data, err := submitter.EncodeMessage(payload)
		if err != nil {
			s.logger.Warn("encode flag message failed", slog.Any("err", err), slog.String("flag_id", flag.ID))
		} else if _, err := s.broker.Publish(ctx, s.cfg.FlagQueue.Name, data); err != nil {
			s.logger.Warn("enqueue flag failed", slog.Any("err", err), slog.String("flag_id", flag.ID))
		} else {
			queued = true
		}
	}
	message := "flag recorded"
	if queued {
		message = "flag queued"
	}
	s.logger.Info(fmt.Sprintf("%s %s", message, flag.ID),
		slog.String("flag_id", flag.ID),
		slog.String("challenge_id", flag.ChallengeID),
		slog.String("target_id", flag.TargetID))
}

func (s *Server) emitRunnerStatusEvent(ctx context.Context, runner domain.Runner, reason string, forceDrain bool) {
	if strings.TrimSpace(runner.ID) == "" {
		return
	}
	evt := events.RunnerEvent{}
	evt.Type = events.RunnerEventTypeStatus
	evt.RunnerID = runner.ID
	evt.InstanceID = runner.InstanceID
	evt.Status = runner.Status
	evt.Reason = reason
	evt.ForceDrain = forceDrain
	evt.Timestamp = time.Now().UTC()
	s.publishRunnerEvent(ctx, evt)
}

func (s *Server) emitRunnerInstanceEvent(ctx context.Context, runner domain.Runner, reason string) {
	if strings.TrimSpace(runner.ID) == "" || strings.TrimSpace(runner.InstanceID) == "" {
		return
	}
	evt := events.RunnerEvent{}
	evt.Type = events.RunnerEventTypeInstance
	evt.RunnerID = runner.ID
	evt.InstanceID = runner.InstanceID
	evt.Status = runner.Status
	evt.Reason = reason
	evt.ForceDrain = true
	evt.Timestamp = time.Now().UTC()
	s.publishRunnerEvent(ctx, evt)
}

func (s *Server) publishRunnerEvent(ctx context.Context, evt events.RunnerEvent) {
	if s.broker == nil || strings.TrimSpace(s.cfg.RunnerEventStream) == "" {
		return
	}
	data, err := events.Encode(evt)
	if err != nil {
		s.logger.Warn("encode runner event failed", slog.Any("err", err))
		return
	}
	if _, err := s.broker.Publish(ctx, s.cfg.RunnerEventStream, data); err != nil && ctx.Err() == nil {
		s.logger.Warn("publish runner event failed",
			slog.Any("err", err),
			slog.String("runner_id", evt.RunnerID),
			slog.String("instance_id", evt.InstanceID),
			slog.String("type", evt.Type))
	}
}

func (s *Server) truncateLogOutput(value string) string {
	value = strings.TrimSpace(value)
	limit := s.cfg.ControlPanel.LogOutputLimit
	if limit <= 0 {
		limit = 4096
	}
	if value == "" {
		return value
	}
	data := []byte(value)
	if len(data) <= limit {
		return value
	}
	truncated := data[:limit]
	for len(truncated) > 0 && !utf8.Valid(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	if len(truncated) == 0 {
		return "…"
	}
	return string(truncated) + "…"
}

func (s *Server) logWithLevel(level string, message string, args ...any) {
	switch level {
	case "debug":
		s.logger.Debug(message, args...)
	case "warn":
		s.logger.Warn(message, args...)
	case "error":
		s.logger.Error(message, args...)
	default:
		s.logger.Info(message, args...)
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	filter := domain.LogFilter{
		Level:  r.URL.Query().Get("level"),
		Source: r.URL.Query().Get("source"),
		RunID:  r.URL.Query().Get("runId"),
		FlagID: r.URL.Query().Get("flagId"),
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if value, err := strconv.Atoi(limit); err == nil {
			filter.Limit = value
		}
	}
	logs, err := s.repository.ListLogs(r.Context(), filter)
	if err != nil {
		s.logger.Error("list logs failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list logs")
		return
	}
	items := make([]map[string]any, 0, len(logs))
	for _, entry := range logs {
		items = append(items, marshalLog(entry))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRunners(w http.ResponseWriter, r *http.Request) {
	if err := s.sweepStaleRunners(r.Context()); err != nil {
		s.logger.Warn("runner sweep failed", slog.Any("err", err))
	}
	switch r.Method {
	case http.MethodGet:
		runners, err := s.repository.ListRunners(r.Context())
		if err != nil {
			s.logger.Error("list runners failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list runners")
			return
		}
		items := make([]map[string]any, 0, len(runners))
		for _, runner := range runners {
			items = append(items, marshalRunner(runner))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if strings.TrimSpace(id) == "" {
			writeError(w, http.StatusBadRequest, "runner id required")
			return
		}
		runner, _ := s.repository.GetRunner(r.Context(), id)
		if err := s.repository.DeleteRunner(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "runner not found")
			return
		}
		s.logger.Info("runner removed", slog.String("runner_id", id))
		s.handleRunnerOutage(r.Context(), runner, fmt.Sprintf("runner %s deleted", id))
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSubmitters(w http.ResponseWriter, r *http.Request) {
	if err := s.sweepStaleSubmitters(r.Context()); err != nil {
		s.logger.Warn("submitter sweep failed", slog.Any("err", err))
	}
	switch r.Method {
	case http.MethodGet:
		submitters, err := s.repository.ListSubmitters(r.Context())
		if err != nil {
			s.logger.Error("list submitters failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list submitters")
			return
		}
		items := make([]map[string]any, 0, len(submitters))
		for _, sub := range submitters {
			items = append(items, marshalSubmitter(sub))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":   items,
			"summary": map[string]any{"pressure": aggregateSubmitterPressure(submitters)},
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSubmitterDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/submitters/")
	path = strings.TrimSpace(path)
	if path == "" {
		writeError(w, http.StatusNotFound, "missing submitter id")
		return
	}
	parts := strings.Split(path, "/")
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeError(w, http.StatusNotFound, "missing submitter id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sub, err := s.repository.GetSubmitter(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "submitter not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"submitter": marshalSubmitter(sub)})
	case http.MethodDelete:
		if err := s.repository.DeleteSubmitter(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "submitter not found")
			return
		}
		s.logger.Info("submitter removed", slog.String("submitter_id", id))
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSubmitterRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ID            string            `json:"id"`
		Type          string            `json:"type"`
		Hostname      string            `json:"hostname"`
		Secret        string            `json:"secret"`
		Concurrency   int               `json:"concurrency"`
		Labels        map[string]string `json:"labels"`
		Config        map[string]string `json:"config"`
		Status        string            `json:"status"`
		PluginVersion string            `json:"pluginVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.SubmitterSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 1
	}
	status := req.Status
	if strings.TrimSpace(status) == "" {
		status = "online"
	}
	now := time.Now().UTC()
	submitter := domain.Submitter{
		ID:            strings.TrimSpace(req.ID),
		Type:          strings.TrimSpace(req.Type),
		Hostname:      strings.TrimSpace(req.Hostname),
		Status:        status,
		Concurrency:   req.Concurrency,
		Labels:        req.Labels,
		Config:        req.Config,
		LastHeartbeat: now,
		RegisteredAt:  now,
		PluginVersion: strings.TrimSpace(req.PluginVersion),
	}
	if err := s.repository.UpsertSubmitter(r.Context(), submitter); err != nil {
		s.logger.Error("submitter register failed", slog.Any("err", err), slog.String("submitter_id", req.ID))
		writeError(w, http.StatusInternalServerError, "failed to register submitter")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"submitter": marshalSubmitter(submitter)})
}

func (s *Server) handleSubmitterHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ID                string            `json:"id"`
		Secret            string            `json:"secret"`
		Status            string            `json:"status"`
		Type              string            `json:"type"`
		Hostname          string            `json:"hostname"`
		Concurrency       int               `json:"concurrency"`
		InFlight          int               `json:"inFlight"`
		Pending           int               `json:"pending"`
		RecentThroughput  float64           `json:"recentThroughput"`
		AvgLatencyMs      float64           `json:"avgLatencyMs"`
		Pressure          float64           `json:"pressure"`
		WindowSeconds     float64           `json:"windowSeconds"`
		LatencySumSeconds float64           `json:"latencySumSeconds"`
		ProcessedCount    int               `json:"processedCount"`
		Labels            map[string]string `json:"labels"`
		Config            map[string]string `json:"config"`
		PluginVersion     string            `json:"pluginVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.SubmitterSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 1
	}
	now := time.Now().UTC()
	existing, err := s.repository.GetSubmitter(r.Context(), req.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load submitter")
		return
	}
	inFlight := req.InFlight
	if inFlight < 0 {
		inFlight = 0
	}
	pending := req.Pending
	if pending < 0 {
		pending = 0
	}
	submitter := domain.Submitter{
		ID:                strings.TrimSpace(req.ID),
		Type:              strings.TrimSpace(req.Type),
		Hostname:          strings.TrimSpace(req.Hostname),
		Status:            strings.TrimSpace(req.Status),
		Concurrency:       req.Concurrency,
		InFlight:          inFlight,
		Pending:           pending,
		RecentThroughput:  req.RecentThroughput,
		AverageLatencyMS:  req.AvgLatencyMs,
		Pressure:          req.Pressure,
		WindowSeconds:     req.WindowSeconds,
		LatencySumSeconds: req.LatencySumSeconds,
		ProcessedCount:    req.ProcessedCount,
		Labels:            req.Labels,
		Config:            req.Config,
		LastHeartbeat:     now,
		RegisteredAt:      existing.RegisteredAt,
		PluginVersion:     strings.TrimSpace(req.PluginVersion),
	}
	if submitter.RegisteredAt.IsZero() {
		submitter.RegisteredAt = now
	}
	if submitter.Status == "" {
		submitter.Status = "online"
	}
	if submitter.WindowSeconds <= 0 {
		submitter.WindowSeconds = 60
	}
	if err := s.repository.UpsertSubmitter(r.Context(), submitter); err != nil {
		s.logger.Error("submitter heartbeat failed", slog.Any("err", err), slog.String("submitter_id", req.ID))
		writeError(w, http.StatusInternalServerError, "failed to update submitter")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submitter": marshalSubmitter(submitter)})
}

func (s *Server) handleSubmitterPackage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/submitter/package")
	path = strings.Trim(path, "/")
	if path == "" || path == "files" {
		switch r.Method {
		case http.MethodGet:
			files, err := s.repository.ListSubmitterFiles(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list files")
				return
			}
			result := make([]map[string]any, 0, len(files))
			for _, file := range files {
				result = append(result, marshalExploitFile(file))
			}
			writeJSON(w, http.StatusOK, map[string]any{"files": result})
		case http.MethodPost:
			name, data, err := readExploitFilePayload(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := s.repository.SaveSubmitterFile(r.Context(), name, data); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to save file")
				return
			}
			files, err := s.repository.ListSubmitterFiles(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list files")
				return
			}
			var saved storage.ExploitFile
			for _, f := range files {
				if f.Name == filepath.Base(name) {
					saved = f
					break
				}
			}
			writeJSON(w, http.StatusCreated, map[string]any{"file": marshalExploitFile(saved)})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	parts := strings.Split(path, "/")
	switch parts[0] {
	case "files":
		if len(parts) < 2 {
			writeError(w, http.StatusBadRequest, "file name required")
			return
		}
		name, err := url.PathUnescape(strings.Join(parts[1:], "/"))
		if err != nil || strings.TrimSpace(name) == "" {
			writeError(w, http.StatusBadRequest, "invalid file name")
			return
		}
		switch r.Method {
		case http.MethodGet:
			data, err := s.repository.ReadSubmitterFile(r.Context(), name)
			if err != nil {
				writeError(w, http.StatusNotFound, "file not found")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
			if _, err := w.Write(data); err != nil {
				s.logger.Warn("stream submitter file failed", slog.Any("err", err), slog.String("file", name))
			}
		case http.MethodDelete:
			if err := s.repository.DeleteSubmitterFile(r.Context(), name); err != nil {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "script":
		switch r.Method {
		case http.MethodGet:
			script, err := s.repository.GetSubmitterScript(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load script")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"script": script})
		case http.MethodPost:
			var req struct {
				Script string `json:"script"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid payload")
				return
			}
			if strings.TrimSpace(req.Script) == "" {
				writeError(w, http.StatusBadRequest, "script cannot be empty")
				return
			}
			if err := s.repository.SaveSubmitterScript(r.Context(), req.Script); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to save script")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"script": req.Script})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "artifact":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		data, version, err := s.repository.SubmitterArtifact(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename=\"submitter-package.zip\"")
		w.Header().Set("X-Submitter-Version", version)
		if _, err := w.Write(data); err != nil {
			s.logger.Warn("stream submitter artifact failed", slog.Any("err", err))
		}
	case "sync":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, version, err := s.repository.SubmitterArtifact(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.broadcastSubmitterPackage(version)
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	case "version":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, version, err := s.repository.SubmitterArtifact(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": version})
	default:
		writeError(w, http.StatusNotFound, "unknown submitter package action")
	}
}

func (s *Server) broadcastSubmitterPackage(version string) {
	channel := strings.TrimSpace(s.cfg.FlagSubmitter.PackageChannel)
	if s.publisher == nil || channel == "" {
		return
	}
	payload := map[string]any{
		"type":      "submitter-package",
		"version":   strings.TrimSpace(version),
		"timestamp": time.Now().UTC(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("encode submitter package update failed", slog.Any("err", err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if err := s.publisher.Publish(ctx, channel, data).Err(); err != nil {
		s.logger.Warn("broadcast submitter package failed", slog.Any("err", err))
	}
}

func (s *Server) populateRunOutput(ctx context.Context, run domain.Run) domain.Run {
	if run.Stdout != "" || run.Stderr != "" || run.FlagValue != "" || run.FlagSource != "" {
		return run
	}
	if strings.TrimSpace(run.ID) == "" {
		return run
	}
	stdout, stderr, flagValue, flagSource := s.collectLegacyRunOutput(ctx, run.ID)
	if run.Stdout == "" {
		run.Stdout = stdout
	}
	if run.Stderr == "" {
		run.Stderr = stderr
	}
	if run.FlagValue == "" {
		run.FlagValue = flagValue
	}
	if run.FlagSource == "" {
		run.FlagSource = flagSource
	}
	return run
}

func (s *Server) collectLegacyRunOutput(ctx context.Context, runID string) (string, string, string, string) {
	filter := domain.LogFilter{
		RunID: runID,
	}
	logs, err := s.repository.ListLogs(ctx, filter)
	if err != nil {
		s.logger.Warn("collect run output failed", slog.Any("err", err), slog.String("run_id", runID))
		return "", "", "", ""
	}
	var stdout string
	var stderr string
	var flagValue string
	var flagSource string
	for _, entry := range logs {
		fields := entry.Fields
		if fields == nil {
			continue
		}
		message := strings.ToLower(entry.Message)
		if output, _ := fields["output"].(string); output != "" {
			switch message {
			case fmt.Sprintf("run %s stdout", runID):
				stdout = output
			case fmt.Sprintf("run %s stderr", runID):
				stderr = output
			default:
				if stdout == "" && strings.Contains(message, "stdout") {
					stdout = output
				}
				if stderr == "" && strings.Contains(message, "stderr") {
					stderr = output
				}
			}
			continue
		}
		if message == fmt.Sprintf("run %s flag", runID) {
			if flagValue == "" {
				if value, ok := fields["flag"].(string); ok {
					flagValue = value
				}
			}
			if flagSource == "" {
				if source, ok := fields["source"].(string); ok {
					flagSource = source
				}
			}
		}
	}
	return stdout, stderr, flagValue, flagSource
}

func (s *Server) loadTargetNames(ctx context.Context) map[string]string {
	result := make(map[string]string)
	targets, err := s.repository.ListTargets(ctx)
	if err != nil {
		s.logger.Warn("list targets failed", slog.Any("err", err))
		return result
	}
	for _, target := range targets {
		result[target.ID] = target.TeamName
	}
	return result
}

func (s *Server) loadChallengeNames(ctx context.Context) map[string]string {
	result := make(map[string]string)
	challenges, err := s.repository.Challenges(ctx)
	if err != nil {
		s.logger.Warn("list challenges failed", slog.Any("err", err))
		return result
	}
	for _, challenge := range challenges {
		result[challenge.ID] = challenge.Name
	}
	return result
}

func (s *Server) lookupTargetLabel(ctx context.Context, targetID string) string {
	if strings.TrimSpace(targetID) == "" {
		return ""
	}
	target, err := s.repository.GetTarget(ctx, targetID)
	if err != nil {
		return ""
	}
	return target.TeamName
}

func (s *Server) lookupExploitName(ctx context.Context, exploitID string) string {
	if strings.TrimSpace(exploitID) == "" {
		return ""
	}
	exploits, err := s.repository.ListExploits(ctx)
	if err != nil {
		s.logger.Warn("list exploits failed", slog.Any("err", err))
		return ""
	}
	for _, exploit := range exploits {
		if exploit.ID == exploitID {
			return exploit.Name
		}
	}
	return ""
}

func (s *Server) lookupChallengeName(ctx context.Context, challengeID string) string {
	if strings.TrimSpace(challengeID) == "" {
		return ""
	}
	challenge, err := s.repository.GetChallenge(ctx, challengeID)
	if err != nil {
		return ""
	}
	return challenge.Name
}

func (s *Server) handleRunnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		runnerReportPayload
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(req.Secret) == "" || req.Secret != s.cfg.RunnerSecret {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		writeError(w, http.StatusBadRequest, "instanceId is required")
		return
	}

	ctx := r.Context()
	existing, err := s.repository.GetRunner(ctx, req.ID)
	notFound := false
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			notFound = true
		} else {
			s.logger.Error("load runner for heartbeat failed", slog.Any("err", err), slog.String("runner_id", req.ID))
			writeError(w, http.StatusInternalServerError, "failed to update runner")
			return
		}
	}

	now := time.Now().UTC()
	if notFound || strings.TrimSpace(existing.ID) == "" {
		runner := s.runnerFromReport(req.runnerReportPayload, now, clientIP(r))
		if err := s.repository.UpsertRunner(ctx, runner); err != nil {
			s.logger.Error("runner heartbeat failed", slog.Any("err", err), slog.String("runner_id", req.ID))
			writeError(w, http.StatusInternalServerError, "failed to update runner")
			return
		}
		s.emitRunnerStatusEvent(ctx, runner, "registered", false)
		s.emitRunnerInstanceEvent(ctx, runner, "registered")
		writeJSON(w, http.StatusOK, map[string]any{"runner": marshalRunner(runner)})
		if err := s.sweepStaleRunners(ctx); err != nil {
			s.logger.Warn("runner sweep failed", slog.Any("err", err))
		}
		return
	}

	requiresReset := needsInstanceReset(existing)
	previousInstance := strings.TrimSpace(existing.InstanceID)
	if requiresReset && previousInstance != "" && previousInstance == instanceID {
		if strings.ToLower(strings.TrimSpace(existing.Status)) != "restoring" {
			existing.Status = "restoring"
			existing.LastHeartbeat = now
			existing.LastIP = clientIP(r)
			if err := s.repository.UpsertRunner(ctx, existing); err != nil {
				s.logger.Warn("failed to mark runner restoring", slog.Any("err", err), slog.String("runner_id", existing.ID))
			}
		}
		s.writeInstanceResetResponse(w, existing)
		return
	}

	runner := existing
	oldInstance := strings.TrimSpace(existing.InstanceID)
	instanceChanged := oldInstance != "" && oldInstance != instanceID
	runner.InstanceID = instanceID
	if strings.TrimSpace(req.Type) != "" {
		runner.Type = strings.TrimSpace(req.Type)
	} else if runner.Type == "" {
		runner.Type = "default"
	}
	if strings.TrimSpace(req.Hostname) != "" {
		runner.Hostname = strings.TrimSpace(req.Hostname)
	} else if runner.Hostname == "" {
		runner.Hostname = req.ID
	}
	if len(req.Tags) > 0 {
		runner.Tags = normalizeTags(req.Tags)
	}
	if req.Labels != nil {
		runner.Labels = ensureLabels(req.Labels)
	} else if runner.Labels == nil {
		runner.Labels = map[string]string{}
	}
	if req.Config != nil {
		runner.Config = req.Config
	} else if runner.Config == nil {
		runner.Config = map[string]string{}
	}
	if req.MaxParallel > 0 {
		runner.MaxParallel = req.MaxParallel
	} else if runner.MaxParallel <= 0 {
		runner.MaxParallel = 1
	}
	if req.ActiveJobs >= 0 {
		runner.ActiveJobs = req.ActiveJobs
	}
	runner.Status = normalizeRunnerStatus(req.Status)
	if runner.RegisteredAt.IsZero() {
		runner.RegisteredAt = now
	}
	runner.LastHeartbeat = now
	runner.LastIP = clientIP(r)

	statusChanged := strings.ToLower(strings.TrimSpace(existing.Status)) != strings.ToLower(strings.TrimSpace(runner.Status))
	if err := s.repository.UpsertRunner(ctx, runner); err != nil {
		s.logger.Error("runner heartbeat failed", slog.Any("err", err), slog.String("runner_id", req.ID))
		writeError(w, http.StatusInternalServerError, "failed to update runner")
		return
	}
	if statusChanged {
		force := strings.EqualFold(runner.Status, "offline") || strings.EqualFold(runner.Status, "restoring")
		s.emitRunnerStatusEvent(ctx, runner, "heartbeat", force)
	}
	if instanceChanged {
		s.emitRunnerInstanceEvent(ctx, runner, "instance-changed")
	}
	writeJSON(w, http.StatusOK, map[string]any{"runner": marshalRunner(runner)})
	if err := s.sweepStaleRunners(ctx); err != nil {
		s.logger.Warn("runner sweep failed", slog.Any("err", err))
	}
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func maxInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func ensureLabels(src map[string]string) map[string]string {
	if src == nil {
		return map[string]string{}
	}
	return copyStringMap(src)
}

func ensureConfig(src map[string]string) map[string]string {
	if src == nil {
		return map[string]string{}
	}
	return copyStringMap(src)
}

func copyStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func normalizeRunnerStatus(value string) string {
	status := strings.ToLower(strings.TrimSpace(value))
	if status == "" {
		return "online"
	}
	return status
}

func needsInstanceReset(runner domain.Runner) bool {
	status := strings.ToLower(strings.TrimSpace(runner.Status))
	return status == "offline" || status == "restoring"
}

func (s *Server) writeInstanceResetResponse(w http.ResponseWriter, runner domain.Runner) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":   "instance_reset_required",
		"message": "runner instance expired; rotate instanceId and re-register",
		"runner":  marshalRunner(runner),
	})
}

type runnerReportPayload struct {
	ID          string            `json:"id"`
	InstanceID  string            `json:"instanceId"`
	Type        string            `json:"type"`
	Hostname    string            `json:"hostname"`
	Tags        []string          `json:"tags"`
	Labels      map[string]string `json:"labels"`
	MaxParallel int               `json:"maxParallel"`
	ActiveJobs  int               `json:"activeJobs"`
	Status      string            `json:"status"`
	Config      map[string]string `json:"config"`
}

func (s *Server) runnerFromReport(req runnerReportPayload, now time.Time, ip string) domain.Runner {
	return domain.Runner{
		ID:            strings.TrimSpace(req.ID),
		InstanceID:    strings.TrimSpace(req.InstanceID),
		Type:          defaultString(strings.TrimSpace(req.Type), "default"),
		Hostname:      defaultString(strings.TrimSpace(req.Hostname), req.ID),
		Tags:          normalizeTags(req.Tags),
		Labels:        ensureLabels(req.Labels),
		Config:        ensureConfig(req.Config),
		MaxParallel:   maxInt(req.MaxParallel, 1),
		ActiveJobs:    maxInt(req.ActiveJobs, 0),
		Status:        normalizeRunnerStatus(req.Status),
		RegisteredAt:  now,
		LastHeartbeat: now,
		LastIP:        ip,
	}
}

func (s *Server) handleSchedulerSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.repository.GetSchedulerSettings(r.Context())
		if err != nil {
			s.logger.Error("load scheduler settings failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to load scheduler settings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": marshalSchedulerSettings(settings)})
	case http.MethodPost, http.MethodPatch:
		var req struct {
			RoundIntervalMinutes   *int    `json:"roundIntervalMinutes"`
			RoundIntervalSeconds   *int    `json:"roundIntervalSeconds"`
			MaxDispatchBatch       *int    `json:"maxDispatchBatch"`
			Enabled                *bool   `json:"enabled"`
			Strategy               *string `json:"strategy"`
			ContinuousDelaySeconds *int    `json:"continuousDelaySeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		settings, err := s.repository.GetSchedulerSettings(r.Context())
		if err != nil {
			s.logger.Error("load scheduler settings failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to load scheduler settings")
			return
		}
		if req.RoundIntervalSeconds != nil {
			if *req.RoundIntervalSeconds <= 0 {
				writeError(w, http.StatusBadRequest, "roundIntervalSeconds must be > 0")
				return
			}
			settings.RoundInterval = time.Duration(*req.RoundIntervalSeconds) * time.Second
		} else if req.RoundIntervalMinutes != nil {
			if *req.RoundIntervalMinutes <= 0 {
				writeError(w, http.StatusBadRequest, "roundIntervalMinutes must be > 0")
				return
			}
			settings.RoundInterval = time.Duration(*req.RoundIntervalMinutes) * time.Minute
		}
		if req.MaxDispatchBatch != nil {
			if *req.MaxDispatchBatch <= 0 {
				writeError(w, http.StatusBadRequest, "maxDispatchBatch must be > 0")
				return
			}
			settings.MaxDispatchBatch = *req.MaxDispatchBatch
		}
		if req.Enabled != nil {
			settings.Enabled = *req.Enabled
		}
		if req.Strategy != nil {
			strategy := strings.ToLower(strings.TrimSpace(*req.Strategy))
			switch domain.SchedulerStrategy(strategy) {
			case domain.SchedulerStrategyInterval, domain.SchedulerStrategyContinuous:
				settings.Strategy = domain.SchedulerStrategy(strategy)
			default:
				writeError(w, http.StatusBadRequest, "invalid scheduler strategy")
				return
			}
		}
		if req.ContinuousDelaySeconds != nil {
			if *req.ContinuousDelaySeconds < 0 {
				writeError(w, http.StatusBadRequest, "continuousDelaySeconds must be >= 0")
				return
			}
			settings.ContinuousDelay = time.Duration(*req.ContinuousDelaySeconds) * time.Second
		}
		updated, err := s.repository.SaveSchedulerSettings(r.Context(), settings)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update scheduler settings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": marshalSchedulerSettings(updated)})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSchedulerTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.buildSchedulerResponse(r.Context(), nil)
	if err != nil {
		s.logger.Error("scheduler overview failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to load scheduler overview")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleSchedulerTargetDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/scheduler/targets/")
	if path == "" {
		writeError(w, http.StatusNotFound, "missing target id")
		return
	}
	parts := strings.Split(path, "/")
	targetID := parts[0]
	rest := parts[1:]

	if len(rest) == 0 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		filter := map[string]struct{}{targetID: {}}
		items, err := s.buildSchedulerResponse(r.Context(), filter)
		if err != nil {
			s.logger.Error("scheduler target detail failed", slog.Any("err", err), slog.String("target_id", targetID))
			writeError(w, http.StatusInternalServerError, "failed to load target schedule")
			return
		}
		if len(items) == 0 {
			writeError(w, http.StatusNotFound, "target not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"target": items[0]})
		return
	}

	action := rest[0]
	if action != "exploits" {
		writeError(w, http.StatusNotFound, "unknown scheduler action")
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Statuses []struct {
			ExploitID string `json:"exploitId"`
			Enabled   bool   `json:"enabled"`
		} `json:"statuses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(req.Statuses) == 0 {
		writeError(w, http.StatusBadRequest, "statuses required")
		return
	}

	ctx := r.Context()
	targets, err := s.repository.ListTargets(ctx)
	if err != nil {
		s.logger.Error("load targets failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to load targets")
		return
	}
	var target domain.Target
	found := false
	for _, t := range targets {
		if t.ID == targetID {
			target = t
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}

	exploits, err := s.repository.ListExploits(ctx)
	if err != nil {
		s.logger.Error("load exploits failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to load exploits")
		return
	}
	exploitIndex := make(map[string]domain.Exploit, len(exploits))
	for _, exploit := range exploits {
		exploitIndex[exploit.ID] = exploit
	}

	for _, status := range req.Statuses {
		exploit, ok := exploitIndex[status.ExploitID]
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown exploit in payload")
			return
		}
		if target.ChallengeID != "" && exploit.ChallengeID != "" && exploit.ChallengeID != target.ChallengeID {
			writeError(w, http.StatusBadRequest, "exploit does not belong to target challenge")
			return
		}
		if status.Enabled == exploit.Enabled {
			if err := s.repository.DeleteTargetExploitOverride(ctx, targetID, exploit.ID); err != nil {
				s.logger.Error("delete exploit override failed", slog.Any("err", err), slog.String("target_id", targetID), slog.String("exploit_id", exploit.ID))
				writeError(w, http.StatusInternalServerError, "failed to update exploit override")
				return
			}
			continue
		}
		if err := s.repository.UpsertTargetExploitOverride(ctx, domain.TargetExploitOverride{
			TargetID:  targetID,
			ExploitID: exploit.ID,
			Enabled:   status.Enabled,
		}); err != nil {
			s.logger.Error("upsert exploit override failed", slog.Any("err", err), slog.String("target_id", targetID), slog.String("exploit_id", exploit.ID))
			writeError(w, http.StatusInternalServerError, "failed to update exploit override")
			return
		}
	}

	filter := map[string]struct{}{targetID: {}}
	items, err := s.buildSchedulerResponse(ctx, filter)
	if err != nil {
		s.logger.Error("scheduler target detail failed", slog.Any("err", err), slog.String("target_id", targetID))
		writeError(w, http.StatusInternalServerError, "failed to load target schedule")
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": items[0]})
}

func (s *Server) buildSchedulerResponse(ctx context.Context, filter map[string]struct{}) ([]map[string]any, error) {
	targets, err := s.repository.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	exploits, err := s.repository.ListExploits(ctx)
	if err != nil {
		return nil, err
	}
	challenges, err := s.repository.Challenges(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := s.repository.ListTargetExploitOverrides(ctx)
	if err != nil {
		return nil, err
	}

	challengeIndex := make(map[string]domain.Challenge, len(challenges))
	for _, ch := range challenges {
		challengeIndex[ch.ID] = ch
	}

	exploitsByChallenge := make(map[string][]domain.Exploit)
	for _, exploit := range exploits {
		exploitsByChallenge[exploit.ChallengeID] = append(exploitsByChallenge[exploit.ChallengeID], exploit)
	}
	for challengeID := range exploitsByChallenge {
		sort.Slice(exploitsByChallenge[challengeID], func(i, j int) bool {
			return strings.ToLower(exploitsByChallenge[challengeID][i].Name) < strings.ToLower(exploitsByChallenge[challengeID][j].Name)
		})
	}

	overrideIndex := make(map[string]map[string]domain.TargetExploitOverride)
	for _, override := range overrides {
		if overrideIndex[override.TargetID] == nil {
			overrideIndex[override.TargetID] = make(map[string]domain.TargetExploitOverride)
		}
		overrideIndex[override.TargetID][override.ExploitID] = override
	}

	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		if filter != nil {
			if _, ok := filter[target.ID]; !ok {
				continue
			}
		}
		exploitList := exploitsByChallenge[target.ChallengeID]
		item := marshalSchedulerTarget(target, challengeIndex[target.ChallengeID], exploitList, overrideIndex[target.ID])
		items = append(items, item)
	}
	return items, nil
}

func marshalSchedulerSettings(settings domain.SchedulerSettings) map[string]any {
	seconds := int(settings.RoundInterval / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	minutes := int(settings.RoundInterval / time.Minute)
	if minutes == 0 && seconds > 0 {
		minutes = 1
	}
	continuous := int(settings.ContinuousDelay / time.Second)
	if continuous < 0 {
		continuous = 0
	}
	return map[string]any{
		"roundIntervalSeconds":   seconds,
		"roundIntervalMinutes":   minutes,
		"maxDispatchBatch":       settings.MaxDispatchBatch,
		"continuousDelaySeconds": continuous,
		"strategy":               string(settings.Strategy),
		"updatedAt":              settings.UpdatedAt,
		"enabled":                settings.Enabled,
	}
}

func marshalSchedulerTarget(target domain.Target, challenge domain.Challenge, exploits []domain.Exploit, overrides map[string]domain.TargetExploitOverride) map[string]any {
	statuses := make([]map[string]any, 0, len(exploits))
	var indicators strings.Builder
	enabledCount := 0
	for _, exploit := range exploits {
		enabled := exploit.Enabled
		if overrides != nil {
			if override, ok := overrides[exploit.ID]; ok {
				enabled = override.Enabled
			}
		}
		statuses = append(statuses, map[string]any{
			"exploitId":      exploit.ID,
			"exploitName":    exploit.Name,
			"enabled":        enabled,
			"defaultEnabled": exploit.Enabled,
		})
		if enabled {
			indicators.WriteString("🟢")
			enabledCount++
		} else {
			indicators.WriteString("🔴")
		}
	}
	summary := map[string]any{
		"total":      len(statuses),
		"enabled":    enabledCount,
		"disabled":   len(statuses) - enabledCount,
		"indicators": indicators.String(),
	}
	result := map[string]any{
		"target":   marshalTarget(target),
		"exploits": statuses,
		"summary":  summary,
	}
	if challenge.ID != "" {
		result["challenge"] = map[string]any{
			"id":          challenge.ID,
			"name":        challenge.Name,
			"category":    challenge.Category,
			"serviceType": challenge.ServiceType,
		}
	}
	return result
}

func marshalExploit(exploit domain.Exploit) map[string]any {
	timeout := 0
	entrypoint := ""
	runScript := ""
	if exploit.Metadata != nil {
		if val, ok := exploit.Metadata["timeout_seconds"].(int); ok {
			timeout = val
		}
		if val, ok := exploit.Metadata["timeout_seconds"].(float64); ok {
			timeout = int(val)
		}
		if val, ok := exploit.Metadata["entrypoint"].(string); ok {
			entrypoint = val
		}
		if val, ok := exploit.Metadata["run_script"].(string); ok {
			runScript = val
		}
	}
	return map[string]any{
		"id":             exploit.ID,
		"challengeId":    exploit.ChallengeID,
		"name":           exploit.Name,
		"description":    exploit.Description,
		"enabled":        exploit.Enabled,
		"runnerTags":     exploit.RunnerTags,
		"scheduleWeight": exploit.ScheduleWeight,
		"timeoutSeconds": timeout,
		"metadata":       exploit.Metadata,
		"entrypoint":     entrypoint,
		"runScript":      runScript,
		"updatedAt":      exploit.UpdatedAt,
	}
}

func marshalExploitFile(file storage.ExploitFile) map[string]any {
	result := map[string]any{
		"name": file.Name,
		"size": file.Size,
		"md5":  file.MD5,
	}
	if !file.ModTime.IsZero() {
		result["modTime"] = file.ModTime
	}
	return result
}

func marshalTarget(target domain.Target) map[string]any {
	return map[string]any{
		"id":               target.ID,
		"challengeId":      target.ChallengeID,
		"teamName":         target.TeamName,
		"ip":               target.IP,
		"port":             target.Port,
		"status":           target.Status,
		"concurrencyLimit": target.ConcurrencyLimit,
		"labels":           target.Labels,
	}
}

func marshalChallenge(ch domain.Challenge) map[string]any {
	return map[string]any{
		"id":          ch.ID,
		"name":        ch.Name,
		"category":    ch.Category,
		"serviceType": ch.ServiceType,
		"description": ch.Description,
		"flagRegex":   ch.FlagRegex,
		"metadata":    ch.Metadata,
		"createdAt":   ch.CreatedAt,
		"updatedAt":   ch.UpdatedAt,
	}
}

func readExploitFilePayload(r *http.Request) (string, []byte, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", nil, fmt.Errorf("failed to parse form")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return "", nil, fmt.Errorf("file field required")
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return "", nil, fmt.Errorf("failed to read file")
		}
		if len(data) == 0 {
			return "", nil, fmt.Errorf("file data required")
		}
		return header.Filename, data, nil
	}

	var req struct {
		ArtifactBase64 string `json:"artifactBase64"`
		ArtifactName   string `json:"artifactName"`
		FileBase64     string `json:"fileBase64"`
		FileName       string `json:"fileName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", nil, fmt.Errorf("invalid payload")
	}

	name := strings.TrimSpace(req.FileName)
	data64 := req.FileBase64
	if data64 == "" {
		data64 = req.ArtifactBase64
		name = strings.TrimSpace(req.ArtifactName)
	}
	if name == "" {
		return "", nil, fmt.Errorf("filename required")
	}
	if data64 == "" {
		return "", nil, fmt.Errorf("file data required")
	}
	data, err := base64.StdEncoding.DecodeString(data64)
	if err != nil {
		return "", nil, fmt.Errorf("invalid base64 data")
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("file data required")
	}
	return name, data, nil
}

func marshalAttempt(attempt domain.SubmissionAttempt) map[string]any {
	return map[string]any{
		"id":          attempt.ID,
		"flagId":      attempt.FlagID,
		"submitterId": attempt.SubmitterID,
		"statusCode":  attempt.StatusCode,
		"error":       attempt.Error,
		"submittedAt": attempt.SubmittedAt,
		"nextRetryAt": attempt.NextRetryAt,
	}
}

func marshalFlag(flag domain.Flag, attempts []domain.SubmissionAttempt, challengeName, targetLabel string) map[string]any {
	cleanAttempts := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		cleanAttempts = append(cleanAttempts, marshalAttempt(attempt))
	}
	return map[string]any{
		"id":              flag.ID,
		"value":           flag.Value,
		"challengeId":     flag.ChallengeID,
		"challengeName":   challengeName,
		"targetId":        flag.TargetID,
		"targetLabel":     targetLabel,
		"status":          flag.Status,
		"submissionCount": flag.SubmissionCount,
		"runId":           flag.RunID,
		"lastSubmittedAt": flag.LastSubmittedAt,
		"createdAt":       flag.CreatedAt,
		"attempts":        cleanAttempts,
	}
}

func marshalLog(entry domain.LogEntry) map[string]any {
	return map[string]any{
		"id":        entry.ID,
		"timestamp": entry.Timestamp,
		"level":     entry.Level,
		"source":    entry.Source,
		"message":   entry.Message,
		"runId":     entry.RunID,
		"flagId":    entry.FlagID,
		"fields":    entry.Fields,
	}
}

func marshalRunSummary(run domain.Run, exploitName, targetLabel string) map[string]any {
	return map[string]any{
		"id":               run.ID,
		"exploitId":        run.ExploitID,
		"exploitName":      exploitName,
		"targetId":         run.TargetID,
		"targetLabel":      targetLabel,
		"runnerId":         run.RunnerID,
		"runnerInstanceId": run.RunnerInstanceID,
		"status":           run.Status,
		"queuedAt":         run.QueuedAt,
		"startedAt":        run.StartedAt,
		"finishedAt":       run.FinishedAt,
		"errorMessage":     run.ErrorMessage,
	}
}

func marshalRunDetail(run domain.Run, exploitName, targetLabel string) map[string]any {
	data := marshalRunSummary(run, exploitName, targetLabel)
	data["flagId"] = run.FlagID
	data["stdout"] = run.Stdout
	data["stderr"] = run.Stderr
	data["flagValue"] = run.FlagValue
	data["flagSource"] = run.FlagSource
	return data
}

func marshalRunner(runner domain.Runner) map[string]any {
	return map[string]any{
		"id":            runner.ID,
		"instanceId":    runner.InstanceID,
		"type":          runner.Type,
		"hostname":      runner.Hostname,
		"lastIp":        runner.LastIP,
		"tags":          runner.Tags,
		"activeJobs":    runner.ActiveJobs,
		"labels":        runner.Labels,
		"config":        runner.Config,
		"maxParallel":   runner.MaxParallel,
		"lastHeartbeat": runner.LastHeartbeat,
		"status":        runner.Status,
		"registeredAt":  runner.RegisteredAt,
	}
}

func marshalSubmitter(sub domain.Submitter) map[string]any {
	return map[string]any{
		"id":                sub.ID,
		"type":              sub.Type,
		"hostname":          sub.Hostname,
		"status":            sub.Status,
		"concurrency":       sub.Concurrency,
		"inFlight":          sub.InFlight,
		"pending":           sub.Pending,
		"recentThroughput":  sub.RecentThroughput,
		"avgLatencyMs":      sub.AverageLatencyMS,
		"pressure":          sub.Pressure,
		"labels":            sub.Labels,
		"config":            sub.Config,
		"lastHeartbeat":     sub.LastHeartbeat,
		"registeredAt":      sub.RegisteredAt,
		"windowSeconds":     sub.WindowSeconds,
		"latencySumSeconds": sub.LatencySumSeconds,
		"processedCount":    sub.ProcessedCount,
		"pluginVersion":     sub.PluginVersion,
	}
}

func newRedisPublisher(cfg config.Config, logger *slog.Logger) redis.UniversalClient {
	channel := strings.TrimSpace(cfg.FlagSubmitter.PackageChannel)
	if channel == "" {
		return nil
	}
	opts := &redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
	if cfg.Redis.TLSEnabled {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Warn("plugin broadcaster disabled", slog.Any("err", err))
		return nil
	}
	return client
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		for _, part := range strings.Split(forwarded, ",") {
			ip := strings.TrimSpace(part)
			if ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

type targetAddress struct {
	IP   string
	Port int
}

func generateTargetAddresses(base string, port, count int) ([]targetAddress, error) {
	parts := strings.Split(base, ".")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid base ip")
	}
	var octets [4]int
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return nil, fmt.Errorf("invalid base ip")
		}
		octets[i] = value
	}
	baseVal := (uint32(octets[0]) << 24) | (uint32(octets[1]) << 16) | (uint32(octets[2]) << 8) | uint32(octets[3])
	addresses := make([]targetAddress, 0, count)
	for i := 0; i < count; i++ {
		val := baseVal + uint32(i)
		addr := targetAddress{
			IP:   fmt.Sprintf("%d.%d.%d.%d", (val>>24)&0xFF, (val>>16)&0xFF, (val>>8)&0xFF, val&0xFF),
			Port: port,
		}
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *Server) sweepStaleRunners(ctx context.Context) error {
	runners, err := s.repository.ListRunners(ctx)
	if err != nil {
		return err
	}
	interval := s.cfg.Runner.HeartbeatInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	offlineThreshold := interval * 2
	purgeThreshold := interval * 20
	now := time.Now().UTC()

	for _, runner := range runners {
		last := runner.LastHeartbeat
		if last.IsZero() {
			last = runner.RegisteredAt
		}
		if last.IsZero() {
			last = now
		}
		since := now.Sub(last)
		if purgeThreshold > 0 && since >= purgeThreshold {
			if err := s.repository.DeleteRunner(ctx, runner.ID); err != nil {
				s.logger.Warn("purge stale runner failed", slog.Any("err", err), slog.String("runner_id", runner.ID))
				continue
			}
			s.logger.Info("purged stale runner", slog.String("runner_id", runner.ID))
			s.handleRunnerOutage(ctx, runner, fmt.Sprintf("runner %s purged after missing heartbeats", runner.ID))
			continue
		}
		if offlineThreshold > 0 && since >= offlineThreshold && runner.Status != "offline" {
			runner.Status = "offline"
			if err := s.repository.UpsertRunner(ctx, runner); err != nil {
				s.logger.Warn("mark runner offline failed", slog.Any("err", err), slog.String("runner_id", runner.ID))
			} else {
				s.logger.Info("runner marked offline due to missed heartbeats", slog.String("runner_id", runner.ID))
				s.handleRunnerOutage(ctx, runner, fmt.Sprintf("runner %s offline after heartbeat timeout", runner.ID))
			}
		}
	}
	return nil
}

func (s *Server) sweepStaleSubmitters(ctx context.Context) error {
	submitters, err := s.repository.ListSubmitters(ctx)
	if err != nil {
		return err
	}
	interval := s.cfg.FlagSubmitter.HeartbeatInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	offlineThreshold := interval * 2
	purgeThreshold := interval * 20
	now := time.Now().UTC()

	for _, submitter := range submitters {
		last := submitter.LastHeartbeat
		if last.IsZero() {
			last = submitter.RegisteredAt
		}
		if last.IsZero() {
			last = now
		}
		since := now.Sub(last)
		if purgeThreshold > 0 && since >= purgeThreshold {
			if err := s.repository.DeleteSubmitter(ctx, submitter.ID); err != nil {
				s.logger.Warn("purge stale submitter failed", slog.Any("err", err), slog.String("submitter_id", submitter.ID))
				continue
			}
			s.logger.Info("purged stale submitter", slog.String("submitter_id", submitter.ID))
			continue
		}
		if offlineThreshold > 0 && since >= offlineThreshold && submitter.Status != "offline" {
			submitter.Status = "offline"
			if err := s.repository.UpsertSubmitter(ctx, submitter); err != nil {
				s.logger.Warn("mark submitter offline failed", slog.Any("err", err), slog.String("submitter_id", submitter.ID))
			} else {
				s.logger.Info("submitter marked offline due to missed heartbeats", slog.String("submitter_id", submitter.ID))
			}
		}
	}
	return nil
}

func (s *Server) expireTimedOutRuns(ctx context.Context) error {
	runs, err := s.repository.ListActiveRuns(ctx)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return nil
	}
	timeoutCache := make(map[string]time.Duration)
	now := time.Now().UTC()
	for _, run := range runs {
		timeout := s.runTimeoutForExploit(ctx, timeoutCache, run.ExploitID)
		if timeout <= 0 {
			continue
		}
		var anchor time.Time
		switch run.Status {
		case domain.RunStatusRunning:
			anchor = run.StartedAt
		case domain.RunStatusPending:
			anchor = run.QueuedAt
		default:
			continue
		}
		if anchor.IsZero() && !run.QueuedAt.IsZero() {
			anchor = run.QueuedAt
		}
		if anchor.IsZero() {
			continue
		}
		if now.Before(anchor.Add(timeout)) {
			continue
		}
		reason := fmt.Sprintf("run timeout after %s", timeout)
		updated, cancelErr := s.cancelRunInternal(ctx, run, reason)
		if cancelErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Warn("cancel timed out run failed",
				slog.Any("err", cancelErr),
				slog.String("run_id", run.ID))
			continue
		}
		if err := s.rescheduleRun(ctx, updated); err != nil && ctx.Err() == nil {
			s.logger.Warn("failed to enqueue replacement run",
				slog.Any("err", err),
				slog.String("run_id", run.ID))
		}
	}
	return nil
}

func (s *Server) runTimeoutForExploit(ctx context.Context, cache map[string]time.Duration, exploitID string) time.Duration {
	defaultTimeout := s.cfg.Runner.DefaultTimeout
	key := strings.TrimSpace(exploitID)
	if key == "" {
		return defaultTimeout
	}
	if value, ok := cache[key]; ok {
		return value
	}
	timeout := defaultTimeout
	exploit, err := s.repository.GetExploit(ctx, key)
	if err == nil {
		_, _, hintTimeout := jobs.HintsFromMetadata(exploit.Metadata)
		if hintTimeout > 0 {
			timeout = hintTimeout
		}
	} else if ctx.Err() != nil {
		return 0
	} else {
		s.logger.Debug("load exploit for timeout failed",
			slog.Any("err", err),
			slog.String("exploit_id", key))
	}
	cache[key] = timeout
	return timeout
}

func (s *Server) handleRunnerOutage(ctx context.Context, runner domain.Runner, reason string) {
	runnerID := strings.TrimSpace(runner.ID)
	if runnerID == "" {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "runner unavailable"
	}
	s.emitRunnerStatusEvent(ctx, runner, reason, true)
}

func (s *Server) reclaimStalledRuns(ctx context.Context) ([]domain.Runner, error) {
	visibility := s.cfg.RunQueue.Visibility
	if visibility <= 0 {
		return nil, nil
	}
	runners, err := s.repository.ListRunners(ctx)
	if err != nil {
		s.logger.Warn("list runners for reclaim failed", slog.Any("err", err))
		return nil, err
	}
	for _, runner := range runners {
		status := strings.ToLower(strings.TrimSpace(runner.Status))
		if status != "online" {
			continue
		}
		instanceID := strings.TrimSpace(runner.InstanceID)
		if instanceID == "" {
			continue
		}
		queueName := queue.RunnerQueueName(s.cfg.RunQueue.RunnerPrefix, runner.ID, runner.InstanceID)
		group := runnerGroupName(runner.Type)
		messages, err := s.broker.Reclaim(ctx, queueName, group, reclaimerConsumerName(group), visibility, 64)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			s.logger.Warn("reclaim stalled jobs failed",
				slog.Any("err", err),
				slog.String("group", group),
				slog.String("queue", queueName))
			continue
		}
		for _, message := range messages {
			if err := s.handleStaleQueueMessage(ctx, queueName, group, visibility, message); err != nil && ctx.Err() == nil {
				s.logger.Warn("handle stale job failed",
					slog.Any("err", err),
					slog.String("group", group),
					slog.String("queue", queueName),
					slog.String("message_id", message.ID))
			}
		}
	}
	return runners, nil
}

func (s *Server) handleStaleQueueMessage(ctx context.Context, queueName string, group string, visibility time.Duration, message queue.Message) error {
	payload, err := jobs.DecodeJobPayload(message.Body)
	if err != nil {
		if ackErr := s.broker.Ack(ctx, queueName, group, message.ID); ackErr != nil {
			return fmt.Errorf("ack malformed job: %w", ackErr)
		}
		return nil
	}
	run, err := s.repository.GetRun(ctx, payload.RunID)
	if err != nil {
		if ackErr := s.broker.Ack(ctx, queueName, group, message.ID); ackErr != nil {
			return ackErr
		}
		return nil
	}
	switch run.Status {
	case domain.RunStatusPending, domain.RunStatusRunning:
		timeout := payload.Timeout(s.cfg.Runner.DefaultTimeout)
		createdAt := payload.CreationTime()
		if createdAt.IsZero() {
			createdAt = run.QueuedAt
		}
		expired := false
		if !createdAt.IsZero() && timeout > 0 {
			if now := time.Now(); now.After(createdAt.Add(timeout)) {
				expired = true
			}
		}
		reason := fmt.Sprintf("visibility timeout after %s", visibility)
		if expired {
			reason = fmt.Sprintf("run timeout after %s", timeout)
		}
		updated, cancelErr := s.cancelRunInternal(ctx, run, reason)
		if cancelErr != nil {
			return cancelErr
		}
		if err := s.rescheduleRun(ctx, updated); err != nil {
			s.logger.Warn("failed to enqueue replacement run",
				slog.Any("err", err),
				slog.String("run_id", run.ID))
		}
	default:
		// no-op
	}
	if err := s.broker.Ack(ctx, queueName, group, message.ID); err != nil {
		return err
	}
	return nil
}

func (s *Server) trimRunQueues(ctx context.Context, runners []domain.Runner) {
	maxLen := s.cfg.RunQueue.TrimMaxLen
	trimMain := queue.TrimStrategy{MaxLen: maxLen}
	minID, err := s.broker.PendingMinID(ctx, s.cfg.RunQueue.Name, "dispatcher")
	if err != nil && ctx.Err() == nil {
		s.logger.Debug("pending min id lookup failed",
			slog.Any("err", err),
			slog.String("stream", s.cfg.RunQueue.Name))
	} else if minID != "" {
		trimMain.MinID = minID
		trimMain.MaxLen = 0
	}
	if err := s.broker.Trim(ctx, s.cfg.RunQueue.Name, trimMain); err != nil && ctx.Err() == nil {
		s.logger.Debug("trim main run stream failed",
			slog.Any("err", err),
			slog.String("stream", s.cfg.RunQueue.Name))
	}
	for _, runner := range runners {
		status := strings.ToLower(strings.TrimSpace(runner.Status))
		if status != "online" {
			continue
		}
		instanceID := strings.TrimSpace(runner.InstanceID)
		if instanceID == "" {
			continue
		}
		stream := queue.RunnerQueueName(s.cfg.RunQueue.RunnerPrefix, runner.ID, runner.InstanceID)
		strategy := queue.TrimStrategy{MaxLen: maxLen}
		group := runnerGroupName(runner.Type)
		minID, err := s.broker.PendingMinID(ctx, stream, group)
		if err == nil && minID != "" {
			strategy.MinID = minID
			strategy.MaxLen = 0
		} else if err != nil && ctx.Err() == nil {
			s.logger.Debug("pending min id lookup failed",
				slog.Any("err", err),
				slog.String("stream", stream))
		}
		if err := s.broker.Trim(ctx, stream, strategy); err != nil && ctx.Err() == nil {
			s.logger.Debug("trim runner stream failed",
				slog.Any("err", err),
				slog.String("stream", stream))
		}
	}
}

func (s *Server) rescheduleRun(ctx context.Context, previous domain.Run) error {
	exploitID := strings.TrimSpace(previous.ExploitID)
	targetID := strings.TrimSpace(previous.TargetID)
	if exploitID == "" || targetID == "" {
		return fmt.Errorf("run %s missing exploit/target for reschedule", previous.ID)
	}
	exploit, err := s.repository.GetExploit(ctx, exploitID)
	if err != nil {
		return fmt.Errorf("load exploit %s: %w", exploitID, err)
	}
	target, err := s.repository.GetTarget(ctx, targetID)
	if err != nil {
		return fmt.Errorf("load target %s: %w", targetID, err)
	}
	if _, err := s.createRunForTarget(ctx, exploit, target); err != nil {
		return fmt.Errorf("create replacement run: %w", err)
	}
	return nil
}

func runnerGroupName(runnerType string) string {
	value := strings.TrimSpace(runnerType)
	if value == "" {
		return "runner"
	}
	return fmt.Sprintf("runner-%s", value)
}

func reclaimerConsumerName(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return "reclaimer"
	}
	return fmt.Sprintf("%s-reclaimer", group)
}

func aggregateSubmitterPressure(submitters []domain.Submitter) float64 {
	var demand float64
	var capacity float64
	for _, sub := range submitters {
		window := sub.WindowSeconds
		if window <= 0 {
			window = 60
		}
		if sub.Concurrency <= 0 {
			continue
		}
		if sub.LatencySumSeconds > 0 {
			demand += sub.LatencySumSeconds
		} else {
			// fallback to avg latency estimate
			avgSeconds := sub.AverageLatencyMS / 1000
			if avgSeconds <= 0 {
				avgSeconds = 1
			}
			demand += avgSeconds * float64(sub.ProcessedCount)
		}
		capacity += float64(sub.Concurrency) * window
	}
	if capacity <= 0 {
		return 0
	}
	return demand / capacity
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}
func (s *Server) handleFlagBulkRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		FlagIDs []string `json:"flagIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if len(req.FlagIDs) == 0 {
		writeError(w, http.StatusBadRequest, "flagIds is required")
		return
	}
	ctx := r.Context()
	results := make([]map[string]any, 0, len(req.FlagIDs))
	seen := make(map[string]struct{})
	for _, raw := range req.FlagIDs {
		flagID := strings.TrimSpace(raw)
		if flagID == "" {
			continue
		}
		if _, exists := seen[flagID]; exists {
			continue
		}
		seen[flagID] = struct{}{}

		flag, err := s.repository.GetFlag(ctx, flagID)
		if err != nil {
			results = append(results, map[string]any{
				"flagId": flagID,
				"error":  "flag not found",
			})
			continue
		}
		flag.Status = domain.FlagStatusRetry
		if err := s.repository.UpdateFlag(ctx, &flag); err != nil {
			results = append(results, map[string]any{
				"flagId": flagID,
				"error":  "failed to update flag",
			})
			continue
		}
		s.enqueueFlag(ctx, flag)
		results = append(results, map[string]any{
			"flagId": flagID,
			"status": flag.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
