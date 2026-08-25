// Package affiliatesupportadmin composes the short-lived, reviewed Affiliate
// support operator command.
package affiliatesupportadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupportadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Actor, Reason, Environment, ConfirmEnvironment string
	RequestID                                                           ids.AffiliateSupportRequestID
	ExpectedVersion                                                     uint64
	Outcome                                                             affiliates.SupportOutcome
	MaxDatabaseConns                                                    int32
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
	service, err := application.New(postgres.NewAffiliateSupportAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	var request affiliates.SupportRequest
	switch config.Action {
	case "inspect":
		request, err = service.Inspect(ctx, config.RequestID, config.Actor, config.Reason, config.Environment)
	case "start-review":
		request, err = service.StartReview(ctx, config.RequestID, config.ExpectedVersion, config.Actor, config.Reason, config.Environment)
	case "resolve":
		request, err = service.Resolve(ctx, config.RequestID, config.ExpectedVersion, config.Outcome, config.Actor, config.Reason, config.Environment)
	default:
		return fmt.Errorf("unsupported Affiliate support operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	logger.Info("Spyglass Affiliate support operator action complete", "action", config.Action, "request_id", request.ID,
		"request_version", request.Version, "state", request.State, "outcome", request.Outcome,
		"environment", config.Environment, "actor", config.Actor)
	return nil
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" ||
		config.ConfirmEnvironment != config.Environment || ids.Validate(string(config.RequestID)) != nil || logger == nil {
		return application.ErrInvalidChange
	}
	switch config.Action {
	case "inspect":
		if config.ExpectedVersion != 0 || config.Outcome != "" {
			return application.ErrInvalidChange
		}
	case "start-review":
		if config.ExpectedVersion == 0 || config.Outcome != "" {
			return application.ErrInvalidChange
		}
	case "resolve":
		if config.ExpectedVersion == 0 || (config.Outcome != affiliates.SupportApproved && config.Outcome != affiliates.SupportDenied) {
			return application.ErrInvalidChange
		}
	default:
		return errors.New("Affiliate support admin action must be inspect, start-review, or resolve")
	}
	return nil
}
