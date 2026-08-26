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
	AffiliateID                     ids.AffiliateID
	EnrollmentState                 affiliates.EnrollmentState
	EnrollmentVersion               uint64
	ObservedAt                      time.Time
	ReservationWindowStartedAt      time.Time
	ValidReservations               uint64
	DistinctReferredAccounts        uint64
	RepeatedReferredAccounts        uint64
	MaximumReservationsPerAccount   uint64
	CrossAffiliateCodeCycleAccounts uint64
	LockedAttributions              uint64
	LargestAccountShareBasisPoints  uint64
	CodeReplacementWindowStartedAt  time.Time
	CodeReplacements                uint64
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

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	eventID := ids.AffiliateEnrollmentEventID(s.ids.New())
	if ids.Validate(string(eventID)) != nil || len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") ||
		len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}
