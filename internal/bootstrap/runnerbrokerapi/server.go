package runnerbrokerapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/kubernetes"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/modelgatewayhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/stripeaction"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/toolrouterhttp"
	"github.com/tinfoyle/spyglass-engine/internal/application/approvedaction"
	"github.com/tinfoyle/spyglass-engine/internal/application/financeaction"
	"github.com/tinfoyle/spyglass-engine/internal/application/marketingaction"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/runneraction"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/observability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	brokertransport "github.com/tinfoyle/spyglass-engine/internal/transport/runnerbrokerapi"
	capabilitytransport "github.com/tinfoyle/spyglass-engine/internal/transport/runnercapabilityapi"
	"github.com/tinfoyle/spyglass-engine/internal/transport/toolrouter"
)

type Config struct {
	CellDatabaseURL, BrokerAudience, Namespace, RunnerServiceAccount string
	EncryptionKeys                                                   map[int][]byte
	ActiveKeyVersion                                                 int
	MaxDatabaseConns                                                 int32
	MaxRequestBody                                                   int64
	IdentityVerifier                                                 runnerbroker.IdentityVerifier
	ToolRouterOrigin, ToolIssuer, ToolSigningKeyID                   string
	ToolSigningKey                                                   []byte
	ToolLifetime                                                     time.Duration
	ToolTransport                                                    http.RoundTripper
	ModelGatewayOrigin                                               string
	ModelTransport                                                   http.RoundTripper
	StripeSecretKey, StripeAPIVersion                                string
	StripeHTTPClient                                                 *http.Client
}

