package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config captures runtime configuration consumed by services.
type Config struct {
	AppName           string
	LogLevel          string
	Postgres          PostgresConfig
	Redis             RedisConfig
	ControlPanel      ControlPanelConfig
	Scheduler         SchedulerConfig
	Runner            RunnerConfig
	FlagSubmitter     FlagSubmitterConfig
	RunQueue          QueueConfig
	FlagQueue         QueueConfig
	Dispatcher        DispatcherConfig
	RunnerSecret      string
	SubmitterSecret   string
	AllowedRunnerTags []string
}

type PostgresConfig struct {
	DSN             string
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ArtifactDir     string
}

type RedisConfig struct {
	Addr       string
	Password   string
	DB         int
	TLSEnabled bool
}

type ControlPanelConfig struct {
	BindAddr        string
	MetricsAddr     string
	WebsocketOrigin string
	AdminToken      string
}

type SchedulerConfig struct {
	PollInterval     time.Duration
	MaxDispatchBatch int
}

type RunnerConfig struct {
	ID                    string
	Type                  string
	ControlPanelURL       string
	ControlPanelTimeout   time.Duration
	MaxParallelJobs       int
	LogFlushInterval      time.Duration
	HeartbeatInterval     time.Duration
	ChrootPath            string
	WorkspaceRoot         string
	FlagFileName          string
	DefaultFlagRegex      string
	DefaultTimeout        time.Duration
	Tags                  []string
	ArtifactCacheTTL      time.Duration
	ArtifactCacheDir      string
	ArtifactCacheMaxBytes int64
}

type FlagSubmitterConfig struct {
	GlobalRateLimit   int
	PerChallengeLimit int
	RetryBackoff      time.Duration
	MaxAttempts       int
	Endpoint          string
	RequestTimeout    time.Duration
	Concurrency       int
	ID                string
	ControlPanelURL   string
	HeartbeatInterval time.Duration
}

type QueueConfig struct {
	Name         string
	Visibility   time.Duration
	RetryDelay   time.Duration
	RunnerPrefix string
	TrimMaxLen   int64
}

type DispatcherConfig struct {
	ID              string
	MaxBatch        int
	BlockTimeout    time.Duration
	RefreshInterval time.Duration
}

// Load returns a Config populated from environment variables with sane defaults.
func Load() (Config, error) {
	cfg := Config{
		AppName:           getenv("APP_NAME", "awd-infra"),
		LogLevel:          getenv("LOG_LEVEL", "info"),
		Postgres:          loadPostgres(),
		Redis:             loadRedis(),
		ControlPanel:      loadControlPanel(),
		Scheduler:         loadScheduler(),
		Runner:            loadRunner(),
		FlagSubmitter:     loadFlagSubmitter(),
		RunQueue:          loadQueue("RUN", "exp:pending", 30*time.Second, 5*time.Second),
		FlagQueue:         loadQueue("FLAG", "flag:pending", 30*time.Second, 10*time.Second),
		Dispatcher:        loadDispatcher(),
		RunnerSecret:      getenv("RUNNER_SECRET", "dev-runner-secret"),
		SubmitterSecret:   getenv("SUBMITTER_SECRET", "dev-submitter-secret"),
		AllowedRunnerTags: splitAndTrim(getenv("ALLOWED_RUNNER_TAGS", "")),
	}

	if cfg.RunnerSecret == "" {
		return cfg, fmt.Errorf("RUNNER_SECRET cannot be empty")
	}
	if cfg.SubmitterSecret == "" {
		return cfg, fmt.Errorf("SUBMITTER_SECRET cannot be empty")
	}

	return cfg, nil
}

func loadPostgres() PostgresConfig {
	return PostgresConfig{
		DSN:             getenv("POSTGRES_DSN", "postgres://awd:awd@localhost:5432/awd?sslmode=disable"),
		MaxIdleConns:    getenvInt("POSTGRES_MAX_IDLE", 5),
		MaxOpenConns:    getenvInt("POSTGRES_MAX_OPEN", 10),
		ConnMaxLifetime: getenvDuration("POSTGRES_CONN_MAX_LIFETIME", time.Minute*5),
		ArtifactDir:     getenv("POSTGRES_ARTIFACT_DIR", ""),
	}
}

func loadRedis() RedisConfig {
	return RedisConfig{
		Addr:       getenv("REDIS_ADDR", "localhost:6379"),
		Password:   getenv("REDIS_PASSWORD", ""),
		DB:         getenvInt("REDIS_DB", 0),
		TLSEnabled: getenv("REDIS_TLS", "false") == "true",
	}
}

func loadControlPanel() ControlPanelConfig {
	return ControlPanelConfig{
		BindAddr:        getenv("CONTROL_PANEL_BIND", ":8080"),
		MetricsAddr:     getenv("CONTROL_PANEL_METRICS", ":9100"),
		WebsocketOrigin: getenv("CONTROL_PANEL_WS_ORIGIN", "*"),
		AdminToken:      getenv("CONTROL_PANEL_TOKEN", ""),
	}
}

