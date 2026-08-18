package runnerbrokerapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/kubernetes"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	brokertransport "github.com/tinfoyle/spyglass-engine/internal/transport/runnerbrokerapi"
)

type Config struct {
	CellDatabaseURL, BrokerAudience, Namespace, RunnerServiceAccount string
	EncryptionKeys                                                   map[int][]byte
	ActiveKeyVersion                                                 int
	MaxDatabaseConns                                                 int32
	MaxRequestBody                                                   int64
	IdentityVerifier                                                 runnerbroker.IdentityVerifier
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	if config.CellDatabaseURL == "" || config.BrokerAudience == "" || config.Namespace == "" || config.RunnerServiceAccount == "" || logger == nil {
		return nil, errors.New("runner broker configuration is required")
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
	return &Server{Handler: withHealth(pool, transport.Handler()), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }

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
		next.ServeHTTP(w, r)
	})
}
