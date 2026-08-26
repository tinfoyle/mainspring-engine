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
	enrollment affiliates.Enrollment
	risk       affiliateadmin.RiskSummary
	state      affiliates.EnrollmentState
	version    uint64
	change     affiliateadmin.Change
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
