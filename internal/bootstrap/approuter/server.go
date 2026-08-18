package approuter

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	routertransport "github.com/tinfoyle/spyglass-engine/internal/transport/approuter"
)

type Config struct {
	DatabaseURL       string
	MaxDatabaseConns  int32
	RouteIssuer       string
	RouteSigningKeyID string
	RouteSigningKey   []byte
	RouteLifetime     time.Duration
	CellRoutes        map[ids.CellID]string
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
	if config.DatabaseURL == "" || config.RouteIssuer == "" || config.RouteSigningKeyID == "" || len(config.RouteSigningKey) < routecontext.MinimumKeyBytes || len(config.CellRoutes) == 0 || logger == nil || clock == nil {
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
	authorizer, err := access.NewAuthorizer(postgres.NewAccessRepository(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	transport, err := routertransport.New(sessionService, authorizer, signer, ids.RandomGenerator{}, routertransport.Config{SessionCookieName: config.SessionCookieName, SecureCookies: config.SecureCookies, TrustedOrigins: config.TrustedOrigins, CellRoutes: config.CellRoutes, AllowHTTPCells: config.AllowHTTPCells}, logger)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Server{Handler: withHealth(pool, transport.Handler()), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }

func withHealth(pool *pgxpool.Pool, next http.Handler) http.Handler {
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
		next.ServeHTTP(w, r)
	})
}
