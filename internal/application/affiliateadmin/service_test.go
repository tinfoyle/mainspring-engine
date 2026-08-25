package affiliateadmin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const adminAffiliateID = "10000000-0000-4000-8000-000000000001"

type adminStore struct {
	enrollment affiliates.Enrollment
	state      affiliates.EnrollmentState
	version    uint64
	change     affiliateadmin.Change
}

func (s *adminStore) Inspect(_ context.Context, _ ids.AffiliateID, change affiliateadmin.Change) (affiliates.Enrollment, error) {
	s.change = change
	return s.enrollment, nil
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
