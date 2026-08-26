package aitokenledger

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestBillingProjectorIssuesIncludedTokensForPaidServicePeriod(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	store := &testStore{}
	publication := catalog.Default(now.Add(-time.Hour))
	issuer, err := NewIssuer(store, func() catalog.PublishedCatalog { return publication }, testIDs{next: "40000000-0000-4000-8000-000000000004"}, testClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	projector, err := NewBillingEventProjector(issuer)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"data":{"object":{"id":"in_paid_1","amount_paid":5000,"billing_reason":"subscription_cycle","paid":true,"status":"paid","parent":{"subscription_details":{"metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001"}}}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid"}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if store.issued.Origin != aitokens.OriginIncluded || store.issued.SourceReference != "in_paid_1" || store.issued.AccountID != ledgerAccountID || store.issued.Quantity != 10_000 {
		t.Fatalf("issued grant = %+v", store.issued)
	}
}

func TestBillingProjectorIgnoresProrationAndNonInvoiceEvents(t *testing.T) {
	now := time.Now().UTC()
	store := &testStore{}
	publication := catalog.Default(now.Add(-time.Hour))
	issuer, _ := NewIssuer(store, func() catalog.PublishedCatalog { return publication }, testIDs{next: "40000000-0000-4000-8000-000000000004"}, testClock{now: now})
	projector, _ := NewBillingEventProjector(issuer)
	payload := []byte(`{"data":{"object":{"id":"in_proration_1","amount_paid":500,"billing_reason":"subscription_update","paid":true,"status":"paid","metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001"}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", AccountID: ids.AccountID(ledgerAccountID)}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if store.issued.ID != "" {
		t.Fatalf("proration issued grant = %+v", store.issued)
	}
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "customer.subscription.updated"}, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}
