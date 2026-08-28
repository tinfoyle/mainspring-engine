// Package affiliateadmin owns the audited operator boundary for Affiliate
// enrollment inspection, suspension, reactivation, and terminal closure.
package affiliateadmin

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidChange = errors.New("Affiliate operator change is invalid")
	ErrNotFound      = errors.New("Affiliate enrollment was not found")
	ErrStateConflict = errors.New("Affiliate enrollment state changed")
	ErrStrongAuth    = errors.New("recent Affiliate passkey confirmation is required")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Change struct {
	EventID     ids.AffiliateEnrollmentEventID
	Actor       string
	Reason      string
	Environment string
}

type RiskFlag string

const (
	RiskRepeatedCheckoutCreation  RiskFlag = "repeated_checkout_creation"
	RiskCrossAffiliateCodeCycling RiskFlag = "cross_affiliate_code_cycling"
	RiskReferralConcentration     RiskFlag = "referral_concentration"
	RiskRapidCodeReplacement      RiskFlag = "rapid_code_replacement"
)

// RiskSummary contains only aggregate, content-free evidence for a human
// review. It deliberately contains no referred Account, User, public-code, or
// provider identifiers and grants no authority to change enrollment state.
type RiskSummary struct {
	AffiliateID                     ids.AffiliateID            `json:"affiliate_id"`
	EnrollmentState                 affiliates.EnrollmentState `json:"enrollment_state"`
	EnrollmentVersion               uint64                     `json:"enrollment_version"`
	ObservedAt                      time.Time                  `json:"observed_at"`
	ReservationWindowStartedAt      time.Time                  `json:"reservation_window_started_at"`
	ValidReservations               uint64                     `json:"valid_reservations"`
	DistinctReferredAccounts        uint64                     `json:"distinct_referred_accounts"`
	RepeatedReferredAccounts        uint64                     `json:"repeated_referred_accounts"`
	MaximumReservationsPerAccount   uint64                     `json:"maximum_reservations_per_account"`
	CrossAffiliateCodeCycleAccounts uint64                     `json:"cross_affiliate_code_cycle_accounts"`
	LockedAttributions              uint64                     `json:"locked_attributions"`
	LargestAccountShareBasisPoints  uint64                     `json:"largest_account_share_basis_points"`
	CodeReplacementWindowStartedAt  time.Time                  `json:"code_replacement_window_started_at"`
	CodeReplacements                uint64                     `json:"code_replacements"`
}

func (r RiskSummary) Validate() error {
	validState := r.EnrollmentState == affiliates.EnrollmentActive || r.EnrollmentState == affiliates.EnrollmentSuspended || r.EnrollmentState == affiliates.EnrollmentClosed
	if ids.Validate(string(r.AffiliateID)) != nil || !validState || r.EnrollmentVersion == 0 || r.ObservedAt.IsZero() ||
		r.ReservationWindowStartedAt.IsZero() || !r.ReservationWindowStartedAt.Before(r.ObservedAt) ||
		r.CodeReplacementWindowStartedAt.IsZero() || !r.CodeReplacementWindowStartedAt.Before(r.ObservedAt) ||
		r.DistinctReferredAccounts > r.ValidReservations || r.RepeatedReferredAccounts > r.DistinctReferredAccounts ||
		r.CrossAffiliateCodeCycleAccounts > r.DistinctReferredAccounts || r.LockedAttributions > r.ValidReservations ||
		r.MaximumReservationsPerAccount > r.ValidReservations || r.LargestAccountShareBasisPoints > 10000 {
		return ErrInvalidChange
	}
	if r.ValidReservations == 0 && (r.DistinctReferredAccounts != 0 || r.RepeatedReferredAccounts != 0 ||
		r.MaximumReservationsPerAccount != 0 || r.CrossAffiliateCodeCycleAccounts != 0 ||
		r.LockedAttributions != 0 || r.LargestAccountShareBasisPoints != 0) {
		return ErrInvalidChange
	}
	return nil
}

