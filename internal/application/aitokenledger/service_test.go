package aitokenledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
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
	reservation     aitokens.Reservation
	issued          aitokens.Grant
	expireIncluded  bool
	usage           Usage
	balance         aitokens.Balance
	err             error
	promotionExists bool
	campaignVersion uint64
	perAccountLimit int64
	issuanceCap     int64
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

func (s *testStore) Promotion(context.Context, ids.AccountID, string, time.Time) (aitokens.Grant, aitokens.Balance, bool, error) {
	return s.issued, s.balance, s.promotionExists, s.err
}

func (s *testStore) RedeemPromotion(_ context.Context, grant aitokens.Grant, campaignVersion uint64, perAccountLimit, issuanceCap int64) (aitokens.Grant, aitokens.Balance, error) {
	s.issued, s.campaignVersion, s.perAccountLimit, s.issuanceCap = grant, campaignVersion, perAccountLimit, issuanceCap
	return grant, s.balance, s.err
}

func (s *testStore) ReverseUnused(_ context.Context, _ ids.AccountID, _ aitokens.GrantOrigin, _, _ string, _ time.Time) (aitokens.Grant, aitokens.Balance, error) {
	return s.issued, s.balance, s.err
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

func TestRedeemPromotionFreezesCatalogCampaignAndRequiresBillingAuthority(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	publication := catalog.Default(now.Add(-time.Hour))
	publication.AITokenPromotions = []catalog.AITokenPromotion{{Code: "launch_bonus", Version: 4, Quantity: 2_000_000, EffectiveFrom: now.Add(-time.Hour), EffectiveUntil: now.Add(time.Hour), ExpiresAfterDays: 45, RedemptionsPerAccount: 1, IssuanceCap: 100, Stacking: "none", Disclosure: "Launch bonus."}}
	store, authorizer := &testStore{}, &testAuthorizer{}
	service, err := New(store, authorizer, func() catalog.PublishedCatalog { return publication }, testIDs{next: "40000000-0000-4000-8000-000000000004"}, testClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	grant, _, err := service.RedeemPromotion(context.Background(), RedeemPromotionCommand{ActorUserID: ledgerUserID, Session: sessions.Session{UserID: ledgerUserID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}, AccountID: ledgerAccountID, PromotionCode: "launch_bonus", RequestID: ledgerRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if len(authorizer.requirement.Roles) != 2 || authorizer.requirement.Roles[0] != accounts.RoleOwner || authorizer.requirement.Roles[1] != accounts.RoleBillingAdmin {
		t.Fatalf("authorization requirement = %+v", authorizer.requirement)
	}
	if grant.Origin != aitokens.OriginPromotion || grant.Quantity != 2_000_000 || grant.DefinitionCode != "launch_bonus" || grant.CatalogVersion != publication.Version || grant.ExpiresAt == nil || !grant.ExpiresAt.Equal(now.AddDate(0, 0, 45)) {
		t.Fatalf("promotion grant = %+v", grant)
	}
	if store.campaignVersion != 4 || store.perAccountLimit != 1 || store.issuanceCap != 100 {
		t.Fatalf("campaign controls = version %d per Account %d cap %d", store.campaignVersion, store.perAccountLimit, store.issuanceCap)
	}
}

func TestRedeemPromotionReturnsDurableExactRetryBeforeCurrentCatalogLookup(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	existing, err := aitokens.NewGrant("40000000-0000-4000-8000-000000000004", ledgerAccountID, aitokens.OriginPromotion, "retired_campaign", 2, ledgerRequestID, 75, &expiresAt, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	store := &testStore{issued: existing, promotionExists: true}
	service := newTestService(t, store, &testAuthorizer{}, now, "50000000-0000-4000-8000-000000000005")
	grant, _, err := service.RedeemPromotion(context.Background(), RedeemPromotionCommand{ActorUserID: ledgerUserID, Session: sessions.Session{UserID: ledgerUserID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}, AccountID: ledgerAccountID, PromotionCode: "retired_campaign", RequestID: ledgerRequestID})
	if err != nil || grant.ID != existing.ID {
		t.Fatalf("grant=%+v err=%v", grant, err)
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
