package browserapp

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

func TestBillingViewUsesCurrentManagedSubscription(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	publication := catalog.Default(now)
	status := commercialaccess.Status{Subscriptions: []commercialaccess.Subscription{{State: "canceled", OfferCode: "team-monthly-v1", LastSyncedAt: now.Add(-time.Hour)}, {State: "active", OfferCode: "operating-monthly-v1", LastSyncedAt: now}}}
	plans, state, _, synced := billingView(publication, accounts.AccountPaid, status, now)
	if state != "active" || synced == "Local entitlement snapshot" {
		t.Fatalf("state=%s synced=%s", state, synced)
	}
	current := ""
	for _, plan := range plans {
		if plan.Current {
			current = plan.OfferCode
		}
	}
	if current != "operating-monthly-v1" {
		t.Fatalf("current plan=%s", current)
	}
}

func TestCanceledSubscriptionAllowsNoPaidPlanToAppearCurrent(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	plans, state, _, _ := billingView(catalog.Default(now), accounts.AccountFree, commercialaccess.Status{Subscriptions: []commercialaccess.Subscription{{State: "canceled", OfferCode: "team-monthly-v1"}}}, now)
	if state != "canceled" {
		t.Fatalf("state=%s", state)
	}
	for _, plan := range plans {
		if plan.Current {
			t.Fatalf("canceled paid plan is still current: %s", plan.OfferCode)
		}
	}
}