// Flags applies review thresholds in application code so they remain visible,
// testable policy rather than hidden enforcement in the database. Flags are
// signals for a human reviewer, never automatic suspension decisions.
func (r RiskSummary) Flags() []RiskFlag {
	flags := make([]RiskFlag, 0, 4)
	if r.MaximumReservationsPerAccount >= 3 {
		flags = append(flags, RiskRepeatedCheckoutCreation)
	}
	if r.CrossAffiliateCodeCycleAccounts > 0 {
		flags = append(flags, RiskCrossAffiliateCodeCycling)
	}
	if r.ValidReservations >= 10 && r.LargestAccountShareBasisPoints >= 5000 {
		flags = append(flags, RiskReferralConcentration)
	}
	if r.CodeReplacements >= 3 {
		flags = append(flags, RiskRapidCodeReplacement)
	}
	slices.Sort(flags)
	return flags
}

type Store interface {
	Inspect(context.Context, ids.AffiliateID, Change) (affiliates.Enrollment, error)
	InspectRisk(context.Context, ids.AffiliateID, Change) (RiskSummary, error)
	Transition(context.Context, ids.AffiliateID, uint64, affiliates.EnrollmentState, Change) (affiliates.Enrollment, error)
	PublishSettlementPolicy(context.Context, uint64, uint64, int64, Change) (SettlementPolicy, error)
	ReserveSupportCheck(context.Context, ids.AffiliateID, ids.SessionID, int64, string, Change) (CheckReservation, error)
	TransitionSupportCheck(context.Context, string, uint64, string, Change) (CheckReservation, error)
	SetRetentionHold(context.Context, ids.AffiliateID, uint64, bool, Change) (RetentionControl, error)
	RestrictRetention(context.Context, ids.AffiliateID, uint64, Change) (RetentionControl, error)
}

type SettlementPolicy struct {
	Version             uint64
	Mode                string
	Currency            string
	CheckThresholdMinor int64
	EffectiveFrom       time.Time
}

type CheckReservation struct {
	ID            string
	AffiliateID   ids.AffiliateID
	State         string
	AmountMinor   int64
	Currency      string
	PolicyVersion uint64
	Version       uint64
	CreatedAt     time.Time
}

// RetentionControl is the version-fenced legal-hold state for one Affiliate.
// It deliberately exposes no retained Affiliate identity beyond the operator's
// exact target and contains no case material.
type RetentionControl struct {
	AffiliateID  ids.AffiliateID
	LegalHold    bool
	RestrictedAt *time.Time
	Version      uint64
	UpdatedAt    time.Time
}

func (r RetentionControl) Validate() error {
	if ids.Validate(string(r.AffiliateID)) != nil || r.Version == 0 || r.UpdatedAt.IsZero() ||
		(r.RestrictedAt != nil && (r.RestrictedAt.IsZero() || r.RestrictedAt.After(r.UpdatedAt))) {
		return ErrInvalidChange
	}
	return nil
}

func (r CheckReservation) Validate() error {
	if ids.Validate(r.ID) != nil || ids.Validate(string(r.AffiliateID)) != nil ||
		(r.State != "reserved" && r.State != "settled" && r.State != "released") ||
		r.AmountMinor <= 0 || r.AmountMinor > 99_999_999 || r.Currency != "USD" ||
		r.PolicyVersion == 0 || r.Version == 0 || r.CreatedAt.IsZero() {
		return ErrInvalidChange
	}
	return nil
}

func (p SettlementPolicy) Validate() error {
	if p.Version == 0 || p.Mode != "account_credit_with_support_check" || p.Currency != "USD" || p.CheckThresholdMinor <= 0 || p.EffectiveFrom.IsZero() {
		return ErrInvalidChange
	}
	return nil
}

type Service struct {
	store Store
	ids   ids.Generator
}

func New(store Store, generator ids.Generator) (*Service, error) {
	if store == nil || generator == nil {
		return nil, ErrInvalidChange
	}
	return &Service{store: store, ids: generator}, nil
}

func (s *Service) Inspect(ctx context.Context, affiliateID ids.AffiliateID, actor, reason, environment string) (affiliates.Enrollment, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil {
		return affiliates.Enrollment{}, ErrInvalidChange
	}
	return s.store.Inspect(ctx, affiliateID, change)
}

func (s *Service) InspectRisk(ctx context.Context, affiliateID ids.AffiliateID, actor, reason, environment string) (RiskSummary, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil {
		return RiskSummary{}, ErrInvalidChange
	}
	value, err := s.store.InspectRisk(ctx, affiliateID, change)
	if err != nil {
		return RiskSummary{}, err
	}
	if err := value.Validate(); err != nil {
		return RiskSummary{}, err
	}
	return value, nil
}

