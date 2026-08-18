package approuter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	routertransport "github.com/tinfoyle/spyglass-engine/internal/transport/approuter"
	"github.com/tinfoyle/spyglass-engine/internal/transport/toolrouter"
)

type Config struct {
	DatabaseURL       string
	MaxDatabaseConns  int32
	RouteIssuer       string
	RouteSigningKeyID string
	RouteSigningKey   []byte
	RouteLifetime     time.Duration
	ToolIssuer        string
	ToolVerifyKeys    map[string][]byte
	DirectoryCacheTTL time.Duration
	DirectoryCapacity int
	CellTransport     http.RoundTripper
	SessionCookieName string
	SecureCookies     bool
	TrustedOrigins    []string
	AllowHTTPCells    bool
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger, clock routecontext.Clock) (*Server, error) {
	if config.DatabaseURL == "" || config.RouteIssuer == "" || config.RouteSigningKeyID == "" || len(config.RouteSigningKey) < routecontext.MinimumKeyBytes || config.ToolIssuer == "" || len(config.ToolVerifyKeys) == 0 || logger == nil || clock == nil {
		return nil, errors.New("app router configuration is required")
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
	if config.RouteLifetime == 0 {
		config.RouteLifetime = routecontext.DefaultLifetime
	}
	signer, err := routecontext.NewSigner(config.RouteIssuer, config.RouteSigningKeyID, config.RouteSigningKey, config.RouteLifetime, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	sessionService, err := sessions.NewService(postgres.NewSessionRepository(pool), ids.RandomGenerator{}, registration.SystemClock{}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	securityPosture, err := securityposture.NewService(postgres.NewSecurityPostureRepository(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	accessRepository := postgres.NewAccessRepository(pool)
	authorizer, err := access.NewAuthorizer(accessRepository, access.WithOwnerSecurityPolicy(securityPosture))
	if err != nil {
		pool.Close()
		return nil, err
	}
	directory, err := accountdirectory.NewCache(postgres.NewAccountDirectoryRepository(pool), clock, accountdirectory.Config{TTL: config.DirectoryCacheTTL, Capacity: config.DirectoryCapacity, AllowHTTP: config.AllowHTTPCells})
	if err != nil {
		pool.Close()
		return nil, err
	}
	transport, err := routertransport.New(sessionService, authorizer, directory, signer, ids.RandomGenerator{}, routertransport.Config{SessionCookieName: config.SessionCookieName, SecureCookies: config.SecureCookies, TrustedOrigins: config.TrustedOrigins, Transport: config.CellTransport}, logger)
	if err != nil {
		pool.Close()
		return nil, err
	}
	toolVerifier, err := toolcontext.NewVerifier(config.ToolIssuer, config.ToolVerifyKeys, toolcontext.MaximumLifetime, routecontext.DefaultClockSkew, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	toolReceipts, err := postgres.NewToolContextReceiptRepository(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	toolAcceptor, err := toolcontext.NewAcceptor(toolVerifier, toolReceipts, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	workloadAuthorizer, err := access.NewWorkloadAuthorizer(accessRepository)
	if err != nil {
		pool.Close()
		return nil, err
	}
	tools, err := toolrouter.New(toolAcceptor, workloadAuthorizer, directory, signer, ids.RandomGenerator{}, logger, toolrouter.Config{Transport: config.CellTransport, AllowHTTPCells: config.AllowHTTPCells})
	if err != nil {
		pool.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/internal/v1/tools:invoke", tools.Handler())
	mux.Handle("/", transport.Handler())
	return &Server{Handler: withHealth(pool, directory, transport, mux), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }

func withHealth(pool *pgxpool.Pool, directory *accountdirectory.Cache, transport *routertransport.Server, next http.Handler) http.Handler {
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
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "account_directory_cache": directory.Stats(), "cell_transport": transport.TransportStats()})
			return
		}
		next.ServeHTTP(w, r)
	})
}
