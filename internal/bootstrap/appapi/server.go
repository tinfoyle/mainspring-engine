package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/admissionhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeaccess"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

type Config struct {
	DatabaseURL        string
	CellID             ids.CellID
	RouteIssuer        string
	RouteVerifyKeys    map[string][]byte
	MaxDatabaseConns   int32
	MaxRequestBody     int64
	AdmissionOrigin    string
	AdmissionTransport http.RoundTripper
	AllowHTTPAdmission bool
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger, clock routecontext.Clock) (*Server, error) {
	if config.DatabaseURL == "" || !routecontext.ValidCellID(config.CellID) || config.RouteIssuer == "" || len(config.RouteVerifyKeys) == 0 || config.AdmissionOrigin == "" || logger == nil || clock == nil {
		return nil, errors.New("cell app API configuration is required")
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
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
	cellPool, err := database.NewCellPool(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	receipts, err := postgres.NewRouteContextReceiptRepository(cellPool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	verifier, err := routecontext.NewVerifier(config.RouteIssuer, routecontext.Audience(config.CellID), config.RouteVerifyKeys, routecontext.MaximumLifetime, routecontext.DefaultClockSkew, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	acceptor, err := routecontext.NewAcceptor(verifier, receipts, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	maxBody := config.MaxRequestBody
	if maxBody == 0 {
		maxBody = cellapi.DefaultMaxBody
	}
	workRepository, err := postgres.NewWorkRepository(cellPool, ids.RandomGenerator{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	workQueries, err := workapp.NewQueryService(routeaccess.NewAuthorizer(), workRepository)
	if err != nil {
		pool.Close()
		return nil, err
	}
	capacity, err := admissionhttp.New(config.AdmissionOrigin, config.AllowHTTPAdmission, config.AdmissionTransport)
	if err != nil {
		pool.Close()
		return nil, err
	}
	workCommands, err := workapp.NewService(routeaccess.NewAuthorizer(), capacity, workRepository, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	agentRepository, err := postgres.NewAgentRepository(cellPool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	agentService, err := agentapp.New(routeaccess.NewAuthorizer(), agentRepository, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	attentionRepository, err := postgres.NewAttentionRepository(cellPool, ids.RandomGenerator{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	actionRecoveryRepository, err := postgres.NewActionRecoveryRepository(cellPool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	actionRecoveryService, err := actionrecovery.New(routeaccess.NewAuthorizer(), actionRecoveryRepository, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	attentionService, err := attentionapp.NewService(routeaccess.NewAuthorizer(), capacity, attentionRepository, clock, attentionapp.WithWorkResumer(workCommands))
	if err != nil {
		pool.Close()
		return nil, err
	}
	transport, err := cellapi.New(acceptor, logger, maxBody, cellapi.WithWorkQueries(workQueries), cellapi.WithWorkCommands(workCommands), cellapi.WithAgents(agentService), cellapi.WithAttention(attentionService), cellapi.WithActionRecovery(actionRecoveryService))
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Server{Handler: withHealth(pool, transport, capacity, transport.Handler()), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }

func withHealth(pool *pgxpool.Pool, routes *cellapi.Server, admission *admissionhttp.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health/live" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"alive"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/health/ready" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			w.Header().Set("Content-Type", "application/json")
			if err := pool.Ping(ctx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ready"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/health/status" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "route_context": routes.Stats(), "admission_transport": admission.TransportStats()})
			return
		}
		next.ServeHTTP(w, r)
	})
}
