package admissionapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	admissiontransport "github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
)

type Config struct {
	DatabaseURL      string
	RouteIssuer      string
	RouteVerifyKeys  map[string][]byte
	CellIDs          []ids.CellID
	MaxDatabaseConns int32
	MaxRequestBody   int64
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger, clock routecontext.Clock) (*Server, error) {
	if config.DatabaseURL == "" || config.RouteIssuer == "" || len(config.RouteVerifyKeys) == 0 || len(config.CellIDs) == 0 || logger == nil || clock == nil {
		return nil, errors.New("admission API configuration is required")
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
	securityPosture, err := securityposture.NewService(postgres.NewSecurityPostureRepository(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	authorizer, err := access.NewAuthorizer(postgres.NewAccessRepository(pool), access.WithOwnerSecurityPolicy(securityPosture))
	if err != nil {
		pool.Close()
		return nil, err
	}
	usage, err := usageadmission.NewService(authorizer, postgres.NewUsageAdmissionRepository(pool), ids.RandomGenerator{}, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	verifiers := make(map[ids.CellID]admissiontransport.Verifier, len(config.CellIDs))
	for _, cellID := range config.CellIDs {
		if !routecontext.ValidCellID(cellID) {
			pool.Close()
			return nil, errors.New("admission API cell ID is invalid")
		}
		if _, exists := verifiers[cellID]; exists {
			pool.Close()
			return nil, errors.New("admission API cell IDs must be unique")
		}
		verifier, err := routecontext.NewVerifier(config.RouteIssuer, routecontext.Audience(cellID), config.RouteVerifyKeys, routecontext.MaximumLifetime, routecontext.DefaultClockSkew, clock)
		if err != nil {
			pool.Close()
			return nil, err
		}
		verifiers[cellID] = verifier
	}
	maxBody := config.MaxRequestBody
	if maxBody == 0 {
		maxBody = admissiontransport.DefaultMaxBody
	}
	transport, err := admissiontransport.New(usage, verifiers, logger, maxBody,
		admissiontransport.WithReviewerDirectory(postgres.NewAttentionReviewerDirectory(pool)),
		admissiontransport.WithAgentExecutionAuthorizer(authorizer))
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