type Server struct {
	Handler         http.Handler
	pool            *pgxpool.Pool
	approvedActions interface {
		ProcessOne(context.Context) (bool, error)
	}
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	if config.CellDatabaseURL == "" || config.BrokerAudience == "" || config.ToolRouterOrigin == "" || config.ModelGatewayOrigin == "" || config.ToolIssuer == "" || config.ToolSigningKeyID == "" || logger == nil {
		return nil, errors.New("runner broker configuration is required")
	}
	if config.IdentityVerifier == nil && (config.Namespace == "" || config.RunnerServiceAccount == "") {
		return nil, errors.New("Kubernetes runner broker identity configuration is required")
	}
	poolConfig, err := pgxpool.ParseConfig(config.CellDatabaseURL)
	if err != nil {
		return nil, err
	}
	if config.MaxDatabaseConns > 0 {
		poolConfig.MaxConns = config.MaxDatabaseConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	verifier := config.IdentityVerifier
	if verifier == nil {
		verifier, err = kubernetes.NewInClusterRunnerIdentity(kubernetes.RunnerIdentityConfig{
			Audience: config.BrokerAudience, Namespace: config.Namespace, RunnerServiceAccount: config.RunnerServiceAccount,
		})
		if err != nil {
			pool.Close()
			return nil, err
		}
	}
	cipher, err := runnerbroker.NewCipher(config.EncryptionKeys, config.ActiveKeyVersion)
	if err != nil {
		pool.Close()
		return nil, err
	}
	repository, err := postgres.NewRunnerBrokerRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	exchange, err := runnerbroker.NewService(repository, verifier, cipher, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	maxBody := config.MaxRequestBody
	if maxBody == 0 {
		maxBody = brokertransport.DefaultMaxBody
	}
	transport, err := brokertransport.New(exchange, logger, maxBody)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if config.ToolLifetime == 0 {
		config.ToolLifetime = toolcontext.DefaultLifetime
	}
	toolSigner, err := toolcontext.NewSigner(config.ToolIssuer, config.ToolSigningKeyID, config.ToolSigningKey, config.ToolLifetime, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	toolHandler, err := toolrouterhttp.New(toolrouterhttp.Config{Origin: config.ToolRouterOrigin, Signer: toolSigner, IDs: ids.RandomGenerator{}, HTTPClient: clientFor(config.ToolTransport)})
	if err != nil {
		pool.Close()
		return nil, err
	}
	modelHandler, err := modelgatewayhttp.New(modelgatewayhttp.Config{Origin: config.ModelGatewayOrigin, HTTPClient: modelClientFor(config.ModelTransport)})
	if err != nil {
		pool.Close()
		return nil, err
	}
	stripeCustomerHandler, err := stripeaction.NewCustomerHandler(config.StripeSecretKey, config.StripeAPIVersion, config.StripeHTTPClient)
	if err != nil {
		pool.Close()
		return nil, err
	}
	cellPool, err := database.NewCellPool(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	financeRepository, err := postgres.NewFinanceRepository(cellPool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	financeActionStore, err := postgres.NewFinanceActionStore(cellPool, financeRepository, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	financePostHandler, err := financeaction.NewEntryPostHandler(financeActionStore)
	if err != nil {
		pool.Close()
		return nil, err
	}
	marketingActionStore, err := postgres.NewMarketingActionStore(cellPool, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	marketingActivateHandler, err := marketingaction.NewReleaseActivateHandler(marketingActionStore)
	if err != nil {
		pool.Close()
		return nil, err
	}
	auditor, err := postgres.NewRunnerCapabilityAuditor(pool, ids.RandomGenerator{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	actionRepository, err := postgres.NewRunnerActionRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	actions, err := runneraction.New(actionRepository, ids.RandomGenerator{}, registration.SystemClock{}, runneraction.DefaultLease)
	if err != nil {
		pool.Close()
		return nil, err
	}
	capabilities, err := runnercapability.New(exchange, actions, auditor, registration.SystemClock{}, []runnercapability.Definition{
		{Capability: toolrouter.WorkSummaryCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.FinanceLedgersReadCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.FinanceAccountsReadCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.FinanceEntryDraftCapability, Effect: runnercapability.EffectAdditive, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingCampaignsReadCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingAssetsReadCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingReleasesReadCapability, Effect: runnercapability.EffectReadOnly, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingCampaignDraftCapability, Effect: runnercapability.EffectAdditive, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingAssetDraftCapability, Effect: runnercapability.EffectAdditive, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: toolrouter.MarketingReleaseDraftCapability, Effect: runnercapability.EffectAdditive, Timeout: 15 * time.Second, Handler: toolHandler},
		{Capability: modelgateway.ModelTurnCapability, Effect: runnercapability.EffectReadOnly, Timeout: modelgateway.MaximumProviderTimeout, Handler: modelHandler},
		{Capability: stripeaction.CustomerCreateCapability, Effect: runnercapability.EffectConsequential, Timeout: 20 * time.Second, Handler: stripeCustomerHandler},
	})
	if err != nil {
		pool.Close()
		return nil, err
	}
	approvedRepository, err := postgres.NewApprovedActionRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	approvedActions, err := approvedaction.New(approvedRepository, ids.RandomGenerator{}, registration.SystemClock{}, approvedaction.DefaultLease, []approvedaction.Definition{
		{Capability: stripeaction.CustomerCreateCapability, Timeout: 20 * time.Second, Handler: stripeCustomerHandler},
		{Capability: financeaction.EntryPostCapability, Timeout: 15 * time.Second, Handler: financePostHandler},
		{Capability: marketingaction.ReleaseActivateCapability, Timeout: 15 * time.Second, Handler: marketingActivateHandler},
	})
	if err != nil {
		pool.Close()
		return nil, err
	}
	capabilityAPI, err := capabilitytransport.New(capabilities, logger, capabilitytransport.DefaultMaxBody)
	if err != nil {
		pool.Close()
		return nil, err
	}
	brokerHandler, capabilityHandler := transport.Handler(), capabilityAPI.Handler()
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= len("/capabilities:invoke") && r.URL.Path[len(r.URL.Path)-len("/capabilities:invoke"):] == "/capabilities:invoke" {
			capabilityHandler.ServeHTTP(w, r)
			return
		}
		brokerHandler.ServeHTTP(w, r)
	})
	return &Server{Handler: withHealth(pool, combined), pool: pool, approvedActions: approvedActions}, nil
}

func clientFor(transport http.RoundTripper) *http.Client {
	if transport == nil {
		return nil
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func modelClientFor(transport http.RoundTripper) *http.Client {
	if transport == nil {
		return nil
	}
	return &http.Client{Transport: transport, Timeout: modelgateway.MaximumProviderTimeout}
}

func (s *Server) Close() { s.pool.Close() }

func (s *Server) RunApprovedActions(ctx context.Context, logger *slog.Logger) {
	for {
		worked, err := s.approvedActions.ProcessOne(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Error("Process approved action", "error", err)
		}
		if worked {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func withHealth(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health/live" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(`{"status":"alive"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/health/ready" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if err := pool.Ping(ctx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ready"}`))
			return
		}
		if r.Method == http.MethodGet && (r.URL.Path == "/health/status" || r.URL.Path == "/metrics") {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			status, err := runnerActionStatus(ctx, pool)
			if err != nil {
				http.Error(w, "status unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.Path == "/metrics" {
				metrics, renderErr := observability.RenderWorkerMetrics("runner-action", status)
				if renderErr != nil {
					http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
				_, _ = w.Write(metrics)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type actionStatus struct {
	Executing               uint64 `json:"executing"`
	Reconciling             uint64 `json:"reconciling"`
	RetryWait               uint64 `json:"retry_wait"`
	Unknown                 uint64 `json:"unknown"`
	ManualResolution        uint64 `json:"manual_resolution"`
	Failed                  uint64 `json:"failed"`
	Succeeded               uint64 `json:"succeeded"`
	OldestRetryDueAgeSecond uint64 `json:"oldest_retry_due_age_seconds"`
}

func runnerActionStatus(ctx context.Context, pool *pgxpool.Pool) (actionStatus, error) {
	var status actionStatus
	err := pool.QueryRow(ctx, `SELECT executing,reconciling,retry_wait,unknown,manual_resolution,failed,succeeded,oldest_retry_due_age_seconds
		FROM public.spyglass_runner_action_stats(statement_timestamp())`).Scan(
		&status.Executing, &status.Reconciling, &status.RetryWait, &status.Unknown,
		&status.ManualResolution, &status.Failed, &status.Succeeded, &status.OldestRetryDueAgeSecond)
	return status, err
}
