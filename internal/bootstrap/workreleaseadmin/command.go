// Package workreleaseadmin wires the short-lived audited Work release
// operator command. It is not a serving process.
package workreleaseadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL        string
	Action             string
	Actor              string
	Reason             string
	Environment        string
	ConfirmEnvironment string
	InspectLimit       int
	Target             application.Target
	MaxDatabaseConns   int32
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
	service, err := application.NewService(postgres.NewWorkReleaseAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	switch config.Action {
	case "inspect":
		inspection, err := service.Inspect(ctx, config.InspectLimit, config.Actor, config.Reason, config.Environment)
		if err != nil {
			return err
		}
		for _, record := range inspection.DeadLetters {
			logger.Info("Spyglass Work release dead letter", "audit_batch_id", inspection.AuditBatchID, "account_id", record.AccountID, "work_item_id", record.WorkItemID, "reservation_id", record.ReservationID, "attempt_count", record.AttemptCount, "last_error_code", record.LastErrorCode, "queued_at", record.QueuedAt, "last_attempt_at", record.LastAttemptAt)
		}
		logger.Info("Spyglass Work release inspection complete", "audit_batch_id", inspection.AuditBatchID, "environment", config.Environment, "actor", config.Actor, "count", len(inspection.DeadLetters))
		return nil
	case "requeue":
		result, err := service.Requeue(ctx, config.Target, config.Actor, config.Reason, config.Environment)
		if err != nil {
			return err
		}
		record := result.DeadLetter
		logger.Info("Spyglass Work release requeued", "audit_batch_id", result.AuditBatchID, "environment", config.Environment, "actor", config.Actor, "account_id", record.AccountID, "work_item_id", record.WorkItemID, "reservation_id", record.ReservationID, "previous_attempt_count", record.AttemptCount, "previous_error_code", record.LastErrorCode, "next_attempt_at", record.NextAttemptAt)
		return nil
	default:
		return fmt.Errorf("unsupported Work release operator action %q", config.Action)
	}
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Action == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" || logger == nil {
		return errors.New("Work release operator database, action, actor, reason, environment, and logger are required")
	}
	if config.ConfirmEnvironment == "" || config.ConfirmEnvironment != config.Environment {
		return errors.New("Work release operator environment confirmation must exactly match the target environment")
	}
	if config.Action != "inspect" && config.Action != "requeue" {
		return fmt.Errorf("unsupported Work release operator action %q", config.Action)
	}
	if config.Action == "requeue" && !config.Target.Valid() {
		return application.ErrInvalidChange
	}
	return nil
}
