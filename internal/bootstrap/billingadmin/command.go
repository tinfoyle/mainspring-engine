// Package billingadmin composes the short-lived billing operator command.
package billingadmin

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Actor, Reason, Environment, ConfirmEnvironment, Mode, TargetID string
	InspectLimit                                                                        int
	MaxDatabaseConns                                                                    int32
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := validateConfig(config, logger); err != nil {
		return err
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return err
	}
	if config.MaxDatabaseConns > 0 {
		poolConfig.MaxConns = config.MaxDatabaseConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	service, err := application.NewService(postgres.NewBillingAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	switch config.Action {
	case "inspect":
		records, batch, err := service.Inspect(ctx, config.InspectLimit, config.Actor, config.Reason, config.Environment, config.Mode)
		if err != nil {
			return err
		}
		for _, record := range records {
			logger.Info("Spyglass billing failure", "audit_batch_id", batch, "kind", record.Kind, "target_id", record.TargetID, "account_id", record.AccountID, "mode", record.Mode, "state", record.State, "attempt_count", record.AttemptCount, "last_error_code", record.LastErrorCode, "explanation_code", record.ExplanationCode, "explanation", record.Explanation)
		}
		logger.Info("Spyglass billing inspection complete", "audit_batch_id", batch, "mode", config.Mode, "count", len(records))
		return nil
	case "replay-event":
		record, batch, err := service.ReplayEvent(ctx, config.TargetID, config.Actor, config.Reason, config.Environment, config.Mode)
		if err != nil {
			return err
		}
		logger.Info("Spyglass billing event replay queued", "audit_batch_id", batch, "event_id", record.TargetID, "account_id", record.AccountID, "mode", record.Mode)
		return nil
	case "refresh-subscription":
		record, batch, err := service.QueueRefresh(ctx, config.TargetID, config.Actor, config.Reason, config.Environment, config.Mode)
		if err != nil {
			return err
		}
		logger.Info("Spyglass subscription refresh queued", "audit_batch_id", batch, "subscription_id", record.TargetID, "account_id", record.AccountID, "mode", record.Mode)
		return nil
	default:
		return errors.New("billing admin action must be inspect, replay-event, or refresh-subscription")
	}
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" || config.ConfirmEnvironment != config.Environment || (config.Mode != "test" && config.Mode != "live") || logger == nil {
		return application.ErrInvalidChange
	}
	switch config.Action {
	case "inspect":
		if config.TargetID != "" || config.InspectLimit < 1 || config.InspectLimit > application.MaximumInspectLimit {
			return application.ErrInvalidChange
		}
	case "replay-event":
		if !strings.HasPrefix(config.TargetID, "evt_") || config.InspectLimit != application.DefaultInspectLimit {
			return application.ErrInvalidChange
		}
	case "refresh-subscription":
		if !strings.HasPrefix(config.TargetID, "sub_") || config.InspectLimit != application.DefaultInspectLimit {
			return application.ErrInvalidChange
		}
	default:
		return errors.New("billing admin action must be inspect, replay-event, or refresh-subscription")
	}
	return nil
}
