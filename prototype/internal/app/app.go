package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/config"
	"github.com/tinfoyle/mainspring-engine/internal/control"
	"github.com/tinfoyle/mainspring-engine/internal/database"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	mailbox "github.com/tinfoyle/mainspring-engine/internal/email"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
	"github.com/tinfoyle/mainspring-engine/internal/gateway"
	"github.com/tinfoyle/mainspring-engine/internal/gdrive"
	"github.com/tinfoyle/mainspring-engine/internal/httpserver"
	"github.com/tinfoyle/mainspring-engine/internal/migrate"
	"github.com/tinfoyle/mainspring-engine/internal/orchestration"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
	"github.com/tinfoyle/mainspring-engine/internal/runner"
	"github.com/tinfoyle/mainspring-engine/internal/scheduling"
	"github.com/tinfoyle/mainspring-engine/internal/secretbox"
	"github.com/tinfoyle/mainspring-engine/internal/tenant"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
	"github.com/tinfoyle/mainspring-engine/internal/webresearch"
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
	case "runner-controller":
		return runRunnerController(ctx, logger, cfg)
	case "runner":
		if len(args) != 2 {
			return fmt.Errorf("usage: mainspring runner <input.json> <output.json>")
		}
		return runner.RunJob(ctx, args[0], args[1], cfg.CodexBinary)
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

	businessTemplate := tenant.ParseBusinessTemplate(cfg.TenantTemplate)
	tenantStore := tenant.NewStore(pool, businessTemplate)
	if err := tenantStore.Bootstrap(ctx, tenantID, cfg.TenantSlug, cfg.TenantName); err != nil {
		return err
	}
	go tenantStore.RunBaselineMaintenance(ctx, tenantID, 12*time.Hour)
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
	actionLedger := toolbroker.NewActionLedger(pool)
	emailService := mailbox.NewService(mailbox.NewStore(pool, credentialBox), emailConnector, actionLedger)
	driveService := gdrive.NewService(pool, tenantID, credentialBox, cfg.GoogleDriveClientID, cfg.GoogleDriveClientSecret, cfg.GoogleDriveRedirectURL)
	approvalService := toolbroker.NewApprovalService(pool, actionLedger)
	usageService := boardroom.NewUsageService(pool, logger.With("service", "usage"))
	issuer, err := toolbroker.NewTokenIssuer([]byte(cfg.ToolTokenSecret), 61*time.Minute)
	if err != nil {
		return err
	}
	webProvider, err := newWebResearchProvider(cfg)
	if err != nil {
		return err
	}
	broker, err := newToolBroker(pool, issuer, documentClient, webProvider)
	if err != nil {
		return err
	}
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
		provider, err := newAgentProvider(cfg)
		if err != nil {
			return err
		}
		runService := boardroom.NewService(logger.With("service", "boardroom"), boardroomStore, provider, tenantID, cfg.AgentTimeout, issuer, broker)
		runService.SetApprovalService(approvalService)
		runService.SetUsageService(usageService)
		runService.SetBudgets(int64(cfg.AgentContextTokens), int64(cfg.AgentOutputTokens), int64(cfg.AgentTurnCostMicros))
		dispatcher = orchestration.NewLocalDispatcher(ctx, logger.With("service", "dispatcher"), runService, cfg.RunConcurrency)
	}
	migratedInputRuns, err := approvalService.MigratePendingOwnerInputs(ctx)
	if err != nil {
		return fmt.Errorf("migrate pending owner input approvals: %w", err)
	}
	recoveredInputs, err := approvalService.RecoverMissingHumanInputs(ctx)
	if err != nil {
		return fmt.Errorf("recover missing owner inputs: %w", err)
	}
	if recoveredInputs > 0 {
		logger.Info("recovered missing owner inputs", "count", recoveredInputs)
	}
	strandedRuns, err := approvalService.RunsAwaitingNoApproval(ctx)
	if err != nil {
		return fmt.Errorf("find stranded approval runs: %w", err)
	}
	runsToResume := map[domain.RunID]bool{}
	for _, runID := range append(migratedInputRuns, strandedRuns...) {
		runsToResume[runID] = true
	}
	for runID := range runsToResume {
		if err := dispatcher.ApprovalDecision(ctx, runID, "migrated-to-your-turn"); err != nil {
			logger.Warn("resume owner input run", "run_id", runID.String(), "error", err)
		}
	}
	server, err := tenant.NewServer(logger.With("service", "tenant"), tenant.ServerConfig{
		TenantID: tenantID, TenantSlug: cfg.TenantSlug, TenantName: cfg.TenantName,
		BusinessTemplate: businessTemplate,
		BaseDomain:       cfg.GatewayBaseDomain, SessionSecret: []byte(cfg.SessionSecret), SetupToken: cfg.SetupToken,
		CookieSecure: cfg.CookieSecure, Development: cfg.Environment == "development", ControlAdminToken: cfg.ControlAdminToken,
		MCPToken: cfg.MCPToken, MCPUserEmail: cfg.MCPUserEmail,
	}, tenantStore, boardroomStore, dispatcher, scheduleService, documentClient, emailService, driveService, webProvider, approvalService, usageService)
	if err != nil {
		return err
	}
	server.SetToolBroker(issuer, broker)
	go tenant.RunAgentWorkDispatcher(ctx, logger.With("service", "agent-work"), tenantStore, boardroomStore, dispatcher, cfg.RunConcurrency, 5*time.Second)
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
	provider, err := newAgentProvider(cfg)
	if err != nil {
		return err
	}
	issuer, err := toolbroker.NewTokenIssuer([]byte(cfg.ToolTokenSecret), 61*time.Minute)
	if err != nil {
		return err
	}
	documentClient, err := rag.NewClient(cfg.RAGInternalURL, cfg.RAGToken, tenantID)
	if err != nil {
		return err
	}
	webProvider, err := newWebResearchProvider(cfg)
	if err != nil {
		return err
	}
	broker, err := newToolBroker(pool, issuer, documentClient, webProvider)
	if err != nil {
		return err
	}
	service := boardroom.NewService(logger.With("service", "boardroom"), boardroom.NewStore(pool), provider, tenantID, cfg.AgentTimeout, issuer, broker)
	service.SetApprovalService(toolbroker.NewApprovalService(pool, toolbroker.NewActionLedger(pool)))
	service.SetUsageService(boardroom.NewUsageService(pool, logger.With("service", "usage")))
	service.SetBudgets(int64(cfg.AgentContextTokens), int64(cfg.AgentOutputTokens), int64(cfg.AgentTurnCostMicros))
	temporalClient, err := orchestration.DialTemporal(cfg.TemporalAddress, cfg.TemporalNamespace)
	if err != nil {
		return fmt.Errorf("connect to Temporal: %w", err)
	}
	defer temporalClient.Close()
	return orchestration.RunWorker(ctx, logger.With("service", "worker"), temporalClient, tenantTaskQueue(cfg, tenantID), orchestration.NewActivities(tenantID, service))
}