func loadScheduler() SchedulerConfig {
	return SchedulerConfig{
		PollInterval:     getenvDuration("SCHEDULER_POLL_INTERVAL", time.Second*5),
		MaxDispatchBatch: getenvInt("SCHEDULER_MAX_BATCH", 50),
	}
}

func loadRunner() RunnerConfig {
	workspaceRoot := getenv("RUNNER_WORKSPACE_ROOT", "/env/tmp")
	cacheDir := getenv("RUNNER_ARTIFACT_CACHE_DIR", "")
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = filepath.Join(workspaceRoot, "cache")
	}
	return RunnerConfig{
		ID:                    getenv("RUNNER_ID", ""),
		Type:                  getenv("RUNNER_TYPE", "default"),
		ControlPanelURL:       getenv("CONTROL_PANEL_URL", ""),
		ControlPanelTimeout:   getenvDuration("RUNNER_CONTROL_PANEL_TIMEOUT", time.Minute),
		MaxParallelJobs:       getenvInt("RUNNER_MAX_PARALLEL", 2),
		LogFlushInterval:      getenvDuration("RUNNER_LOG_FLUSH_INTERVAL", time.Second*2),
		HeartbeatInterval:     getenvDuration("RUNNER_HEARTBEAT_INTERVAL", time.Second*10),
		ChrootPath:            getenv("RUNNER_CHROOT_PATH", "/env"),
		WorkspaceRoot:         workspaceRoot,
		FlagFileName:          getenv("RUNNER_FLAG_FILE", "flag"),
		DefaultFlagRegex:      getenv("RUNNER_FLAG_REGEX", `flag\{[a-zA-Z0-9_\-]+\}`),
		DefaultTimeout:        getenvDuration("RUNNER_DEFAULT_TIMEOUT", time.Second*120),
		Tags:                  splitAndTrim(getenv("RUNNER_TAGS", "")),
		ArtifactCacheTTL:      getenvDuration("RUNNER_ARTIFACT_CACHE_TTL", time.Minute*30),
		ArtifactCacheDir:      cacheDir,
		ArtifactCacheMaxBytes: getenvInt64("RUNNER_ARTIFACT_CACHE_MAX_BYTES", 2*1024*1024*1024),
	}
}

func loadFlagSubmitter() FlagSubmitterConfig {
	return FlagSubmitterConfig{
		GlobalRateLimit:   getenvInt("FLAG_SUBMIT_GLOBAL_RATE", 10),
		PerChallengeLimit: getenvInt("FLAG_SUBMIT_PER_CHALLENGE", 5),
		RetryBackoff:      getenvDuration("FLAG_SUBMIT_RETRY_BACKOFF", time.Second*30),
		MaxAttempts:       getenvInt("FLAG_SUBMIT_MAX_ATTEMPTS", 5),
		Endpoint:          getenv("FLAG_SUBMIT_URL", "http://flagserver:5000/upload_flag"),
		RequestTimeout:    getenvDuration("FLAG_SUBMIT_TIMEOUT", time.Second*5),
		Concurrency:       getenvInt("FLAG_SUBMIT_CONCURRENCY", 1),
		ID:                getenv("SUBMITTER_ID", ""),
		ControlPanelURL:   getenv("SUBMITTER_CONTROL_PANEL_URL", getenv("CONTROL_PANEL_URL", "")),
		HeartbeatInterval: getenvDuration("SUBMITTER_HEARTBEAT_INTERVAL", time.Second*10),
	}
}

func loadQueue(prefix, defaultName string, defaultVisibility, defaultRetry time.Duration) QueueConfig {
	name := getenv(prefix+"_QUEUE_NAME", defaultName)
	return QueueConfig{
		Name:         name,
		Visibility:   getenvDuration(prefix+"_QUEUE_VISIBILITY", defaultVisibility),
		RetryDelay:   getenvDuration(prefix+"_QUEUE_RETRY_DELAY", defaultRetry),
		RunnerPrefix: getenv(prefix+"_QUEUE_RUNNER_PREFIX", defaultRunnerPrefix(name)),
		TrimMaxLen:   getenvInt64(prefix+"_QUEUE_TRIM_MAXLEN", 50000),
	}
}

func loadDispatcher() DispatcherConfig {
	return DispatcherConfig{
		ID:              getenv("DISPATCHER_ID", "dispatcher"),
		MaxBatch:        getenvInt("DISPATCHER_MAX_BATCH", 32),
		BlockTimeout:    getenvDuration("DISPATCHER_BLOCK_TIMEOUT", 5*time.Second),
		RefreshInterval: getenvDuration("DISPATCHER_REFRESH_INTERVAL", time.Second*10),
	}
}

func defaultRunnerPrefix(base string) string {
	value := strings.TrimSpace(base)
	if value == "" {
		return "runner"
	}
	return value + ":runner"
}

func getenv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		num, err := strconv.Atoi(val)
		if err == nil {
			return num
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		duration, err := time.ParseDuration(val)
		if err == nil {
			return duration
		}
	}
	return fallback
}

func splitAndTrim(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func getenvInt64(key string, fallback int64) int64 {
	if val, ok := os.LookupEnv(key); ok {
		num, err := strconv.ParseInt(val, 10, 64)
		if err == nil {
			return num
		}
	}
	return fallback
}