func (s *Service) Transition(ctx context.Context, affiliateID ids.AffiliateID, expectedVersion uint64, state affiliates.EnrollmentState, actor, reason, environment string) (affiliates.Enrollment, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil || expectedVersion == 0 ||
		(state != affiliates.EnrollmentActive && state != affiliates.EnrollmentSuspended && state != affiliates.EnrollmentClosed) {
		return affiliates.Enrollment{}, ErrInvalidChange
	}
	return s.store.Transition(ctx, affiliateID, expectedVersion, state, change)
}

func (s *Service) PublishSettlementPolicy(ctx context.Context, expectedVersion, newVersion uint64, checkThresholdMinor int64, actor, reason, environment string) (SettlementPolicy, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || expectedVersion == 0 || newVersion != expectedVersion+1 || checkThresholdMinor <= 0 || checkThresholdMinor > 99_999_999 {
		return SettlementPolicy{}, ErrInvalidChange
	}
	value, err := s.store.PublishSettlementPolicy(ctx, expectedVersion, newVersion, checkThresholdMinor, change)
	if err != nil {
		return SettlementPolicy{}, err
	}
	if err := value.Validate(); err != nil {
		return SettlementPolicy{}, err
	}
	return value, nil
}

// ReserveSupportCheck atomically removes an operator-selected amount from the
// Affiliate's available balance. The store re-verifies that the supplied
// session belongs to this Affiliate and contains recent passkey evidence.
// This records accounting authority only; it does not select or issue a check.
func (s *Service) ReserveSupportCheck(ctx context.Context, affiliateID ids.AffiliateID, sessionID ids.SessionID, amountMinor int64, actor, reason, environment string) (CheckReservation, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil || ids.Validate(string(sessionID)) != nil || amountMinor <= 0 || amountMinor > 99_999_999 {
		return CheckReservation{}, ErrInvalidChange
	}
	value, err := s.store.ReserveSupportCheck(ctx, affiliateID, sessionID, amountMinor, s.ids.New(), change)
	if err != nil {
		return CheckReservation{}, err
	}
	if err := value.Validate(); err != nil {
		return CheckReservation{}, err
	}
	return value, nil
}

// TransitionSupportCheck records only the internal accounting disposition of
// a reserved amount. Delivery, stop, loss and reissue remain Support procedure.
func (s *Service) TransitionSupportCheck(ctx context.Context, reservationID string, expectedVersion uint64, state, actor, reason, environment string) (CheckReservation, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(reservationID) != nil || expectedVersion == 0 || (state != "settled" && state != "released") {
		return CheckReservation{}, ErrInvalidChange
	}
	value, err := s.store.TransitionSupportCheck(ctx, reservationID, expectedVersion, state, change)
	if err != nil {
		return CheckReservation{}, err
	}
	if err := value.Validate(); err != nil {
		return CheckReservation{}, err
	}
	return value, nil
}

// SetRetentionHold applies or releases a scoped, auditable legal hold. The
// expected version prevents one reviewer from overwriting another decision.
func (s *Service) SetRetentionHold(ctx context.Context, affiliateID ids.AffiliateID, expectedVersion uint64, legalHold bool, actor, reason, environment string) (RetentionControl, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil || expectedVersion == 0 {
		return RetentionControl{}, ErrInvalidChange
	}
	value, err := s.store.SetRetentionHold(ctx, affiliateID, expectedVersion, legalHold, change)
	if err != nil {
		return RetentionControl{}, err
	}
	if err := value.Validate(); err != nil {
		return RetentionControl{}, err
	}
	return value, nil
}

// RestrictRetention is the one-way verified-erasure transition. The
// enrollment must already be terminally closed; billing-credit settlement and
// required evidence remain available only to constrained operational roles.
func (s *Service) RestrictRetention(ctx context.Context, affiliateID ids.AffiliateID, expectedVersion uint64, actor, reason, environment string) (RetentionControl, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil || expectedVersion == 0 {
		return RetentionControl{}, ErrInvalidChange
	}
	value, err := s.store.RestrictRetention(ctx, affiliateID, expectedVersion, change)
	if err != nil {
		return RetentionControl{}, err
	}
	if err := value.Validate(); err != nil || value.RestrictedAt == nil {
		return RetentionControl{}, ErrInvalidChange
	}
	return value, nil
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	eventID := ids.AffiliateEnrollmentEventID(s.ids.New())
	if ids.Validate(string(eventID)) != nil || len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") ||
		len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}
