package affiliatesupportadmin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupportadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const supportRequestID = "20000000-0000-4000-8000-000000000001"

type supportAdminStore struct {
	state   affiliates.SupportState
	outcome affiliates.SupportOutcome
	version uint64
	change  affiliatesupportadmin.Change
}

func (s *supportAdminStore) Inspect(_ context.Context, _ ids.AffiliateSupportRequestID, change affiliatesupportadmin.Change) (affiliates.SupportRequest, error) {
	s.change = change
	return supportRequest(affiliates.SupportSubmitted, "", 1), nil
}

func (s *supportAdminStore) Transition(_ context.Context, _ ids.AffiliateSupportRequestID, version uint64, state affiliates.SupportState, outcome affiliates.SupportOutcome, change affiliatesupportadmin.Change) (affiliates.SupportRequest, error) {
	s.state, s.outcome, s.version, s.change = state, outcome, version, change
	return supportRequest(state, outcome, version+1), nil
}

type supportAdminIDs struct{ value string }

func (g supportAdminIDs) New() string { return g.value }

func supportRequest(state affiliates.SupportState, outcome affiliates.SupportOutcome, version uint64) affiliates.SupportRequest {
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	return affiliates.SupportRequest{ID: supportRequestID, AffiliateID: "20000000-0000-4000-8000-000000000002",
		UserID: "20000000-0000-4000-8000-000000000003", Kind: affiliates.SupportEnrollmentAppeal,
		State: state, Outcome: outcome, Version: version, CreatedAt: now, UpdatedAt: now}
}

func TestResolveBindsDecisionAndExactVersion(t *testing.T) {
	store := &supportAdminStore{}
	service, _ := affiliatesupportadmin.New(store, supportAdminIDs{"20000000-0000-4000-8000-000000000004"})
	value, err := service.Resolve(context.Background(), supportRequestID, 2, affiliates.SupportDenied,
		"operator@example.test", "Decline after evidence review", "local")
	if err != nil || value.State != affiliates.SupportDeclined || store.version != 2 || store.outcome != affiliates.SupportDenied {
		t.Fatalf("value=%+v store=%+v err=%v", value, store, err)
	}
}

func TestResolveRejectsUnknownOutcome(t *testing.T) {
	service, _ := affiliatesupportadmin.New(&supportAdminStore{}, supportAdminIDs{"20000000-0000-4000-8000-000000000004"})
	_, err := service.Resolve(context.Background(), supportRequestID, 1, "maybe", "operator@example.test", "Documented decision", "local")
	if !errors.Is(err, affiliatesupportadmin.ErrInvalidChange) {
		t.Fatalf("error=%v", err)
	}
}
