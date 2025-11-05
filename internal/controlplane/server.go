package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"awdinfra/internal/config"
	"awdinfra/internal/domain"
	"awdinfra/internal/queue"
	"awdinfra/internal/storage"
	"awdinfra/internal/submitter"
)

// Server exposes REST endpoints and orchestrates runner coordination.
type Server struct {
	cfg        config.Config
	logger     *slog.Logger
	repository storage.Repository
	broker     queue.Broker
	httpServer *http.Server
}

// New returns a configured control plane server.
func New(cfg config.Config, logger *slog.Logger, repository storage.Repository, broker queue.Broker) *Server {
	mux := http.NewServeMux()
	server := &Server{
		cfg:        cfg,
		logger:     logger,
		repository: repository,
		broker:     broker,
		httpServer: &http.Server{
			Addr:              cfg.ControlPlane.BindAddr,
			ReadHeaderTimeout: 5 * time.Second,
			Handler:           mux,
		},
	}

	server.registerRoutes(mux)
	return server
}

// Run starts the HTTP server and listens until context cancellation.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("control plane listening", slog.String("addr", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		s.logger.Info("shutting down control plane http server")
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v1/challenges", s.handleChallenges)
	mux.HandleFunc("/api/v1/challenges/", s.handleChallengeDetail)
	mux.HandleFunc("/api/v1/targets", s.handleTargets)
	mux.HandleFunc("/api/v1/targets/import", s.handleImportTargets)
	mux.HandleFunc("/api/v1/exploits", s.handleExploits)
	mux.HandleFunc("/api/v1/exploits/", s.handleExploitActions)
	mux.HandleFunc("/api/v1/flags", s.handleFlags)
	mux.HandleFunc("/api/v1/flags/", s.handleFlagDetail)
	mux.HandleFunc("/api/v1/runs", s.handleRuns)
	mux.HandleFunc("/api/v1/runs/", s.handleRunDetail)
	mux.HandleFunc("/api/v1/logs", s.handleLogs)
	mux.HandleFunc("/api/v1/runners", s.handleRunners)
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

func (s *Server) handleChallenges(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		challenges, err := s.repository.Challenges(r.Context())
		if err != nil {
			s.logger.Error("list challenges failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to list challenges")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": challenges})
	case http.MethodPost:
		var req struct {
			Name        string         `json:"name"`
			Category    string         `json:"category"`
			ServiceType string         `json:"serviceType"`
			Description string         `json:"description"`
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
		created, err := s.repository.CreateChallenge(r.Context(), domain.Challenge{
			Name:        req.Name,
			Category:    req.Category,
			ServiceType: req.ServiceType,
			Description: req.Description,
			Metadata:    req.Metadata,
		})
		if err != nil {
			s.logger.Error("create challenge failed", slog.Any("err", err))
			writeError(w, http.StatusInternalServerError, "failed to create challenge")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"challenge": created})
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
		writeJSON(w, http.StatusOK, map[string]any{"challenge": challenge})
	case http.MethodPatch, http.MethodPut:
		var req struct {
			Name        string         `json:"name"`
			Category    string         `json:"category"`
			ServiceType string         `json:"serviceType"`
			Description string         `json:"description"`
			Metadata    map[string]any `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		updated, err := s.repository.UpdateChallenge(r.Context(), domain.Challenge{
			ID:          id,
			Name:        req.Name,
			Category:    req.Category,
			ServiceType: req.ServiceType,
			Description: req.Description,
			Metadata:    req.Metadata,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "challenge not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"challenge": updated})
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
		}
		var artifact []byte
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
		if req.RunScript != "" {
			metadata["run_script"] = req.RunScript
		}
		if req.ScheduleWeight == 0 {
			req.ScheduleWeight = 1
		}
		exploit := domain.Exploit{
			ChallengeID:    req.ChallengeID,
			Name:           req.Name,
			Description:    req.Description,
			Enabled:        true,
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
			revID := fmt.Sprintf("rev-%d", time.Now().UnixNano())
			rev := domain.ExploitRevision{
				ID:         revID,
				ExploitID:  created.ID,
				Digest:     fmt.Sprintf("%x", sha256.Sum256(artifact)),
				StorageURI: fmt.Sprintf("memory://exploit/%s/%s", created.ID, revID),
			}
			if err := s.repository.AddExploitRevision(r.Context(), rev); err != nil {
				s.logger.Warn("store revision failed", slog.Any("err", err))
			} else if err := s.repository.SaveExploitArtifact(r.Context(), rev.ID, artifact); err != nil {
				s.logger.Warn("store artifact failed", slog.Any("err", err))
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
	if len(parts) < 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "invalid exploit path")
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
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
	case "revisions":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				writeError(w, http.StatusBadRequest, "failed to parse form")
				return
			}
		}
		revision := domain.ExploitRevision{ExploitID: id, Digest: fmt.Sprintf("upload-%d", time.Now().UnixNano())}
		if err := s.repository.AddExploitRevision(r.Context(), revision); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store revision")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"revision": revision})
	default:
		writeError(w, http.StatusNotFound, "unknown exploit action")
	}
}

func (s *Server) handleFlags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	flags, err := s.repository.ListFlags(r.Context())
	if err != nil {
		s.logger.Error("list flags failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list flags")
		return
	}
	responses := make([]map[string]any, 0, len(flags))
	for _, flag := range flags {
		attempts, _ := s.repository.ListFlagAttempts(r.Context(), flag.ID)
		responses = append(responses, map[string]any{
			"id":              flag.ID,
			"value":           flag.Value,
			"challengeId":     flag.ChallengeID,
			"targetId":        flag.TargetID,
			"status":          flag.Status,
			"submissionCount": flag.SubmissionCount,
			"attempts":        attempts,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": responses})
}

func (s *Server) handleFlagDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/flags/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "invalid flag path")
		return
	}
	id := parts[0]
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
		_ = s.repository.AppendLog(r.Context(), domain.LogEntry{
			Level:   "info",
			Source:  "control-plane",
			Message: fmt.Sprintf("flag %s requeued", id),
			FlagID:  id,
		})
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
	default:
		writeError(w, http.StatusNotFound, "unknown flag action")
	}
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	runs, err := s.repository.ListRuns(r.Context())
	if err != nil {
		s.logger.Error("list runs failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	responses := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		responses = append(responses, map[string]any{
			"id":         run.ID,
			"exploitId":  run.ExploitID,
			"targetId":   run.TargetID,
			"runnerId":   run.RunnerID,
			"status":     run.Status,
			"queuedAt":   run.QueuedAt,
			"startedAt":  run.StartedAt,
			"finishedAt": run.FinishedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": responses})
}

func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/runs/")
	if id == "" {
		writeError(w, http.StatusNotFound, "missing run id")
		return
	}
	run, err := s.repository.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run": map[string]any{
			"id":           run.ID,
			"exploitId":    run.ExploitID,
			"targetId":     run.TargetID,
			"runnerId":     run.RunnerID,
			"status":       run.Status,
			"queuedAt":     run.QueuedAt,
			"startedAt":    run.StartedAt,
			"finishedAt":   run.FinishedAt,
			"stdout":       "",
			"stderr":       "",
			"errorMessage": run.ErrorMessage,
		},
	})
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
	writeJSON(w, http.StatusOK, map[string]any{"items": logs})
}

func (s *Server) handleRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	runners, err := s.repository.ListRunners(r.Context())
	if err != nil {
		s.logger.Error("list runners failed", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "failed to list runners")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runners})
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
