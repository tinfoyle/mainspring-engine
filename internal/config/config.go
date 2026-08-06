package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

type Config struct {
	Environment     string
	LogLevel        string
	ShutdownTimeout time.Duration
	AutoMigrate     bool

	ControlAddr        string
	ControlDatabaseURL string
	ControlAdminToken  string

	GatewayAddr          string
	GatewayBaseDomain    string
	GatewayRouteCacheTTL time.Duration

	TenantAddr        string
	TenantID          string
	TenantSlug        string
	TenantName        string
	TenantInternalURL string
	TenantDatabaseURL string
	SessionSecret     string
	SetupToken        string
	CookieSecure      bool
	RunConcurrency    int

	RAGAddr        string
	RAGInternalURL string
	RAGToken       string

	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
	OrchestrationMode string

	AgentProvider   string
	CodexBinary     string
	AgentTimeout    time.Duration
	ToolTokenSecret string
}

func Load() (Config, error) {
	cfg := Config{
		Environment:        env("MAINSPRING_ENV", "development"),
		LogLevel:           strings.ToLower(env("MAINSPRING_LOG_LEVEL", "info")),
		AutoMigrate:        boolean("MAINSPRING_AUTO_MIGRATE", true),
		ControlAddr:        env("MAINSPRING_CONTROL_ADDR", ":8080"),
		ControlDatabaseURL: os.Getenv("MAINSPRING_CONTROL_DATABASE_URL"),
		ControlAdminToken:  os.Getenv("MAINSPRING_CONTROL_ADMIN_TOKEN"),
		GatewayAddr:        env("MAINSPRING_GATEWAY_ADDR", ":8081"),
		GatewayBaseDomain:  strings.ToLower(env("MAINSPRING_GATEWAY_BASE_DOMAIN", "lvh.me")),
		TenantAddr:         env("MAINSPRING_TENANT_ADDR", ":8082"),
		TenantID:           os.Getenv("MAINSPRING_TENANT_ID"),
		TenantSlug:         strings.ToLower(os.Getenv("MAINSPRING_TENANT_SLUG")),
		TenantName:         env("MAINSPRING_TENANT_NAME", "Mainspring Boardroom"),
		TenantInternalURL:  os.Getenv("MAINSPRING_TENANT_INTERNAL_URL"),
		TenantDatabaseURL:  os.Getenv("MAINSPRING_TENANT_DATABASE_URL"),
		SessionSecret:      os.Getenv("MAINSPRING_SESSION_SECRET"),
		SetupToken:         os.Getenv("MAINSPRING_SETUP_TOKEN"),
		CookieSecure:       boolean("MAINSPRING_COOKIE_SECURE", false),
		RunConcurrency:     integer("MAINSPRING_RUN_CONCURRENCY", 4),
		RAGAddr:            env("MAINSPRING_RAG_ADDR", ":8083"),
		RAGInternalURL:     env("MAINSPRING_RAG_INTERNAL_URL", "http://127.0.0.1:8083"),
		RAGToken:           os.Getenv("MAINSPRING_RAG_TOKEN"),
		TemporalAddress:    env("MAINSPRING_TEMPORAL_ADDRESS", "localhost:7233"),
		TemporalNamespace:  env("MAINSPRING_TEMPORAL_NAMESPACE", "default"),
		TemporalTaskQueue:  strings.TrimSpace(os.Getenv("MAINSPRING_TEMPORAL_TASK_QUEUE")),
		OrchestrationMode:  strings.ToLower(env("MAINSPRING_ORCHESTRATION", "local")),
		AgentProvider:      strings.ToLower(env("MAINSPRING_AGENT_PROVIDER", "mock")),
		CodexBinary:        env("MAINSPRING_CODEX_BINARY", "codex"),
		ToolTokenSecret:    os.Getenv("MAINSPRING_TOOL_TOKEN_SECRET"),
	}

	var err error
	if cfg.ShutdownTimeout, err = duration("MAINSPRING_SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.GatewayRouteCacheTTL, err = duration("MAINSPRING_GATEWAY_ROUTE_CACHE_TTL", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.AgentTimeout, err = duration("MAINSPRING_AGENT_TIMEOUT", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.ToolTokenSecret) == "" {
		cfg.ToolTokenSecret = cfg.SessionSecret
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, errors.New("MAINSPRING_LOG_LEVEL must be debug, info, warn, or error")
	}
	switch cfg.OrchestrationMode {
	case "local", "temporal":
	default:
		return Config{}, errors.New("MAINSPRING_ORCHESTRATION must be local or temporal")
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func boolean(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func integer(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			return fallback
		}
		parsed = parsed*10 + int(character-'0')
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, errors.New(key + " must be a valid duration: " + err.Error())
	}
	if parsed <= 0 {
		return 0, errors.New(key + " must be greater than zero")
	}
	return parsed, nil
}
