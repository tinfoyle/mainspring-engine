package affiliateadmin_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const adminAffiliateID = "10000000-0000-4000-8000-000000000001"

type adminStore struct {
	enrollment    affiliates.Enrollment
	risk          affiliateadmin.RiskSummary
	state         affiliates.EnrollmentState
	version       uint64
	change        affiliateadmin.Change
	policy        affiliateadmin.SettlementPolicy
	check         affiliateadmin.CheckReservation
	sessionID     ids.SessionID
	amount        int64
	reservationID string
	retention     affiliateadmin.RetentionControl
	legalHold     bool
}

func (s *adminStore) SetRetentionHold(_ context.Context, affiliateID ids.AffiliateID, version uint64, legalHold bool, change affiliateadmin.Change) (affiliateadmin.RetentionControl, error) {
	s.version, s.legalHold, s.change = version, legalHold, change
	value := affiliateadmin.RetentionControl{AffiliateID: affiliateID, LegalHold: legalHold, Version: version + 1,
		UpdatedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)}
	s.retention = value
	return value, nil
}

func (s *adminStore) RestrictRetention(_ context.Context, affiliateID ids.AffiliateID, version uint64, change affiliateadmin.Change) (affiliateadmin.RetentionControl, error) {
	s.version, s.change = version, change
	restrictedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	value := affiliateadmin.RetentionControl{AffiliateID: affiliateID, RestrictedAt: &restrictedAt,
		Version: version + 1, UpdatedAt: restrictedAt}
	s.retention = value
	return value, nil
}

func (s *adminStore) ReserveSupportCheck(_ context.Context, affiliateID ids.AffiliateID, sessionID ids.SessionID, amount int64, reservationID string, change affiliateadmin.Change) (affiliateadmin.CheckReservation, error) {
	s.sessionID, s.amount, s.reservationID, s.change = sessionID, amount, reservationID, change
	value := affiliateadmin.CheckReservation{ID: reservationID, AffiliateID: affiliateID, State: "reserved",
		AmountMinor: amount, Currency: "USD", PolicyVersion: 1, Version: 1,
		CreatedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	s.check = value
	return value, nil
}

func (s *adminStore) TransitionSupportCheck(_ context.Context, reservationID string, version uint64, state string, change affiliateadmin.Change) (affiliateadmin.CheckReservation, error) {
	s.reservationID, s.version, s.state, s.change = reservationID, version, affiliates.EnrollmentState(state), change
	value := affiliateadmin.CheckReservation{ID: reservationID, AffiliateID: adminAffiliateID, State: state,
		AmountMinor: 10_000, Currency: "USD", PolicyVersion: 1, Version: version + 1,
		CreatedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)}
	s.check = value
	return value, nil
}

func (s *adminStore) PublishSettlementPolicy(_ context.Context, _, newVersion uint64, threshold int64, change affiliateadmin.Change) (affiliateadmin.SettlementPolicy, error) {
	s.change = change
	s.policy = affiliateadmin.SettlementPolicy{Version: newVersion, Mode: "account_credit_with_support_check", Currency: "USD", CheckThresholdMinor: threshold, EffectiveFrom: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)}
	return s.policy, nil
}

func (s *adminStore) Inspect(_ context.Context, _ ids.AffiliateID, change affiliateadmin.Change) (affiliates.Enrollment, error) {
	s.change = change
	return s.enrollment, nil
}

func (s *adminStore) InspectRisk(_ context.Context, _ ids.AffiliateID, change affiliateadmin.Change) (affiliateadmin.RiskSummary, error) {
	s.change = change
	return s.risk, nil
}

func (s *adminStore) Transition(_ context.Context, _ ids.AffiliateID, version uint64, state affiliates.EnrollmentState, change affiliateadmin.Change) (affiliates.Enrollment, error) {
	s.version, s.state, s.change = version, state, change
	value := s.enrollment
	value.State, value.Version = state, version+1
	return value, nil
}

type adminIDs struct{ value string }

func (g adminIDs) New() string { return g.value }

func TestTransitionBindsExactVersionAndAuditContext(t *testing.T) {
	store := &adminStore{enrollment: affiliates.Enrollment{ID: adminAffiliateID, UserID: "10000000-0000-4000-8000-000000000002",
		PublicCode: "IO-PARTNER1", TermsVersion: 1, RuleVersion: 1, State: affiliates.EnrollmentActive, Version: 4,
		CreatedAt: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)}}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	value, err := service.Transition(context.Background(), adminAffiliateID, 4, affiliates.EnrollmentSuspended,
		"operator@example.test", "Suspend while the referral is reviewed", "local")
	if err != nil || value.State != affiliates.EnrollmentSuspended || store.version != 4 || store.change.Environment != "local" {
		t.Fatalf("value=%+v store=%+v err=%v", value, store, err)
	}
}

func TestTransitionRejectsUnboundedAuditInput(t *testing.T) {
	service, _ := affiliateadmin.New(&adminStore{}, adminIDs{"10000000-0000-4000-8000-000000000003"})
	_, err := service.Transition(context.Background(), adminAffiliateID, 1, affiliates.EnrollmentSuspended,
		"op", "short", "Local")
	if !errors.Is(err, affiliateadmin.ErrInvalidChange) {
		t.Fatalf("error=%v", err)
	}
}

