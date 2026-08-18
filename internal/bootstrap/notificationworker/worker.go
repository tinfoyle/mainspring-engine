package notificationworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/smtp"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type Config struct {
	DatabaseURL, SMTPAddress, SMTPServerName, SMTPUsername, SMTPPassword, SMTPFromAddress, SMTPFromName, AppOrigin string
	NotificationEncryptionKey                                                                                      []byte
	MaxDatabaseConns                                                                                               int32
	PollInterval                                                                                                   time.Duration
}

type Worker struct {
	pool      *pgxpool.Pool
	processor interface {
		ProcessOne(context.Context) (bool, error)
	}
	poll   time.Duration
	logger *slog.Logger
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
	delivery, err := smtp.New(smtp.Config{Address: config.SMTPAddress, ServerName: config.SMTPServerName, Username: config.SMTPUsername, Password: config.SMTPPassword, FromAddress: config.SMTPFromAddress, FromName: config.SMTPFromName, AppOrigin: config.AppOrigin})
	if err != nil {
		pool.Close()
		return nil, err
	}
	processor, err := notifications.NewProcessor(postgres.NewNotificationOutbox(pool), envelopeCipher, delivery, registration.SystemClock{}, 2*time.Minute)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Worker{pool: pool, processor: processor, poll: config.PollInterval, logger: logger}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, err := w.processor.ProcessOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.logger.Error("notification delivery failed", "error", err)
		}
		if worked {
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
func (w *Worker) Close()                          { w.pool.Close() }
