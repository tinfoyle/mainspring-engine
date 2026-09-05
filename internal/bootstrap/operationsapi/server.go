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

	"github.com/tinfoyle/spyglass-engine/internal/adapters/accesslogs"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
	transport "github.com/tinfoyle/spyglass-engine/internal/transport/operationsapi"
)

type Config struct {
	Environment           string
	IdentityDatabaseURL   string
	ProjectionDatabaseURL string
	BillingDatabaseURL    string
	PrivacyDatabaseURL    string
	AffiliateDatabaseURL  string
	Origin                string
	AppOrigin             string
	PasskeyRPID           string
	PasskeyEncryptionKeys map[int][]byte
	PasskeyActiveVersion  int
	NetworkActorKey       []byte
	TrustedProxyCIDRs     []string
	MaxDatabaseConns      int32
	MaxRequestBody        int64
	SecureCookie          bool
	TrafficLogDirectory   string
	TrafficHosts          []string
}

type Server struct {
	Handler        http.Handler
	identityPool   *pgxpool.Pool
	projectionPool *pgxpool.Pool
	billingPool    *pgxpool.Pool
	privacyPool    *pgxpool.Pool
	affiliatePool  *pgxpool.Pool
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	config.Environment = strings.TrimSpace(config.Environment)
	config.Origin = strings.TrimSuffix(strings.TrimSpace(config.Origin), "/")
	if config.Environment == "" || config.IdentityDatabaseURL == "" || config.ProjectionDatabaseURL == "" || config.BillingDatabaseURL == "" || config.PrivacyDatabaseURL == "" || config.AffiliateDatabaseURL == "" ||
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
	billingConfig, err := pgxpool.ParseConfig(config.BillingDatabaseURL)
	if err != nil {
		return nil, err
	}
	privacyConfig, err := pgxpool.ParseConfig(config.PrivacyDatabaseURL)
	if err != nil {
		return nil, err
	}
	affiliateConfig, err := pgxpool.ParseConfig(config.AffiliateDatabaseURL)
	if err != nil {
		return nil, err
	}
	roles := map[string]bool{}
	for _, name := range []string{identityConfig.ConnConfig.User, projectionConfig.ConnConfig.User, billingConfig.ConnConfig.User, privacyConfig.ConnConfig.User, affiliateConfig.ConnConfig.User} {
		if name == "" || roles[name] {
			return nil, errors.New("operations database credentials must use five distinct roles")
		}
		roles[name] = true
	}
	if config.MaxDatabaseConns > 0 {
		identityConfig.MaxConns = config.MaxDatabaseConns
		projectionConfig.MaxConns = config.MaxDatabaseConns
		billingConfig.MaxConns = config.MaxDatabaseConns
		privacyConfig.MaxConns = config.MaxDatabaseConns
		affiliateConfig.MaxConns = config.MaxDatabaseConns
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
	billingPool, err := pgxpool.NewWithConfig(ctx, billingConfig)
	if err != nil {
		return nil, err
	}
	closeBilling := true
	defer func() {
		if closeBilling {
			billingPool.Close()
		}
	}()
	if err := billingPool.Ping(ctx); err != nil {
		return nil, err
	}
	privacyPool, err := pgxpool.NewWithConfig(ctx, privacyConfig)
	if err != nil {
		return nil, err
	}
	closePrivacy := true
	defer func() {
		if closePrivacy {
			privacyPool.Close()
		}
	}()
	if err := privacyPool.Ping(ctx); err != nil {
		return nil, err
	}
	affiliatePool, err := pgxpool.NewWithConfig(ctx, affiliateConfig)
	if err != nil {
		return nil, err
	}
	closeAffiliate := true
	defer func() {
		if closeAffiliate {
			affiliatePool.Close()
		}
	}()
	if err := affiliatePool.Ping(ctx); err != nil {
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
	billingService, err := billingadmin.NewService(postgres.NewBillingAdminRepository(billingPool), ids.RandomGenerator{})
	if err != nil {
		return nil, err
	}
	privacyService, err := privacyrightsadmin.New(postgres.NewPrivacyRightsAdminRepository(privacyPool), ids.RandomGenerator{})
	if err != nil {
		return nil, err
	}
	affiliateService, err := affiliateadmin.New(postgres.NewAffiliateAdminRepository(affiliatePool), ids.RandomGenerator{})
	if err != nil {
		return nil, err
	}
	var trafficService *trafficreport.Service
	if config.TrafficLogDirectory != "" {
		reader, err := accesslogs.New(config.TrafficLogDirectory, config.TrafficHosts)
		if err != nil {
			return nil, err
		}
		trafficService, err = trafficreport.New(reader, postgres.NewOperationsTrafficAuthorizer(projectionPool, config.Environment))
		if err != nil {
			return nil, err
		}
	}
	operatorServices := transport.OperatorServices{Billing: billingService, Privacy: privacyService, Affiliate: affiliateService}
	if trafficService != nil {
		operatorServices.Traffic = trafficService
	}
	api, err := transport.New(consoleService, passkeyService, sessionService, operatorServices,
		transport.Cookie{Secure: config.SecureCookie}, config.Origin, config.Environment, config.MaxRequestBody, logger)
	if err != nil {
		return nil, err
	}
	adminCipher, err := operationsauth.NewCipher(config.PasskeyEncryptionKeys, config.PasskeyActiveVersion)
	if err != nil {
		return nil, err
	}
	adminAuth, err := operationsauth.New(postgres.NewOperationsAuthenticationRepository(identityPool), adminCipher, sessionService, clock, config.Origin)
	if err != nil {
		return nil, err
	}
	if err = api.WithAuthenticator(adminAuth, networkGuard, config.AppOrigin); err != nil {
		return nil, err
	}
	closeIdentity, closeProjection, closeBilling, closePrivacy, closeAffiliate = false, false, false, false, false
	return &Server{Handler: actorResolver.Handler(api.Handler()), identityPool: identityPool, projectionPool: projectionPool,
		billingPool: billingPool, privacyPool: privacyPool, affiliatePool: affiliatePool}, nil
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
	if server.billingPool != nil {
		server.billingPool.Close()
	}
	if server.privacyPool != nil {
		server.privacyPool.Close()
	}
	if server.affiliatePool != nil {
		server.affiliatePool.Close()
	}
}
