package accountapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

type Config struct {
	DatabaseURL         string
	StripeWebhookSecret string
	StripeMode          string
	MaxDatabaseConns    int32
}

type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
}

// New constructs the persistent account-api mode. Verification delivery is an
// explicit required adapter; this composition never falls back to logging or
// returning verification credentials.
func New(ctx context.Context, config Config, sender registration.VerificationSender, logger *slog.Logger) (*Server, error) {
	if config.DatabaseURL == "" || sender == nil || logger == nil {
		return nil, errors.New("database URL, verification sender, and logger are required")
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
	registrations := registration.NewService(registrationRepository, sender, registrationRepository, publishedCatalog, ids.RandomGenerator{}, clock)

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
	handler := httpapi.NewServer(registrations, func() catalog.PublishedCatalog { return publishedCatalog }, nil, false, logger, httpapi.WithBillingWebhook(webhook)).Handler()
	return &Server{Handler: handler, pool: pool}, nil
}

func (s *Server) Close() { s.pool.Close() }
