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
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/exportcapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	transport "github.com/tinfoyle/spyglass-engine/internal/transport/mcpgateway"
)

type Config struct {
	DatabaseURL            string
	MaxDatabaseConns       int32
	RouteIssuer            string
	RouteSigningKeyID      string
	RouteSigningKey        []byte
	RouteLifetime          time.Duration
	DirectoryCacheTTL      time.Duration
	DirectoryCapacity      int
	CellTransport          http.RoundTripper
	AllowHTTPCells         bool
	TrustedOrigins         []string
	ResourceURL            string
	ResourceMetadataURL    string
	AuthorizationServers   []string
	AppOrigin              string
	ExportDownloadKeyID    string
	ExportDownloadKeys     map[string][]byte
	ExportDownloadLifetime time.Duration
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
	exportRepository := postgres.NewAccountExportRepository(pool)
	exportService, err := accountexport.NewService(exportRepository, authorizer, ids.RandomGenerator{}, clock, 7*24*time.Hour)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if config.ExportDownloadLifetime == 0 {
		config.ExportDownloadLifetime = exportcapability.DefaultLifetime
	}
	activeExportKey, ok := config.ExportDownloadKeys[config.ExportDownloadKeyID]
	if !ok {
		pool.Close()
		return nil, errors.New("active Account export download key is absent from MCP gateway")
	}
	exportSigner, err := exportcapability.NewSigner(config.AppOrigin, config.ExportDownloadKeyID, activeExportKey, config.ExportDownloadLifetime, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	exportCapabilities, err := accountexport.NewCapabilityIssuer(exportRepository, authorizer, exportSigner, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	directory, err := accountdirectory.NewCache(postgres.NewAccountDirectoryRepository(pool), clock, accountdirectory.Config{TTL: config.DirectoryCacheTTL, Capacity: config.DirectoryCapacity, AllowHTTP: config.AllowHTTPCells})
	if err != nil {
		pool.Close()
		return nil, err
	}
	gateway, err := transport.New(authenticator, authorizer, directory, signer, ids.RandomGenerator{}, logger, transport.Config{TrustedOrigins: config.TrustedOrigins, ResourceURL: config.ResourceURL, ResourceMetadataURL: config.ResourceMetadataURL, AuthorizationServers: config.AuthorizationServers, Transport: config.CellTransport, AccountExports: exportService, ExportCapabilities: exportCapabilities, AppOrigin: config.AppOrigin})
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

func (a tokenAuthenticator) Authenticate(ctx context.Context, token string, requirement transport.TokenRequirement) (transport.Principal, error) {
	authority, err := a.service.Authenticate(ctx, token, mcpauth.TokenRequirement{Audience: requirement.Audience, Scope: requirement.Scope})
	if err != nil {
		return transport.Principal{}, err
	}
	return transport.Principal{Actor: authority.Actor, StrongAuthenticatedAt: authority.StrongAuthenticatedAt}, nil
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
