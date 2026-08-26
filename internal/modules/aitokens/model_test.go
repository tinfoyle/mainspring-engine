package aitokens

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const tokenAccountID ids.AccountID = "10000000-0000-4000-8000-000000000001"

func TestReserveUsesExpiringCohortsBeforePurchasedBalance(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	promotionExpiry, includedExpiry := now.Add(24*time.Hour), now.Add(30*24*time.Hour)
	purchased := tokenGrant(t, "20000000-0000-4000-8000-000000000001", OriginPurchased, 500, nil, now.Add(-3*time.Hour))
	included := tokenGrant(t, "20000000-0000-4000-8000-000000000002", OriginIncluded, 300, &includedExpiry, now.Add(-2*time.Hour))
	promotion := tokenGrant(t, "20000000-0000-4000-8000-000000000003", OriginPromotion, 200, &promotionExpiry, now.Add(-time.Hour))
	updated, reservation, err := Reserve([]Grant{purchased, included, promotion}, Reservation{ID: "30000000-0000-4000-8000-000000000001", AccountID: tokenAccountID, RequestID: "40000000-0000-4000-8000-000000000001", Rate: testRate(catalog.AIComplexityBalanced, 600), Maximum: 600, State: ReservationActive}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []Allocation{{GrantID: promotion.ID, Amount: 200}, {GrantID: included.ID, Amount: 300}, {GrantID: purchased.ID, Amount: 100}}
	if len(reservation.Allocations) != len(want) {
		t.Fatalf("allocations = %+v", reservation.Allocations)
	}
	for index := range want {
		if reservation.Allocations[index] != want[index] {
			t.Fatalf("allocation %d = %+v want %+v", index, reservation.Allocations[index], want[index])
		}
	}
	if updated[0].Available != 400 || updated[0].Reserved != 100 || updated[1].Available != 0 || updated[2].Available != 0 {
		t.Fatalf("updated grants = %+v", updated)
	}
}

func TestReserveIsAtomicWhenBalanceIsInsufficient(t *testing.T) {
	now := time.Now().UTC()
	grant := tokenGrant(t, "20000000-0000-4000-8000-000000000001", OriginPurchased, 50, nil, now)
	updated, _, err := Reserve([]Grant{grant}, Reservation{ID: "30000000-0000-4000-8000-000000000001", AccountID: tokenAccountID, RequestID: "40000000-0000-4000-8000-000000000001", Rate: testRate(catalog.AIComplexityAdvanced, 100), Maximum: 100, State: ReservationActive}, now)
	if !errors.Is(err, ErrInsufficient) || updated != nil || grant.Available != 50 || grant.Reserved != 0 {
		t.Fatalf("updated=%+v grant=%+v err=%v", updated, grant, err)
	}
}

func TestSettlementDebitsExactUsageAndDoesNotResurrectExpiredGrant(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	grant := tokenGrant(t, "20000000-0000-4000-8000-000000000001", OriginIncluded, 500, &expires, now)
	reserved, reservation, err := Reserve([]Grant{grant}, Reservation{ID: "30000000-0000-4000-8000-000000000001", AccountID: tokenAccountID, RequestID: "40000000-0000-4000-8000-000000000001", Rate: testRate(catalog.AIComplexityBalanced, 400), Maximum: 400, State: ReservationActive}, now)
	if err != nil {
		t.Fatal(err)
	}
	closed, reservation, err := Close(reserved, reservation, 125, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reservation.State != ReservationSettled || reservation.Settled != 125 || closed[0].Consumed != 125 || closed[0].Reserved != 0 || closed[0].Available != 100 {
		t.Fatalf("grant=%+v reservation=%+v", closed[0], reservation)
	}
	balance, err := Summarize(closed, now.Add(2*time.Minute))
	if err != nil || balance.Available != 0 || balance.Consumed != 125 {
		t.Fatalf("balance=%+v err=%v", balance, err)
	}
}

func TestReverseUnusedRequiresReservationsToClose(t *testing.T) {
	now := time.Now().UTC()
	grant := tokenGrant(t, "20000000-0000-4000-8000-000000000001", OriginPurchased, 500, nil, now)
	grant.Available, grant.Reserved = 400, 100
	if _, err := ReverseUnused(grant, GrantReversed); !errors.Is(err, ErrGrantInUse) {
		t.Fatalf("reverse active grant error = %v", err)
	}
	grant.Reserved, grant.Consumed = 0, 100
	reversed, err := ReverseUnused(grant, GrantReversed)
	if err != nil || reversed.Available != 0 || reversed.Consumed != 100 || reversed.State != GrantReversed {
		t.Fatalf("reversed=%+v err=%v", reversed, err)
	}
}

func TestChargeUsesFrozenRateAndCeiling(t *testing.T) {
	rate := catalog.AIComplexityRate{MinimumCharge: 5, MaximumReservation: 1000, InputPerThousand: 2, CachedInputPerThousand: 1, OutputPerThousand: 8, ToolInvocation: 10}
	charge, err := Charge(rate, 1250, 250, 125, 2)
	// 1000*2 + 250*1 + 125*8 + 2*10*1000 = 23250 milli-Tokens.
	if err != nil || charge != 24 {
		t.Fatalf("charge=%d err=%v", charge, err)
	}
	minimum, err := Charge(rate, 1, 0, 0, 0)
	if err != nil || minimum != 5 {
		t.Fatalf("minimum charge=%d err=%v", minimum, err)
	}
}

func tokenGrant(t *testing.T, id string, origin GrantOrigin, quantity int64, expires *time.Time, now time.Time) Grant {
	t.Helper()
	grant, err := NewGrant(ids.AITokenGrantID(id), tokenAccountID, origin, "definition_v1", 3, "source-1", quantity, expires, now)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func testRate(complexity catalog.AIComplexity, maximum int64) catalog.AIComplexityRate {
	return catalog.AIComplexityRate{Code: string(complexity) + "_v1", Version: 1, Complexity: complexity, InputPerThousand: 1, CachedInputPerThousand: 1, OutputPerThousand: 1, MinimumCharge: 1, MaximumReservation: maximum, EstimatedMinimum: 1, EstimatedMaximum: maximum, InternalProvider: "configurable", InternalModel: string(complexity), InternalAdapterVersion: 1, InternalModelPolicyVersion: 1}
}
