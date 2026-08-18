package accountapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	stripeadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/browserapp"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

type Config struct {
	DatabaseURL               string
	StripeWebhookSecret       string
	StripeSecretKey           string
	StripeAPIVersion          string
	StripeMode                string
	MaxDatabaseConns          int32
	AppOrigin                 string
	PublicOrigin              string
	NotificationEncryptionKey []byte
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

// New constructs the persistent account-api mode. Identity messages are
// encrypted and durably queued; this process never connects to SMTP.
func New(ctx context.Context, config Config, logger *slog.Logger) (*Server, error) {
	if config.DatabaseURL == "" || config.AppOrigin == "" || config.PublicOrigin == "" || logger == nil {
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
	registrationRepository := postgres.NewRegistrationRepository(pool)
	clock := registration.SystemClock{}
	registrations := registration.NewService(registrationRepository, sender, registrationRepository, publishedCatalog, ids.RandomGenerator{}, clock, authn.Passwords{})
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
	authenticationService, err := authentication.NewService(authenticationRepository, authenticationRepository, passwords, sessionService, clock, dummyHash)
	if err != nil {
		pool.Close()
		return nil, err
	}
	recoveryService, err := recovery.NewService(postgres.NewRecoveryRepository(pool), sender, authenticationRepository, passwords, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	authorizer, err := access.NewAuthorizer(postgres.NewAccessRepository(pool))
	if err != nil {
		pool.Close()
		return nil, err
	}
	accountAccess, err := accountaccess.NewService(postgres.NewAccountAccessRepository(pool), authorizer)
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
	stripeProvider, err := stripeadapter.New(config.StripeSecretKey, config.StripeAPIVersion, nil)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if stripeProvider.Mode() != config.StripeMode {
		pool.Close()
		return nil, errors.New("Stripe secret key mode does not match configured mode")
	}
	commercialService, err := commercialaccess.New(stripeProvider, postgres.NewCommercialAccessRepository(pool), authorizer, func() catalog.PublishedCatalog { return publishedCatalog }, clock, config.AppOrigin, config.StripeMode)
	if err != nil {
		pool.Close()
		return nil, err
	}
	apiHandler := httpapi.NewServer(registrations, func() catalog.PublishedCatalog { return publishedCatalog }, nil, false, logger,
		httpapi.WithBillingWebhook(webhook),
		httpapi.WithCommercialAccess(commercialService, config.AppOrigin),
		httpapi.WithAuthentication(authenticationService, sessionService, httpapi.SessionCookie{Secure: true, Origin: config.AppOrigin}),
		httpapi.WithAccountAccess(accountAccess),
		httpapi.WithInvitations(invitationService, nil, false),
		httpapi.WithRecovery(recoveryService, nil, false),
	).Handler()
	browser, err := browserapp.New(registrations, authenticationService, sessionService, accountAccess, invitationService, func() catalog.PublishedCatalog { return publishedCatalog }, nil, nil, browserapp.Config{SecureCookies: true, TrustedOrigins: []string{config.AppOrigin, config.PublicOrigin}}, logger, browserapp.WithCommercialAccess(commercialService), browserapp.WithRecovery(recoveryService, nil))
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Server{Handler: withReadiness(pool, browser.Handler(apiHandler)), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }

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
