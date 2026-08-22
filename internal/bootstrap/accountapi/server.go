package accountapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	stripeadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
	"github.com/tinfoyle/spyglass-engine/internal/transport/browserapp"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
	"github.com/tinfoyle/spyglass-engine/internal/transport/mcpoauth"
)

type Config struct {
	DatabaseURL               string
	StripeWebhookSecret       string
	StripeSecretKey           string
	StripeAPIVersion          string
	StripeMode                string
	StripeHTTPClient          *http.Client
	MaxDatabaseConns          int32
	AppOrigin                 string
	PublicOrigin              string
	MCPResourceOrigin         string
	NotificationEncryptionKey []byte
	NetworkActorKey           []byte
	PasskeyEncryptionKeys     map[int][]byte
	PasskeyActiveKeyVersion   int
	PasskeyRPID               string
	TrustedProxyCIDRs         []string
	CatalogRefreshInterval    time.Duration
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
	stop    context.CancelFunc
	done    <-chan struct{}
}

// New constructs the persistent account-api mode. Identity messages are
// encrypted and durably queued; this process never connects to SMTP.
func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	if config.DatabaseURL == "" || config.AppOrigin == "" || config.PublicOrigin == "" || config.MCPResourceOrigin == "" || logger == nil {
		return nil, errors.New("database URL, application/public origins, and logger are required")
	}
	if config.StripeMode != "test" && config.StripeMode != "live" {
		return nil, errors.New("Stripe mode must be test or live")
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
	notificationCipher, err := notifications.NewCipher(config.NotificationEncryptionKey, 1)
	if err != nil {
		pool.Close()
		return nil, err
	}
	sender, err := notifications.NewQueuedSender(postgres.NewNotificationOutbox(pool), notificationCipher, ids.RandomGenerator{}, registration.SystemClock{})
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
	catalogCache, err := catalog.NewCache(publishedCatalog)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if config.CatalogRefreshInterval <= 0 {
		config.CatalogRefreshInterval = 5 * time.Second
	}
	registrationRepository := postgres.NewRegistrationRepository(pool)
	clock := registration.SystemClock{}
	networkGuard, err := abuse.NewGuard(postgres.NewNetworkRateLimiter(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	actorResolver, err := networkactor.New(config.NetworkActorKey, config.TrustedProxyCIDRs)
	if err != nil {
		pool.Close()
		return nil, err
	}
	registrations := registration.NewService(registrationRepository, sender, registrationRepository, catalogCache.Current, ids.RandomGenerator{}, clock, authn.Passwords{})
	passwords := authn.Passwords{}
	sessionService, err := sessions.NewService(postgres.NewSessionRepository(pool), ids.RandomGenerator{}, clock, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	dummyHash, err := passwords.Hash("production timing equalization material")
	if err != nil {
		pool.Close()
		return nil, err
	}
	authenticationRepository := postgres.NewAuthenticationRepository(pool)
	authenticationService, err := authentication.NewService(authenticationRepository, authenticationRepository, networkGuard, passwords, sessionService, clock, dummyHash)
	if err != nil {
		pool.Close()
		return nil, err
	}
	passkeyCipher, err := passkeys.NewCipherKeyring(config.PasskeyEncryptionKeys, config.PasskeyActiveKeyVersion)
	if err != nil {
		pool.Close()
		return nil, err
	}
	passkeyRepository, err := postgres.NewPasskeyRepository(pool, passkeyCipher)
	if err != nil {
		pool.Close()
		return nil, err
	}
	recoveryCodeRepository := postgres.NewRecoveryCodeRepository(pool)
	recoveryCodeService, err := recoverycodes.NewService(recoveryCodeRepository, ids.RandomGenerator{}, recoverycodes.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	securityPosture, err := securityposture.NewService(postgres.NewSecurityPostureRepository(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	passkeyService, err := passkeys.NewService(passkeyRepository, sessionService, networkGuard, ids.RandomGenerator{}, clock, passkeys.Config{RelyingPartyID: config.PasskeyRPID, Origins: []string{config.AppOrigin}, RecoveryPolicy: recoveryCodeService})
	if err != nil {
		pool.Close()
		return nil, err
	}
	recoveryService, err := recovery.NewService(postgres.NewRecoveryRepository(pool), sender, authenticationRepository, networkGuard, passwords, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	contactChangeService, err := contactchange.NewService(postgres.NewContactChangeRepository(pool), sender, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	authorizer, err := access.NewAuthorizer(postgres.NewAccessRepository(pool), access.WithOwnerSecurityPolicy(securityPosture))
	if err != nil {
		pool.Close()
		return nil, err
	}
	accountAccess, err := accountaccess.NewService(postgres.NewAccountAccessRepository(pool), authorizer, securityPosture)
	if err != nil {
		pool.Close()
		return nil, err
	}
	accountLifecycle, err := accountlifecycle.NewService(postgres.NewAccountLifecycleRepository(pool), authorizer, ids.RandomGenerator{}, clock, 7*24*time.Hour)
	if err != nil {
		pool.Close()
		return nil, err
	}
	memberService, err := accountmembers.NewService(postgres.NewAccountMemberRepositoryWithOwnershipNotifications(pool, sender), authorizer, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	invitationService, err := invitations.NewService(postgres.NewInvitationRepository(pool), sender, authorizer, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}

	verifier, err := billing.NewSignatureVerifier(config.StripeWebhookSecret, 5*time.Minute, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	webhook, err := billing.NewWebhookService(verifier, postgres.NewBillingInbox(pool), config.StripeMode, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	stripeProvider, err := stripeadapter.New(config.StripeSecretKey, config.StripeAPIVersion, config.StripeHTTPClient)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if stripeProvider.Mode() != config.StripeMode {
		pool.Close()
		return nil, errors.New("Stripe secret key mode does not match configured mode")
	}
	commercialService, err := commercialaccess.New(stripeProvider, postgres.NewCommercialAccessRepository(pool), authorizer, catalogCache.Current, clock, config.AppOrigin, config.StripeMode)
	if err != nil {
		pool.Close()
		return nil, err
	}
	apiHandler := httpapi.NewServer(registrations, catalogCache.Current, nil, false, logger,
		httpapi.WithBillingWebhook(webhook),
		httpapi.WithCommercialAccess(commercialService, config.AppOrigin),
		httpapi.WithAuthentication(authenticationService, sessionService, httpapi.SessionCookie{Secure: true, Origin: config.AppOrigin}),
		httpapi.WithAccountAccess(accountAccess),
		httpapi.WithAccountLifecycle(accountLifecycle),
		httpapi.WithAccountMembers(memberService),
		httpapi.WithInvitations(invitationService, nil, false),
		httpapi.WithRecovery(recoveryService, nil, false),
		httpapi.WithPasskeys(passkeyService),
		httpapi.WithRecoveryCodes(recoveryCodeService),
		httpapi.WithSecurityPosture(securityPosture),
		httpapi.WithContactChanges(contactChangeService, nil, false),
	).Handler()
	browser, err := browserapp.New(registrations, authenticationService, sessionService, accountAccess, invitationService, catalogCache.Current, nil, nil, browserapp.Config{SecureCookies: true, TrustedOrigins: []string{config.AppOrigin}}, logger, browserapp.WithCommercialAccess(commercialService), browserapp.WithAccountLifecycle(accountLifecycle), browserapp.WithAccountMembers(memberService), browserapp.WithRecovery(recoveryService, nil), browserapp.WithPasskeys(passkeyService), browserapp.WithRecoveryCodes(recoveryCodeService), browserapp.WithContactChanges(contactChangeService, nil))
	if err != nil {
		pool.Close()
		return nil, err
	}
	mcpAuthorization, err := mcpauth.New(postgres.NewMCPAuthRepository(pool), ids.RandomGenerator{}, mcpauth.RandomSecrets{}, clock, config.AppOrigin, config.MCPResourceOrigin)
	if err != nil {
		pool.Close()
		return nil, err
	}
	clientMetadata, err := mcpoauth.NewHTTPMetadataLoader(net.DefaultResolver)
	if err != nil {
		pool.Close()
		return nil, err
	}
	oauth, err := mcpoauth.New(mcpAuthorization, sessionService, clientMetadata, mcpoauth.Config{Issuer: config.AppOrigin, Resource: config.MCPResourceOrigin, TrustedOrigin: config.AppOrigin, SecureCookies: true}, logger)
	if err != nil {
		pool.Close()
		return nil, err
	}
	refreshContext, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go refreshCatalog(refreshContext, config.CatalogRefreshInterval, catalogRepository, catalogCache, logger, done)
	return &Server{Handler: withReadiness(pool, actorResolver.Handler(oauth.Handler(browser.Handler(apiHandler)))), pool: pool, stop: stop, done: done}, nil
}

func (s *Server) Close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
	s.pool.Close()
}

func refreshCatalog(ctx context.Context, interval time.Duration, repository *postgres.CatalogRepository, cache *catalog.Cache, logger *slog.Logger, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryContext, cancel := context.WithTimeout(ctx, 3*time.Second)
			next, err := repository.Published(queryContext)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					logger.Error("refresh published Catalog", "error", err)
				}
				continue
			}
			current := cache.Current()
			if current.Version == next.Version && current.PublishedAt.Equal(next.PublishedAt) {
				continue
			}
			if err := cache.Replace(next); err != nil {
				logger.Error("reject invalid published Catalog refresh", "error", err)
				continue
			}
			logger.Info("published Catalog refreshed", "catalog_version", next.Version, "published_at", next.PublishedAt)
		}
	}
}

func withReadiness(pool *pgxpool.Pool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
