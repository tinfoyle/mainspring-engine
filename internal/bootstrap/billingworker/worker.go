package billingworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	stripeadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesettlement"
	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, StripeSecretKey, StripeAPIVersion, StripeMode string
	MaxDatabaseConns                                           int32
	PollInterval                                               time.Duration
}

type Worker struct {
	pool                                            *pgxpool.Pool
	processor                                       eventProcessor
	reconciler                                      reconciliationProcessor
	terminator                                      reconciliationProcessor
	poll                                            time.Duration
	logger                                          *slog.Logger
	events, reconciliations, terminations, failures atomic.Uint64
}

type Status struct {
	EventsProcessed          uint64 `json:"events_processed"`
	ReconciliationsProcessed uint64 `json:"reconciliations_processed"`
	TerminationsProcessed    uint64 `json:"terminations_processed"`
	Failures                 uint64 `json:"failures"`
}

type eventProcessor interface {
	ProcessOne(context.Context) (bool, error)
}
type reconciliationProcessor interface {
	ProcessOne(context.Context) (bool, error)
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("billing worker database URL and logger are required")
	}
	if config.StripeMode != "test" && config.StripeMode != "live" {
		return nil, errors.New("Stripe mode must be test or live")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
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
	provider, err := stripeadapter.New(config.StripeSecretKey, config.StripeAPIVersion, nil)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if provider.Mode() != config.StripeMode {
		pool.Close()
		return nil, errors.New("Stripe secret key mode does not match configured mode")
	}
	clock := registration.SystemClock{}
	repository := postgres.NewBillingProjectionRepository(pool)
	projector, err := billing.NewProjector(provider, repository, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	publishedCatalog, err := postgres.NewCatalogRepository(pool).Published(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	tokenIssuer, err := aitokenledger.NewIssuer(postgres.NewAITokenLedgerRepository(pool), func() catalog.PublishedCatalog { return publishedCatalog }, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	tokenProjector, err := aitokenledger.NewBillingEventProjector(tokenIssuer)
	if err != nil {
		pool.Close()
		return nil, err
	}
	purchaseProjector, err := commercialaccess.NewBillingEventProjector(postgres.NewCommercialAccessRepository(pool), tokenIssuer)
	if err != nil {
		pool.Close()
		return nil, err
	}
	affiliateService, err := affiliateprogram.New(postgres.NewAffiliateProgramRepository(pool), ids.RandomGenerator{}, affiliateprogram.RandomCodeGenerator{}, clock, 1, 1)
	if err != nil {
		pool.Close()
		return nil, err
	}
	affiliateProjector, err := affiliateprogram.NewBillingEventProjector(affiliateService)
	if err != nil {
		pool.Close()
		return nil, err
	}
	settlementService, err := affiliatesettlement.New(postgres.NewAffiliateSettlementRepository(pool), provider, ids.RandomGenerator{}, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	settlementProjector, err := affiliatesettlement.NewBillingEventProjector(settlementService)
	if err != nil {
		pool.Close()
		return nil, err
	}
	processor, err := billing.NewProcessor(postgres.NewBillingInbox(pool), billing.SequenceHandler{projector, tokenProjector, purchaseProjector, affiliateProjector, settlementProjector}, clock, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	reconciler, err := billing.NewReconciler(repository, projector, clock, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	terminator, err := subscriptionlifecycle.NewTerminationProcessor(postgres.NewSubscriptionLifecycleRepository(pool), provider, clock, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, reconciler: reconciler, terminator: terminator, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, eventErr := w.processor.ProcessOne(ctx)
		reconciled, reconcileErr := w.reconciler.ProcessOne(ctx)
		terminated, terminationErr := false, error(nil)
		if w.terminator != nil {
			terminated, terminationErr = w.terminator.ProcessOne(ctx)
		}
		if ctx.Err() != nil {
			return nil
		}
		if eventErr != nil {
			w.failures.Add(1)
			w.logger.Error("billing event processing failed", "error", eventErr)
		}
		if reconcileErr != nil {
			w.failures.Add(1)
			w.logger.Error("billing reconciliation failed", "error", reconcileErr)
		}
		if terminationErr != nil {
			w.failures.Add(1)
			w.logger.Error("subscription termination failed", "error", terminationErr)
		}
		if worked {
			w.events.Add(1)
		}
		if reconciled {
			w.reconciliations.Add(1)
		}
		if terminated {
			w.terminations.Add(1)
		}
		if worked || reconciled || terminated {
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) Ready(ctx context.Context) error { return w.pool.Ping(ctx) }
func (w *Worker) Status(context.Context) (any, error) {
	return Status{EventsProcessed: w.events.Load(), ReconciliationsProcessed: w.reconciliations.Load(), TerminationsProcessed: w.terminations.Load(), Failures: w.failures.Load()}, nil
}
func (w *Worker) Close() { w.pool.Close() }
