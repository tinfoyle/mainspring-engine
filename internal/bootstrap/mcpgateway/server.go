package mcpgateway

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
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	transport "github.com/tinfoyle/spyglass-engine/internal/transport/mcpgateway"
)

type Config struct {
	DatabaseURL          string
	MaxDatabaseConns     int32
	RouteIssuer          string
	RouteSigningKeyID    string
	RouteSigningKey      []byte
	RouteLifetime        time.Duration
	DirectoryCacheTTL    time.Duration
	DirectoryCapacity    int
	CellTransport        http.RoundTripper
	AllowHTTPCells       bool
	TrustedOrigins       []string
	ResourceURL          string
	ResourceMetadataURL  string
	AuthorizationServers []string
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

// New composes the global half of production MCP. It receives only a narrowly
// privileged global database credential and signed cell-routing authority.
func New(ctx context.Context, config Config, logger *slog.Logger, clock routecontext.Clock) (*Server, error) {
	if config.DatabaseURL == "" || config.RouteIssuer == "" || config.RouteSigningKeyID == "" || len(config.RouteSigningKey) < routecontext.MinimumKeyBytes || logger == nil || clock == nil || len(config.AuthorizationServers) != 1 {
		return nil, errors.New("MCP gateway bootstrap configuration is required")
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
	tokenService, err := mcpauth.New(postgres.NewMCPAuthRepository(pool), ids.RandomGenerator{}, mcpauth.RandomSecrets{}, clock, config.AuthorizationServers[0], config.ResourceURL)
	if err != nil {
		pool.Close()
		return nil, err
	}
	authenticator := tokenAuthenticator{service: tokenService}
	signer, err := routecontext.NewSigner(config.RouteIssuer, config.RouteSigningKeyID, config.RouteSigningKey, config.RouteLifetime, clock)
	if err != nil {
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
	directory, err := accountdirectory.NewCache(postgres.NewAccountDirectoryRepository(pool), clock, accountdirectory.Config{TTL: config.DirectoryCacheTTL, Capacity: config.DirectoryCapacity, AllowHTTP: config.AllowHTTPCells})
	if err != nil {
		pool.Close()
		return nil, err
	}
	gateway, err := transport.New(authenticator, authorizer, directory, signer, ids.RandomGenerator{}, logger, transport.Config{TrustedOrigins: config.TrustedOrigins, ResourceURL: config.ResourceURL, ResourceMetadataURL: config.ResourceMetadataURL, AuthorizationServers: config.AuthorizationServers, Transport: config.CellTransport})
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Server{Handler: withHealth(pool, gateway.Handler()), pool: pool}, nil
}

// tokenAuthenticator is composition glue: it translates the transport's
// bearer-token requirement into the OAuth application's transport-neutral
// requirement without coupling either layer to the other.
type tokenAuthenticator struct{ service *mcpauth.Service }

func (a tokenAuthenticator) Authenticate(ctx context.Context, token string, requirement transport.TokenRequirement) (access.Actor, error) {
	return a.service.Authenticate(ctx, token, mcpauth.TokenRequirement{Audience: requirement.Audience, Scope: requirement.Scope})
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
		if r.Method == http.MethodGet && r.URL.Path == "/health/status" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
