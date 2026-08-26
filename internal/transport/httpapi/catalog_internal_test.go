package httpapi

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

func TestPublicCatalogOffersArePublishedAndEffective(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	publication := catalog.Default(now.Add(-time.Hour))
	publication.Offers = append(publication.Offers,
		catalog.Offer{Code: "scheduled-v1", PlanCode: "team", PlanVersion: 2, Currency: "USD", AmountMinor: 9900, BillingInterval: "month", Published: true, EffectiveFrom: now.Add(time.Hour)},
		catalog.Offer{Code: "draft-v1", PlanCode: "team", PlanVersion: 2, Currency: "USD", AmountMinor: 7900, BillingInterval: "month", Published: false, EffectiveFrom: now.Add(-time.Hour)},
	)
	offers := effectiveCatalogOffers(publication, now)
	for _, offer := range offers {
		if offer.Code == "scheduled-v1" || offer.Code == "draft-v1" {
			t.Fatalf("non-effective offer leaked through public Catalog: %s", offer.Code)
		}
	}
	if len(offers) != 1 {
		t.Fatalf("effective offer count = %d, want 1", len(offers))
	}
}
