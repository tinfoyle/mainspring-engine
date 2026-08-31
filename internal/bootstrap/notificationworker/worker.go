package notificationworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/smswebhook"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/smtp"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, SMTPAddress, SMTPServerName, SMTPUsername, SMTPPassword, SMTPFromAddress, SMTPFromName, AppOrigin string
	Environment, SMSGatewayURL, SMSGatewayBearerToken, SMSFrom                                                     string
	SMTPRootCAFile                                                                                                 string
	NotificationEncryptionKey                                                                                      []byte
	MaxDatabaseConns                                                                                               int32
	PollInterval                                                                                                   time.Duration
}

type Worker struct {
	pool      *pgxpool.Pool
	processor interface {
		ProcessOne(context.Context) (bool, error)
	}
	subscriptionProcessor interface {
		ProcessOne(context.Context) (bool, error)
	}
	poll                                       time.Duration
	logger                                     *slog.Logger
	processed, subscriptionProcessed, failures atomic.Uint64
}

type Status struct {
	Processed             uint64 `json:"processed"`
	SubscriptionProcessed uint64 `json:"subscription_processed"`
	Failures              uint64 `json:"failures"`
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.DatabaseURL == "" || logger == nil {
		return nil, errors.New("notification worker database URL and logger are required")
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
	envelopeCipher, err := notifications.NewCipher(config.NotificationEncryptionKey, 1)
	if err != nil {
		pool.Close()
		return nil, err
	}
	var smsDelivery *smswebhook.Sender
	if config.Environment == "local" || config.Environment == "local-secure" || config.Environment == "development" {
		smsDelivery, err = smswebhook.New(smswebhook.Config{DevelopmentDiscard: true})
	} else {
		smsDelivery, err = smswebhook.New(smswebhook.Config{URL: config.SMSGatewayURL, BearerToken: config.SMSGatewayBearerToken, From: config.SMSFrom})
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	delivery, err := smtp.New(smtp.Config{Address: config.SMTPAddress, ServerName: config.SMTPServerName, Username: config.SMTPUsername, Password: config.SMTPPassword, FromAddress: config.SMTPFromAddress, FromName: config.SMTPFromName, AppOrigin: config.AppOrigin, RootCAFile: config.SMTPRootCAFile, SMS: smsDelivery})
	if err != nil {
		pool.Close()
		return nil, err
	}
	outbox := postgres.NewNotificationOutbox(pool)
	processor, err := notifications.NewProcessor(outbox, envelopeCipher, delivery, registration.SystemClock{}, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	preparer, err := notifications.NewQueuedSender(outbox, envelopeCipher, ids.RandomGenerator{}, registration.SystemClock{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	subscriptionProcessor, err := subscriptionlifecycle.NewNoticeProcessor(postgres.NewSubscriptionLifecycleRepository(pool), preparer, ids.RandomGenerator{}, registration.SystemClock{}, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, subscriptionProcessor: subscriptionProcessor, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, err := w.processor.ProcessOne(ctx)
		subscriptionWorked, subscriptionErr := false, error(nil)
		if w.subscriptionProcessor != nil {
			subscriptionWorked, subscriptionErr = w.subscriptionProcessor.ProcessOne(ctx)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.failures.Add(1)
			w.logger.Error("notification delivery failed", "error", err)
		}
		if subscriptionErr != nil {
			w.failures.Add(1)
			w.logger.Error("subscription lifecycle notice processing failed", "error", subscriptionErr)
		}
		if worked {
			w.processed.Add(1)
		}
		if subscriptionWorked {
			w.subscriptionProcessed.Add(1)
		}
		if worked || subscriptionWorked {
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
	return Status{Processed: w.processed.Load(), SubscriptionProcessed: w.subscriptionProcessed.Load(), Failures: w.failures.Load()}, nil
}
func (w *Worker) Close() { w.pool.Close() }
