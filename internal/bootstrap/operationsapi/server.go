// Package operationsapi composes the isolated staff Operations Console API.
package operationsapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
	transport "github.com/tinfoyle/spyglass-engine/internal/transport/operationsapi"
)

type Config struct {
	Environment           string
	IdentityDatabaseURL   string
	ProjectionDatabaseURL string
	Origin                string
	PasskeyRPID           string
	PasskeyEncryptionKeys map[int][]byte
	PasskeyActiveVersion  int
	NetworkActorKey       []byte
	TrustedProxyCIDRs     []string
	MaxDatabaseConns      int32
	MaxRequestBody        int64
	SecureCookie          bool
}

type Server struct {
	Handler        http.Handler
	identityPool   *pgxpool.Pool
	projectionPool *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	config.Environment = strings.TrimSpace(config.Environment)
	config.Origin = strings.TrimSuffix(strings.TrimSpace(config.Origin), "/")
	if config.Environment == "" || config.IdentityDatabaseURL == "" || config.ProjectionDatabaseURL == "" ||
		config.Origin == "" || config.PasskeyRPID == "" || logger == nil {
		return nil, errors.New("operations API database, environment, origin, passkey and logger configuration is required")
	}
	identityConfig, err := pgxpool.ParseConfig(config.IdentityDatabaseURL)
	if err != nil {
		return nil, err
	}
	projectionConfig, err := pgxpool.ParseConfig(config.ProjectionDatabaseURL)
	if err != nil {
		return nil, err
	}
	if identityConfig.ConnConfig.User == projectionConfig.ConnConfig.User {
		return nil, errors.New("operations identity and projection database credentials must use different roles")
	}
	if config.MaxDatabaseConns > 0 {
		identityConfig.MaxConns = config.MaxDatabaseConns
		projectionConfig.MaxConns = config.MaxDatabaseConns
	}
	identityPool, err := pgxpool.NewWithConfig(ctx, identityConfig)
	if err != nil {
		return nil, err
	}
	closeIdentity := true
	defer func() {
		if closeIdentity {
			identityPool.Close()
		}
	}()
	if err := identityPool.Ping(ctx); err != nil {
		return nil, err
	}
	projectionPool, err := pgxpool.NewWithConfig(ctx, projectionConfig)
	if err != nil {
		return nil, err
	}
	closeProjection := true
	defer func() {
		if closeProjection {
			projectionPool.Close()
		}
	}()
	if err := projectionPool.Ping(ctx); err != nil {
		return nil, err
	}

	clock := registration.SystemClock{}
	sessionService, err := sessions.NewService(postgres.NewOperationsSessionRepository(identityPool), ids.RandomGenerator{}, clock, 8*time.Hour, 30*time.Minute, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	passkeyCipher, err := passkeys.NewCipherKeyring(config.PasskeyEncryptionKeys, config.PasskeyActiveVersion)
	if err != nil {
		return nil, err
	}
	passkeyRepository, err := postgres.NewPasskeyRepository(identityPool, passkeyCipher)
	if err != nil {
		return nil, err
	}
	networkGuard, err := abuse.NewGuard(postgres.NewNetworkRateLimiter(identityPool))
	if err != nil {
		return nil, err
	}
	passkeyService, err := passkeys.NewService(passkeyRepository, sessionService, networkGuard, ids.RandomGenerator{}, clock,
		passkeys.Config{RelyingPartyID: config.PasskeyRPID, Origins: []string{config.Origin}})
	if err != nil {
		return nil, err
	}
	consoleService, err := operationsconsole.New(postgres.NewOperationsConsoleRepository(projectionPool), ids.RandomGenerator{}, clock, config.Environment)
	if err != nil {
		return nil, err
	}
	actorResolver, err := networkactor.New(config.NetworkActorKey, config.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	api, err := transport.New(consoleService, passkeyService, sessionService, transport.Cookie{Secure: config.SecureCookie}, config.Origin, config.MaxRequestBody, logger)
	if err != nil {
		return nil, err
	}
	closeIdentity, closeProjection = false, false
	return &Server{Handler: actorResolver.Handler(api.Handler()), identityPool: identityPool, projectionPool: projectionPool}, nil
}

func (server *Server) Close() {
	if server == nil {
		return
	}
	if server.identityPool != nil {
		server.identityPool.Close()
	}
	if server.projectionPool != nil {
		server.projectionPool.Close()
	}
}
