package admissionapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	admissiontransport "github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
)

type Config struct {
	DatabaseURL                string
	RouteIssuer                string
	RouteVerifyKeys            map[string][]byte
	CellIDs                    []ids.CellID
	MaxDatabaseConns           int32
	MaxRequestBody             int64
	AgentExecutionPoliciesJSON string
	CatalogRefreshInterval     time.Duration
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
	stop    context.CancelFunc
	done    chan struct{}
}

func New(ctx context.Context, config Config, logger *slog.Logger, clock routecontext.Clock) (*Server, error) {
	if config.DatabaseURL == "" || config.RouteIssuer == "" || len(config.RouteVerifyKeys) == 0 || len(config.CellIDs) == 0 || config.AgentExecutionPoliciesJSON == "" || logger == nil || clock == nil {
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
	catalogRepository := postgres.NewCatalogRepository(pool)
	publishedCatalog, err := catalogRepository.Published(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	privateCatalog, err := catalog.ApplyAIExecutionPolicies(publishedCatalog, config.AgentExecutionPoliciesJSON)
	if err != nil {
		pool.Close()
		return nil, err
	}
	catalogCache, err := catalog.NewCache(privateCatalog)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if config.CatalogRefreshInterval <= 0 {
		config.CatalogRefreshInterval = 5 * time.Second
	}
	tokens, err := aitokenledger.New(postgres.NewAITokenLedgerRepository(pool), authorizer, catalogCache.Current, ids.RandomGenerator{}, registration.SystemClock{})
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
		admissiontransport.WithAgentExecutionAuthorizer(authorizer),
		admissiontransport.WithAITokens(tokens))
	if err != nil {
		pool.Close()
		return nil, err
	}
	refreshContext, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go refreshPrivateCatalog(refreshContext, config.CatalogRefreshInterval, catalogRepository, catalogCache, config.AgentExecutionPoliciesJSON, logger, done)
	return &Server{Handler: withHealth(pool, transport.Handler()), pool: pool, stop: stop, done: done}, nil
}

func (s *Server) Close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
	s.pool.Close()
}

func refreshPrivateCatalog(ctx context.Context, interval time.Duration, repository *postgres.CatalogRepository, cache *catalog.Cache, policies string, logger *slog.Logger, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryContext, cancel := context.WithTimeout(ctx, 3*time.Second)
			published, err := repository.Published(queryContext)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					logger.Error("refresh AI Token Catalog", "error", err)
				}
				continue
			}
			current := cache.Current()
			if current.Version == published.Version && current.PublishedAt.Equal(published.PublishedAt) {
				continue
			}
			next, err := catalog.ApplyAIExecutionPolicies(published, policies)
			if err != nil {
				logger.Error("reject AI Token Catalog refresh", "error", err)
				continue
			}
			if err := cache.Replace(next); err != nil {
				logger.Error("reject invalid AI Token Catalog refresh", "error", err)
				continue
			}
			logger.Info("AI Token Catalog refreshed", "catalog_version", next.Version, "published_at", next.PublishedAt)
		}
	}
}

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