func TestSupportCanPublishNextCheckThresholdPolicy(t *testing.T) {
	store := &adminStore{}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	policy, err := service.PublishSettlementPolicy(context.Background(), 1, 2, 25_000,
		"support@example.test", "Adjust the reviewed check eligibility threshold", "local")
	if err != nil || policy.Version != 2 || policy.CheckThresholdMinor != 25_000 || store.change.Actor != "support@example.test" {
		t.Fatalf("policy=%+v change=%+v err=%v", policy, store.change, err)
	}
	if _, err := service.PublishSettlementPolicy(context.Background(), 1, 3, 25_000,
		"support@example.test", "Attempt to skip a policy version", "local"); !errors.Is(err, affiliateadmin.ErrInvalidChange) {
		t.Fatalf("nonsequential policy error=%v", err)
	}
}

func TestSupportCheckReservationBindsCustomerPasskeyEvidenceAndExactAmount(t *testing.T) {
	store := &adminStore{}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	value, err := service.ReserveSupportCheck(context.Background(), adminAffiliateID,
		"10000000-0000-4000-8000-000000000004", 12_500, "support@example.test",
		"Reserve the amount selected during Support review", "local")
	if err != nil || value.State != "reserved" || store.amount != 12_500 ||
		store.sessionID != "10000000-0000-4000-8000-000000000004" || store.change.Actor != "support@example.test" {
		t.Fatalf("value=%+v store=%+v err=%v", value, store, err)
	}
	settled, err := service.TransitionSupportCheck(context.Background(), value.ID, 1, "settled",
		"support@example.test", "Record external check accounting completion", "local")
	if err != nil || settled.State != "settled" || settled.Version != 2 || store.version != 1 {
		t.Fatalf("settled=%+v store=%+v err=%v", settled, store, err)
	}
}

func TestRetentionHoldIsScopedVersionedAndAudited(t *testing.T) {
	store := &adminStore{}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	held, err := service.SetRetentionHold(context.Background(), adminAffiliateID, 2, true,
		"privacy@example.test", "Preserve Affiliate evidence for a scoped legal review", "local")
	if err != nil || !held.LegalHold || held.Version != 3 || store.version != 2 || !store.legalHold ||
		store.change.Actor != "privacy@example.test" {
		t.Fatalf("held=%+v store=%+v err=%v", held, store, err)
	}
	released, err := service.SetRetentionHold(context.Background(), adminAffiliateID, held.Version, false,
		"privacy@example.test", "Release the Affiliate hold after the legal review", "local")
	if err != nil || released.LegalHold || released.Version != 4 {
		t.Fatalf("released=%+v err=%v", released, err)
	}
	if _, err := service.SetRetentionHold(context.Background(), adminAffiliateID, 0, true,
		"privacy@example.test", "Attempt a hold without an observed version", "local"); !errors.Is(err, affiliateadmin.ErrInvalidChange) {
		t.Fatalf("unversioned hold error=%v", err)
	}
}

func TestVerifiedErasureRestrictionIsExplicitAndOneWay(t *testing.T) {
	store := &adminStore{}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	restricted, err := service.RestrictRetention(context.Background(), adminAffiliateID, 4,
		"privacy@example.test", "Restrict retained evidence after verified Affiliate erasure", "local")
	if err != nil || restricted.RestrictedAt == nil || restricted.Version != 5 || store.version != 4 {
		t.Fatalf("restricted=%+v store=%+v err=%v", restricted, store, err)
	}
}

func TestInspectRiskReturnsContentFreeSignalsAndDeterministicFlags(t *testing.T) {
	observed := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := &adminStore{risk: affiliateadmin.RiskSummary{
		AffiliateID: adminAffiliateID, EnrollmentState: affiliates.EnrollmentActive, EnrollmentVersion: 4,
		ObservedAt: observed, ReservationWindowStartedAt: observed.Add(-24 * time.Hour),
		ValidReservations: 12, DistinctReferredAccounts: 5, RepeatedReferredAccounts: 1,
		MaximumReservationsPerAccount: 6, CrossAffiliateCodeCycleAccounts: 1, LockedAttributions: 2,
		LargestAccountShareBasisPoints: 5000, CodeReplacementWindowStartedAt: observed.Add(-30 * 24 * time.Hour),
		CodeReplacements: 3,
	}}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	value, err := service.InspectRisk(context.Background(), adminAffiliateID, "operator@example.test",
		"Review aggregate referral risk evidence", "local")
	want := []affiliateadmin.RiskFlag{affiliateadmin.RiskCrossAffiliateCodeCycling, affiliateadmin.RiskRapidCodeReplacement,
		affiliateadmin.RiskReferralConcentration, affiliateadmin.RiskRepeatedCheckoutCreation}
	if err != nil || !slices.Equal(value.Flags(), want) || store.change.Actor != "operator@example.test" ||
		store.change.Reason != "Review aggregate referral risk evidence" {
		t.Fatalf("value=%+v flags=%v change=%+v err=%v", value, value.Flags(), store.change, err)
	}
}

func TestInspectRiskRejectsInvalidAggregate(t *testing.T) {
	observed := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := &adminStore{risk: affiliateadmin.RiskSummary{AffiliateID: adminAffiliateID,
		EnrollmentState: affiliates.EnrollmentActive, EnrollmentVersion: 1, ObservedAt: observed,
		ReservationWindowStartedAt: observed.Add(-24 * time.Hour), CodeReplacementWindowStartedAt: observed.Add(-30 * 24 * time.Hour),
		ValidReservations: 1, DistinctReferredAccounts: 2}}
	service, _ := affiliateadmin.New(store, adminIDs{"10000000-0000-4000-8000-000000000003"})
	if _, err := service.InspectRisk(context.Background(), adminAffiliateID, "operator@example.test",
		"Review aggregate referral risk evidence", "local"); !errors.Is(err, affiliateadmin.ErrInvalidChange) {
		t.Fatalf("error=%v", err)
	}
}
