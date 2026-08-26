// Package privacyrightsadmin composes the short-lived, reviewed privacy-rights
// fulfillment operator command.
package privacyrightsadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Actor, Reason, Environment, ConfirmEnvironment string
	RequestID                                                           ids.PrivacyRightsRequestID
	ExpectedVersion                                                     uint64
	ResolutionState                                                     privacy.RightsState
	Evidence                                                            application.ResolutionEvidence
	DueBefore                                                           time.Time
	Limit                                                               int
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
	service, err := application.New(postgres.NewPrivacyRightsAdminRepository(pool), ids.RandomGenerator{})
	if err != nil {
		return err
	}
	var request privacy.RightsRequest
	switch config.Action {
	case "list-open":
		var items []application.QueueItem
		items, err = service.ListOpen(ctx, config.DueBefore, config.Limit, config.Actor, config.Reason, config.Environment)
		if err == nil {
			for _, item := range items {
				logger.Info("Spyglass open privacy rights request", "request_id", item.RequestID,
					"request_version", item.Version, "kind", item.Kind, "scope", item.Scope, "state", item.State,
					"requested_at", item.RequestedAt, "response_due_at", item.ResponseDueAt, "updated_at", item.UpdatedAt,
					"environment", config.Environment)
			}
			logger.Info("Spyglass privacy rights deadline queue listed", "result_count", len(items),
				"due_before", config.DueBefore, "limit", config.Limit, "environment", config.Environment, "actor", config.Actor)
			return nil
		}
	case "inspect":
		request, err = service.Inspect(ctx, config.RequestID, config.Actor, config.Reason, config.Environment)
	case "start-review":
		request, err = service.StartReview(ctx, config.RequestID, config.ExpectedVersion, config.Actor, config.Reason, config.Environment)
	case "resolve":
		request, err = service.Resolve(ctx, config.RequestID, config.ExpectedVersion, config.ResolutionState, config.Evidence, config.Actor, config.Reason, config.Environment)
	default:
		return fmt.Errorf("unsupported privacy rights operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	attributes := []any{"action", config.Action, "request_id", request.ID, "request_version", request.Version,
		"kind", request.Kind, "scope", request.Scope, "state", request.State, "response_due_at", request.ResponseDueAt,
		"environment", config.Environment, "actor", config.Actor}
	if config.Action == "resolve" {
		attributes = append(attributes, "evidence_id", config.Evidence.ID, "evidence_sha256", fmt.Sprintf("%x", config.Evidence.SHA256))
	}
	logger.Info("Spyglass privacy rights operator action complete", attributes...)
	return nil
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" ||
		config.ConfirmEnvironment != config.Environment || logger == nil {
		return application.ErrInvalidChange
	}
	switch config.Action {
	case "list-open":
		if config.RequestID != "" || config.ExpectedVersion != 0 || config.ResolutionState != "" || config.Evidence.ID != "" ||
			config.DueBefore.IsZero() || config.Limit < 1 || config.Limit > 100 {
			return application.ErrInvalidChange
		}
	case "inspect":
		if ids.Validate(string(config.RequestID)) != nil || config.ExpectedVersion != 0 || config.ResolutionState != "" || config.Evidence.ID != "" || !config.DueBefore.IsZero() || config.Limit != 0 {
			return application.ErrInvalidChange
		}
	case "start-review":
		if ids.Validate(string(config.RequestID)) != nil || config.ExpectedVersion == 0 || config.ResolutionState != "" || config.Evidence.ID != "" || !config.DueBefore.IsZero() || config.Limit != 0 {
			return application.ErrInvalidChange
		}
	case "resolve":
		if ids.Validate(string(config.RequestID)) != nil || config.ExpectedVersion == 0 || ids.Validate(config.Evidence.ID) != nil || !config.DueBefore.IsZero() || config.Limit != 0 ||
			(config.ResolutionState != privacy.RightsCompleted && config.ResolutionState != privacy.RightsPartiallyCompleted && config.ResolutionState != privacy.RightsDeclined) {
			return application.ErrInvalidChange
		}
	default:
		return errors.New("privacy rights admin action must be list-open, inspect, start-review, or resolve")
	}
	return nil
}