func newAgentProvider(cfg config.Config) (agent.Provider, error) {
	if cfg.AgentProvider == "remote" || cfg.AgentProvider == "runner" {
		return agent.NewRemoteProvider(cfg.RunnerURL, cfg.RunnerProvider, cfg.AgentTimeout)
	}
	return agent.NewProvider(cfg.AgentProvider, cfg.CodexBinary)
}

func runRunnerController(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	server, err := runner.NewServer(logger.With("service", "runner-controller"), runner.NewDockerClient(cfg.RunnerSocket), runner.ContainerLimits{
		Image: cfg.RunnerImage, Network: cfg.RunnerNetwork, MemoryBytes: int64(cfg.RunnerMemoryMB) << 20,
		NanoCPUs: int64(cfg.RunnerCPUMillis) * 1_000_000, PIDs: int64(cfg.RunnerPIDs), Timeout: cfg.AgentTimeout,
		CodexAuthPath: cfg.RunnerCodexAuthPath, CACertPath: cfg.RunnerCACertPath,
	}, cfg.RunnerConcurrency)
	if err != nil {
		return err
	}
	cleanup, cancel := context.WithTimeout(ctx, 15*time.Second)
	if err := server.CleanupOrphans(cleanup, true); err != nil {
		logger.Warn("initial runner reconciliation", "error", err)
	}
	cancel()
	go server.RunCleanup(ctx, time.Minute)
	return httpserver.Run(ctx, logger, cfg.RunnerAddr, server.Handler(), cfg.ShutdownTimeout)
}

func newToolBroker(pool *pgxpool.Pool, issuer *toolbroker.TokenIssuer, documentClient *rag.Client, webProvider webresearch.Provider) (*toolbroker.Broker, error) {
	broker := toolbroker.NewBroker(issuer, toolbroker.NewPostgresAuditor(pool))
	if err := toolbroker.RegisterDocumentSearch(broker, documentClient); err != nil {
		return nil, fmt.Errorf("register document search tool: %w", err)
	}
	if err := toolbroker.RegisterDocumentWrite(broker, documentClient); err != nil {
		return nil, fmt.Errorf("register document write tools: %w", err)
	}
	if webProvider != nil {
		if err := toolbroker.RegisterWebResearch(broker, webProvider); err != nil {
			return nil, fmt.Errorf("register web research tools: %w", err)
		}
	}
	if err := toolbroker.RegisterFinance(broker, finance.NewService(pool)); err != nil {
		return nil, fmt.Errorf("register finance tools: %w", err)
	}
	return broker, nil
}

func newWebResearchProvider(cfg config.Config) (webresearch.Provider, error) {
	if cfg.WebResearchProvider == "disabled" {
		return nil, nil
	}
	provider, err := webresearch.NewFirecrawl(cfg.FirecrawlURL, cfg.FirecrawlToken, cfg.FirecrawlTimeout)
	if err != nil {
		return nil, fmt.Errorf("configure Firecrawl web research: %w", err)
	}
	return provider, nil
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
