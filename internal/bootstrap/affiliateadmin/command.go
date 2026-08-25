// Package affiliateadmin composes the short-lived, reviewed Affiliate
// enrollment operator command.
package affiliateadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Actor, Reason, Environment, ConfirmEnvironment string
	AffiliateID                                                         ids.AffiliateID
	ExpectedVersion                                                     uint64
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
	service, err := application.New(postgres.NewAffiliateAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	var enrollment affiliates.Enrollment
	switch config.Action {
	case "inspect":
		enrollment, err = service.Inspect(ctx, config.AffiliateID, config.Actor, config.Reason, config.Environment)
	case "activate", "suspend", "close":
		state := map[string]affiliates.EnrollmentState{"activate": affiliates.EnrollmentActive,
			"suspend": affiliates.EnrollmentSuspended, "close": affiliates.EnrollmentClosed}[config.Action]
		enrollment, err = service.Transition(ctx, config.AffiliateID, config.ExpectedVersion, state,
			config.Actor, config.Reason, config.Environment)
	default:
		return fmt.Errorf("unsupported Affiliate operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	logger.Info("Spyglass Affiliate operator action complete", "action", config.Action, "affiliate_id", enrollment.ID,
		"enrollment_version", enrollment.Version, "state", enrollment.State, "environment", config.Environment, "actor", config.Actor)
	return nil
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" ||
		config.ConfirmEnvironment != config.Environment || ids.Validate(string(config.AffiliateID)) != nil || logger == nil {
		return application.ErrInvalidChange
	}
	switch config.Action {
	case "inspect":
		if config.ExpectedVersion != 0 {
			return application.ErrInvalidChange
		}
	case "activate", "suspend", "close":
		if config.ExpectedVersion == 0 {
			return application.ErrInvalidChange
		}
	default:
		return errors.New("Affiliate admin action must be inspect, activate, suspend, or close")
	}
	return nil
}
