package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/config"
	"github.com/tinfoyle/mainspring-engine/internal/control"
	"github.com/tinfoyle/mainspring-engine/internal/database"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	mailbox "github.com/tinfoyle/mainspring-engine/internal/email"
	"github.com/tinfoyle/mainspring-engine/internal/gateway"
	"github.com/tinfoyle/mainspring-engine/internal/httpserver"
	"github.com/tinfoyle/mainspring-engine/internal/migrate"
	"github.com/tinfoyle/mainspring-engine/internal/orchestration"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
	"github.com/tinfoyle/mainspring-engine/internal/scheduling"
	"github.com/tinfoyle/mainspring-engine/internal/secretbox"
	"github.com/tinfoyle/mainspring-engine/internal/tenant"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

func Run(ctx context.Context, logger *slog.Logger, cfg config.Config, mode string, args []string) error {
	switch mode {
	case "control":
		return runControl(ctx, logger, cfg)
	case "gateway":
		return runGateway(ctx, logger, cfg)
	case "migrate":
		return runMigrate(ctx, cfg, args)
	case "tenant":
		return runTenant(ctx, logger, cfg)
	case "rag":
		return runRAG(ctx, logger, cfg)
	case "provision":
		return runProvision(ctx, logger, cfg)
	case "worker":
		return runWorker(ctx, logger, cfg)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func runRAG(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	tenantID, err := domain.ParseTenantID(cfg.TenantID)
	if err != nil {
		return fmt.Errorf("MAINSPRING_TENANT_ID is required and must be a UUID: %w", err)
	}
	pool, err := database.Open(ctx, cfg.TenantDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := migrate.Run(ctx, pool, migrate.Tenant); err != nil {
			return err
		}
	}
	server, err := rag.NewServer(logger.With("service", "rag"), rag.NewStore(pool), tenantID, cfg.RAGToken)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, logger, cfg.RAGAddr, server.Handler(), cfg.ShutdownTimeout)
}

func runProvision(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	tenantID, err := domain.ParseTenantID(cfg.TenantID)
	if err != nil {
		return fmt.Errorf("MAINSPRING_TENANT_ID is required and must be a UUID: %w", err)
	}
	pool, err := database.Open(ctx, cfg.ControlDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Run(ctx, pool, migrate.Control); err != nil {
		return err
	}
	registered, err := control.NewStore(pool).RegisterTenant(ctx, tenantID, cfg.TenantName, cfg.TenantSlug, cfg.TenantInternalURL)
	if err != nil {
		return err
	}
	logger.Info("tenant runtime registered", "tenant_id", registered.ID.String(), "slug", registered.Slug, "internal_url", registered.InternalURL)
	return nil
}

func runTenant(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	tenantID, err := domain.ParseTenantID(cfg.TenantID)
	if err != nil {
		return fmt.Errorf("MAINSPRING_TENANT_ID is required and must be a UUID: %w", err)
	}
	if strings.TrimSpace(cfg.TenantSlug) == "" {
		return fmt.Errorf("MAINSPRING_TENANT_SLUG is required")
	}
	pool, err := database.Open(ctx, cfg.TenantDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := migrate.Run(ctx, pool, migrate.Tenant); err != nil {
			return err
		}
	}

	tenantStore := tenant.NewStore(pool)
	if err := tenantStore.Bootstrap(ctx, tenantID, cfg.TenantSlug, cfg.TenantName); err != nil {
		return err
	}
	boardroomStore := boardroom.NewStore(pool)
	documentClient, err := rag.NewClient(cfg.RAGInternalURL, cfg.RAGToken, tenantID)
	if err != nil {
		return err
	}
	credentialBox, err := secretbox.New([]byte(cfg.CredentialEncryptionKey))
	if err != nil {
		return fmt.Errorf("configure credential encryption: %w", err)
	}
	var emailConnector mailbox.Connector = mailbox.NewNetworkConnector(20 * time.Second)
	if cfg.EmailProvider == "mock" {
		emailConnector = mailbox.NewMockConnector()
	}
	emailService := mailbox.NewService(mailbox.NewStore(pool, credentialBox), emailConnector, toolbroker.NewActionLedger(pool))
	var dispatcher tenant.RunDispatcher
	var scheduleService *scheduling.Service
	if cfg.OrchestrationMode == "temporal" {
		temporalClient, err := orchestration.DialTemporal(cfg.TemporalAddress, cfg.TemporalNamespace)
		if err != nil {
			return fmt.Errorf("connect to Temporal: %w", err)
		}
		defer temporalClient.Close()
		dispatcher = orchestration.NewTemporalDispatcher(temporalClient, tenantID, tenantTaskQueue(cfg, tenantID))
		scheduleService = scheduling.NewService(scheduling.NewStore(pool), temporalClient, tenantID, tenantTaskQueue(cfg, tenantID))
	} else {
		provider, err := agent.NewProvider(cfg.AgentProvider, cfg.CodexBinary)
		if err != nil {
			return err
		}
		issuer, err := toolbroker.NewTokenIssuer([]byte(cfg.ToolTokenSecret), cfg.AgentTimeout+time.Minute)
		if err != nil {
			return err
		}
		runService := boardroom.NewService(logger.With("service", "boardroom"), boardroomStore, provider, tenantID, cfg.AgentTimeout, issuer)
		dispatcher = orchestration.NewLocalDispatcher(ctx, logger.With("service", "dispatcher"), runService, cfg.RunConcurrency)
	}
	server, err := tenant.NewServer(logger.With("service", "tenant"), tenant.ServerConfig{
		TenantID: tenantID, TenantSlug: cfg.TenantSlug, TenantName: cfg.TenantName,
		BaseDomain: cfg.GatewayBaseDomain, SessionSecret: []byte(cfg.SessionSecret), SetupToken: cfg.SetupToken,
		CookieSecure: cfg.CookieSecure, Development: cfg.Environment == "development",
	}, tenantStore, boardroomStore, dispatcher, scheduleService, documentClient, emailService)
	if err != nil {
		return err
	}
	return httpserver.Run(ctx, logger, cfg.TenantAddr, server.Handler(), cfg.ShutdownTimeout)
}

func runWorker(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	tenantID, err := domain.ParseTenantID(cfg.TenantID)
	if err != nil {
		return fmt.Errorf("MAINSPRING_TENANT_ID is required and must be a UUID: %w", err)
	}
	pool, err := database.Open(ctx, cfg.TenantDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := migrate.Run(ctx, pool, migrate.Tenant); err != nil {
			return err
		}
	}
	provider, err := agent.NewProvider(cfg.AgentProvider, cfg.CodexBinary)
	if err != nil {
		return err
	}
	issuer, err := toolbroker.NewTokenIssuer([]byte(cfg.ToolTokenSecret), cfg.AgentTimeout+time.Minute)
	if err != nil {
		return err
	}
	service := boardroom.NewService(logger.With("service", "boardroom"), boardroom.NewStore(pool), provider, tenantID, cfg.AgentTimeout, issuer)
	temporalClient, err := orchestration.DialTemporal(cfg.TemporalAddress, cfg.TemporalNamespace)
	if err != nil {
		return fmt.Errorf("connect to Temporal: %w", err)
	}
	defer temporalClient.Close()
	return orchestration.RunWorker(ctx, logger.With("service", "worker"), temporalClient, tenantTaskQueue(cfg, tenantID), orchestration.NewActivities(tenantID, service))
}

func tenantTaskQueue(cfg config.Config, tenantID domain.TenantID) string {
	if value := strings.TrimSpace(cfg.TemporalTaskQueue); value != "" {
		return value
	}
	return "mainspring-tenant-" + tenantID.String()
}

func runControl(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	if len(cfg.ControlAdminToken) < 32 {
		return fmt.Errorf("MAINSPRING_CONTROL_ADMIN_TOKEN must contain at least 32 bytes")
	}
	pool, err := database.Open(ctx, cfg.ControlDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := migrate.Run(ctx, pool, migrate.Control); err != nil {
			return err
		}
	}

	server := control.NewServer(logger.With("service", "control"), pool, cfg.ControlAdminToken)
	return httpserver.Run(ctx, logger, cfg.ControlAddr, server.Handler(), cfg.ShutdownTimeout)
}

func runGateway(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	pool, err := database.Open(ctx, cfg.ControlDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := migrate.Run(ctx, pool, migrate.Control); err != nil {
			return err
		}
	}

	store := control.NewStore(pool)
	server := gateway.NewServer(logger.With("service", "gateway"), store, cfg.GatewayBaseDomain, cfg.GatewayRouteCacheTTL)
	return httpserver.Run(ctx, logger, cfg.GatewayAddr, server.Handler(), cfg.ShutdownTimeout)
}

func runMigrate(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: mainspring migrate <control|tenant>")
	}
	kind := migrate.Kind(args[0])
	databaseURL := cfg.ControlDatabaseURL
	if kind == migrate.Tenant {
		databaseURL = cfg.TenantDatabaseURL
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return migrate.Run(ctx, pool, kind)
}
