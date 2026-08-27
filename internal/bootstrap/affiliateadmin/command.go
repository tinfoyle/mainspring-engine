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
	CustomerSessionID                                                   ids.SessionID
	CheckReservationID                                                  string
	ExpectedVersion                                                     uint64
	ExpectedPolicyVersion, NewPolicyVersion                             uint64
	CheckThresholdMinor, CheckAmountMinor                               int64
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
	case "inspect-risk":
		var risk application.RiskSummary
		risk, err = service.InspectRisk(ctx, config.AffiliateID, config.Actor, config.Reason, config.Environment)
		if err == nil {
			flags := risk.Flags()
			flagNames := make([]string, len(flags))
			for index := range flags {
				flagNames[index] = string(flags[index])
			}
			logger.Info("Spyglass Affiliate risk inspection complete", "action", config.Action,
				"affiliate_id", risk.AffiliateID, "enrollment_version", risk.EnrollmentVersion,
				"state", risk.EnrollmentState, "environment", config.Environment, "actor", config.Actor,
				"observed_at", risk.ObservedAt, "reservation_window_started_at", risk.ReservationWindowStartedAt,
				"valid_reservations", risk.ValidReservations, "distinct_referred_accounts", risk.DistinctReferredAccounts,
				"repeated_referred_accounts", risk.RepeatedReferredAccounts,
				"maximum_reservations_per_account", risk.MaximumReservationsPerAccount,
				"cross_affiliate_code_cycle_accounts", risk.CrossAffiliateCodeCycleAccounts,
				"locked_attributions", risk.LockedAttributions,
				"largest_account_share_basis_points", risk.LargestAccountShareBasisPoints,
				"code_replacement_window_started_at", risk.CodeReplacementWindowStartedAt,
				"code_replacements", risk.CodeReplacements, "risk_flags", flagNames)
		}
	case "activate", "suspend", "close":
		state := map[string]affiliates.EnrollmentState{"activate": affiliates.EnrollmentActive,
			"suspend": affiliates.EnrollmentSuspended, "close": affiliates.EnrollmentClosed}[config.Action]
		enrollment, err = service.Transition(ctx, config.AffiliateID, config.ExpectedVersion, state,
			config.Actor, config.Reason, config.Environment)
	case "set-check-threshold":
		var policy application.SettlementPolicy
		policy, err = service.PublishSettlementPolicy(ctx, config.ExpectedPolicyVersion, config.NewPolicyVersion,
			config.CheckThresholdMinor, config.Actor, config.Reason, config.Environment)
		if err == nil {
			logger.Info("Spyglass Affiliate settlement policy published", "action", config.Action,
				"policy_version", policy.Version, "mode", policy.Mode, "currency", policy.Currency,
				"check_threshold_minor", policy.CheckThresholdMinor, "effective_from", policy.EffectiveFrom,
				"environment", config.Environment, "actor", config.Actor)
		}
	case "reserve-check":
		var reservation application.CheckReservation
		reservation, err = service.ReserveSupportCheck(ctx, config.AffiliateID, config.CustomerSessionID,
			config.CheckAmountMinor, config.Actor, config.Reason, config.Environment)
		if err == nil {
			logCheckReservation(logger, config, reservation)
		}
	case "settle-check", "release-check":
		state := map[string]string{"settle-check": "settled", "release-check": "released"}[config.Action]
		var reservation application.CheckReservation
		reservation, err = service.TransitionSupportCheck(ctx, config.CheckReservationID, config.ExpectedVersion,
			state, config.Actor, config.Reason, config.Environment)
		if err == nil {
			logCheckReservation(logger, config, reservation)
		}
	default:
		return fmt.Errorf("unsupported Affiliate operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	if config.Action == "inspect-risk" || config.Action == "set-check-threshold" ||
		config.Action == "reserve-check" || config.Action == "settle-check" || config.Action == "release-check" {
		return nil
	}
	logger.Info("Spyglass Affiliate operator action complete", "action", config.Action, "affiliate_id", enrollment.ID,
		"enrollment_version", enrollment.Version, "state", enrollment.State, "environment", config.Environment, "actor", config.Actor)
	return nil
}

func logCheckReservation(logger *slog.Logger, config Config, reservation application.CheckReservation) {
	logger.Info("Spyglass Affiliate Support check accounting updated", "action", config.Action,
		"reservation_id", reservation.ID, "affiliate_id", reservation.AffiliateID, "state", reservation.State,
		"amount_minor", reservation.AmountMinor, "currency", reservation.Currency,
		"policy_version", reservation.PolicyVersion, "version", reservation.Version,
		"environment", config.Environment, "actor", config.Actor)
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" ||
		config.ConfirmEnvironment != config.Environment || logger == nil {
		return application.ErrInvalidChange
	}
	switch config.Action {
	case "inspect", "inspect-risk":
		if ids.Validate(string(config.AffiliateID)) != nil || config.ExpectedVersion != 0 {
			return application.ErrInvalidChange
		}
	case "activate", "suspend", "close":
		if ids.Validate(string(config.AffiliateID)) != nil || config.ExpectedVersion == 0 {
			return application.ErrInvalidChange
		}
	case "set-check-threshold":
		if config.AffiliateID != "" || config.ExpectedPolicyVersion == 0 || config.NewPolicyVersion != config.ExpectedPolicyVersion+1 || config.CheckThresholdMinor <= 0 {
			return application.ErrInvalidChange
		}
	case "reserve-check":
		if ids.Validate(string(config.AffiliateID)) != nil || ids.Validate(string(config.CustomerSessionID)) != nil ||
			config.CheckAmountMinor <= 0 || config.ExpectedVersion != 0 || config.CheckReservationID != "" {
			return application.ErrInvalidChange
		}
	case "settle-check", "release-check":
		if config.AffiliateID != "" || config.CustomerSessionID != "" || ids.Validate(config.CheckReservationID) != nil ||
			config.ExpectedVersion == 0 || config.CheckAmountMinor != 0 {
			return application.ErrInvalidChange
		}
	default:
		return errors.New("Affiliate admin action must be inspect, inspect-risk, activate, suspend, close, set-check-threshold, reserve-check, settle-check, or release-check")
	}
	return nil
}
