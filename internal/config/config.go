package config

import (
	"fmt"
	"os"
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
	ControlPlane      ControlPlaneConfig
	Scheduler         SchedulerConfig
	Runner            RunnerConfig
	FlagSubmitter     FlagSubmitterConfig
	RunQueue          QueueConfig
	FlagQueue         QueueConfig
	RunnerSecret      string
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

type ControlPlaneConfig struct {
	BindAddr        string
	MetricsAddr     string
	WebsocketOrigin string
}

type SchedulerConfig struct {
	PollInterval     time.Duration
	MaxDispatchBatch int
}

type RunnerConfig struct {
	ID                string
	Type              string
	ControlPlaneURL   string
	MaxParallelJobs   int
	LogFlushInterval  time.Duration
	HeartbeatInterval time.Duration
	ChrootPath        string
	WorkspaceRoot     string
	FlagFileName      string
	DefaultFlagRegex  string
	DefaultTimeout    time.Duration
	Tags              []string
}

type FlagSubmitterConfig struct {
	GlobalRateLimit   int
	PerChallengeLimit int
	RetryBackoff      time.Duration
	MaxAttempts       int
	Endpoint          string
	RequestTimeout    time.Duration
	Concurrency       int
}

type QueueConfig struct {
	Name       string
	Visibility time.Duration
	RetryDelay time.Duration
}

// Load returns a Config populated from environment variables with sane defaults.
func Load() (Config, error) {
	cfg := Config{
		AppName:           getenv("APP_NAME", "awd-infra"),
		LogLevel:          getenv("LOG_LEVEL", "info"),
		Postgres:          loadPostgres(),
		Redis:             loadRedis(),
		ControlPlane:      loadControlPlane(),
		Scheduler:         loadScheduler(),
		Runner:            loadRunner(),
		FlagSubmitter:     loadFlagSubmitter(),
		RunQueue:          loadQueue("RUN", "exp:pending", 30*time.Second, 5*time.Second),
		FlagQueue:         loadQueue("FLAG", "flag:pending", 30*time.Second, 10*time.Second),
		RunnerSecret:      getenv("RUNNER_SECRET", "dev-runner-secret"),
		AllowedRunnerTags: splitAndTrim(getenv("ALLOWED_RUNNER_TAGS", "")),
	}

	if cfg.RunnerSecret == "" {
		return cfg, fmt.Errorf("RUNNER_SECRET cannot be empty")
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

func loadControlPlane() ControlPlaneConfig {
	return ControlPlaneConfig{
		BindAddr:        getenv("CONTROL_PLANE_BIND", ":8080"),
		MetricsAddr:     getenv("CONTROL_PLANE_METRICS", ":9100"),
		WebsocketOrigin: getenv("CONTROL_PLANE_WS_ORIGIN", "*"),
	}
}

func loadScheduler() SchedulerConfig {
	return SchedulerConfig{
		PollInterval:     getenvDuration("SCHEDULER_POLL_INTERVAL", time.Second*5),
		MaxDispatchBatch: getenvInt("SCHEDULER_MAX_BATCH", 50),
	}
}

func loadRunner() RunnerConfig {
	return RunnerConfig{
		ID:                getenv("RUNNER_ID", ""),
		Type:              getenv("RUNNER_TYPE", "default"),
		ControlPlaneURL:   getenv("CONTROL_PLANE_URL", ""),
		MaxParallelJobs:   getenvInt("RUNNER_MAX_PARALLEL", 2),
		LogFlushInterval:  getenvDuration("RUNNER_LOG_FLUSH_INTERVAL", time.Second*2),
		HeartbeatInterval: getenvDuration("RUNNER_HEARTBEAT_INTERVAL", time.Second*10),
		ChrootPath:        getenv("RUNNER_CHROOT_PATH", "/env"),
		WorkspaceRoot:     getenv("RUNNER_WORKSPACE_ROOT", "/env/tmp"),
		FlagFileName:      getenv("RUNNER_FLAG_FILE", "flag"),
		DefaultFlagRegex:  getenv("RUNNER_FLAG_REGEX", `flag\{[a-zA-Z0-9_\-]+\}`),
		DefaultTimeout:    getenvDuration("RUNNER_DEFAULT_TIMEOUT", time.Second*120),
		Tags:              splitAndTrim(getenv("RUNNER_TAGS", "")),
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
	}
}

func loadQueue(prefix, defaultName string, defaultVisibility, defaultRetry time.Duration) QueueConfig {
	return QueueConfig{
		Name:       getenv(prefix+"_QUEUE_NAME", defaultName),
		Visibility: getenvDuration(prefix+"_QUEUE_VISIBILITY", defaultVisibility),
		RetryDelay: getenvDuration(prefix+"_QUEUE_RETRY_DELAY", defaultRetry),
	}
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
