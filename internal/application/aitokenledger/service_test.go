package aitokenledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	ledgerAccountID ids.AccountID = "10000000-0000-4000-8000-000000000001"
	ledgerUserID    ids.UserID    = "20000000-0000-4000-8000-000000000002"
	ledgerRequestID               = "30000000-0000-4000-8000-000000000003"
)

type testAuthorizer struct {
	requirement access.Requirement
	err         error
}

func (a *testAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.requirement = requirement
	return access.AccountContext{}, a.err
}

type testStore struct {
	reservation    aitokens.Reservation
	issued         aitokens.Grant
	expireIncluded bool
	usage          Usage
	balance        aitokens.Balance
	err            error
}

func (s *testStore) Balance(context.Context, ids.AccountID, time.Time) (aitokens.Balance, error) {
	return s.balance, s.err
}

func (s *testStore) Reserve(_ context.Context, reservation aitokens.Reservation, _ time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	s.reservation = reservation
	return reservation, s.balance, s.err
}

func (s *testStore) Close(_ context.Context, _ ids.AccountID, _ string, usage Usage, _ time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	s.usage = usage
	return s.reservation, s.balance, s.err
}

func (s *testStore) Issue(_ context.Context, grant aitokens.Grant, expireIncluded bool) (aitokens.Grant, aitokens.Balance, error) {
	s.issued, s.expireIncluded = grant, expireIncluded
	return grant, s.balance, s.err
}

type testIDs struct{ next string }

func (g testIDs) New() string { return g.next }

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

func TestReserveFreezesSelectedComplexityRateAndRequiresAgentsMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store, authorizer := &testStore{}, &testAuthorizer{}
	service := newTestService(t, store, authorizer, now, "40000000-0000-4000-8000-000000000004")
	result, _, err := service.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: ledgerUserID}, AccountID: ledgerAccountID, RequestID: ledgerRequestID, Complexity: catalog.AIComplexityBalanced})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.requirement.Package != catalog.PackageAgents || !authorizer.requirement.Mutation {
		t.Fatalf("authorization requirement = %+v", authorizer.requirement)
	}
	if result.Rate.Complexity != catalog.AIComplexityBalanced || result.Rate.InternalProvider == "" || result.Maximum != result.Rate.MaximumReservation || result.ID == "" {
		t.Fatalf("frozen reservation = %+v", result)
	}
}

func TestReserveDoesNotTouchStoreWhenAuthorizationFails(t *testing.T) {
	authorizer := &testAuthorizer{err: errors.New("denied")}
	store := &testStore{}
	service := newTestService(t, store, authorizer, time.Now().UTC(), "40000000-0000-4000-8000-000000000004")
	_, _, err := service.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: ledgerUserID}, AccountID: ledgerAccountID, RequestID: ledgerRequestID, Complexity: catalog.AIComplexityBalanced})
	if err == nil || store.reservation.ID != "" {
		t.Fatalf("reservation=%+v err=%v", store.reservation, err)
	}
}

func TestIssueIncludedFreezesCatalogDefinitionAndExpiresPriorCohort(t *testing.T) {
	now := time.Now().UTC()
	store := &testStore{}
	service := newTestService(t, store, &testAuthorizer{}, now, "40000000-0000-4000-8000-000000000004")
	grant, _, err := service.IssueIncluded(context.Background(), ledgerAccountID, "in_renewal_1")
	if err != nil {
		t.Fatal(err)
	}
	if !store.expireIncluded || grant.Origin != aitokens.OriginIncluded || grant.Quantity != 10_000 || grant.DefinitionCode != "team_renewal_v1" || grant.CatalogVersion != 3 {
		t.Fatalf("included grant = %+v expire=%v", grant, store.expireIncluded)
	}
}

func TestIssuePurchasedUsesEffectiveBundleWithoutExpiringIncluded(t *testing.T) {
	now := time.Now().UTC()
	store := &testStore{}
	service := newTestService(t, store, &testAuthorizer{}, now, "40000000-0000-4000-8000-000000000004")
	grant, _, err := service.IssuePurchased(context.Background(), ledgerAccountID, "tokens_10k_v1", "pi_topup_1")
	if err != nil {
		t.Fatal(err)
	}
	if store.expireIncluded || grant.Origin != aitokens.OriginPurchased || grant.Quantity != 10_000 || grant.ExpiresAt != nil {
		t.Fatalf("purchased grant = %+v expire=%v", grant, store.expireIncluded)
	}
}

func newTestService(t *testing.T, store Store, authorizer Authorizer, now time.Time, nextID string) *Service {
	t.Helper()
	publication := catalog.Default(now.Add(-time.Hour))
	service, err := New(store, authorizer, func() catalog.PublishedCatalog { return publication }, testIDs{next: nextID}, testClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
