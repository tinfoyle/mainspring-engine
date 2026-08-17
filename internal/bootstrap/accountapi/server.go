package accountapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
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
	DatabaseURL         string
	StripeWebhookSecret string
	StripeMode          string
	MaxDatabaseConns    int32
	AppOrigin           string
	PublicOrigin        string
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

type NotificationSender interface {
	registration.VerificationSender
	invitations.Sender
}

// New constructs the persistent account-api mode. Verification delivery is an
// explicit required adapter; this composition never falls back to logging or
// returning verification credentials.
func New(ctx context.Context, config Config, sender NotificationSender, logger *slog.Logger) (*Server, error) {
	if config.DatabaseURL == "" || config.AppOrigin == "" || config.PublicOrigin == "" || sender == nil || logger == nil {
		return nil, errors.New("database URL, application/public origins, notification sender, and logger are required")
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
	apiHandler := httpapi.NewServer(registrations, func() catalog.PublishedCatalog { return publishedCatalog }, nil, false, logger,
		httpapi.WithBillingWebhook(webhook),
		httpapi.WithAuthentication(authenticationService, sessionService, httpapi.SessionCookie{Secure: true}),
		httpapi.WithAccountAccess(accountAccess),
		httpapi.WithInvitations(invitationService, nil, false),
	).Handler()
	browser, err := browserapp.New(registrations, authenticationService, sessionService, accountAccess, invitationService, func() catalog.PublishedCatalog { return publishedCatalog }, nil, nil, browserapp.Config{SecureCookies: true, TrustedOrigins: []string{config.AppOrigin, config.PublicOrigin}}, logger)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Server{Handler: browser.Handler(apiHandler), pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }
