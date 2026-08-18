// Package agentqueueadmin wires the short-lived audited Agent queue operator
// command. It is not a serving process.
package agentqueueadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/agentqueueadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Queue, Actor, Reason, Environment, ConfirmEnvironment string
	InspectLimit                                                               int
	Target                                                                     application.Target
	MaxDatabaseConns                                                           int32
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
	service, err := application.NewService(postgres.NewAgentQueueAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	switch config.Action {
	case "inspect":
		inspection, err := service.Inspect(ctx, config.Queue, config.InspectLimit, config.Actor, config.Reason, config.Environment)
		if err != nil {
			return err
		}
		for _, record := range inspection.DeadLetters {
			logger.Info("Spyglass Agent queue dead letter", "audit_batch_id", inspection.AuditBatchID, "queue", record.Queue, "account_id", record.AccountID, "invocation_id", record.InvocationID, "attempt_count", record.AttemptCount, "last_error_code", record.LastErrorCode, "created_at", record.CreatedAt, "updated_at", record.UpdatedAt)
		}
		logger.Info("Spyglass Agent queue inspection complete", "audit_batch_id", inspection.AuditBatchID, "queue", config.Queue, "environment", config.Environment, "actor", config.Actor, "count", len(inspection.DeadLetters))
		return nil
	case "requeue":
		result, err := service.Requeue(ctx, config.Target, config.Actor, config.Reason, config.Environment)
		if err != nil {
			return err
		}
		record := result.DeadLetter
		logger.Info("Spyglass Agent queue dead letter requeued", "audit_batch_id", result.AuditBatchID, "queue", record.Queue, "environment", config.Environment, "actor", config.Actor, "account_id", record.AccountID, "invocation_id", record.InvocationID, "previous_attempt_count", record.AttemptCount, "previous_error_code", record.LastErrorCode, "next_attempt_at", record.NextAttemptAt)
		return nil
	default:
		return fmt.Errorf("unsupported Agent queue operator action %q", config.Action)
	}
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Action == "" || config.Queue == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" || logger == nil {
		return errors.New("Agent queue operator database, action, queue, actor, reason, environment, and logger are required")
	}
	if config.ConfirmEnvironment == "" || config.ConfirmEnvironment != config.Environment {
		return errors.New("Agent queue operator environment confirmation must exactly match the target environment")
	}
	if config.Action != "inspect" && config.Action != "requeue" {
		return fmt.Errorf("unsupported Agent queue operator action %q", config.Action)
	}
	if config.Action == "requeue" && !config.Target.Valid() {
		return application.ErrInvalidChange
	}
	return nil
}
