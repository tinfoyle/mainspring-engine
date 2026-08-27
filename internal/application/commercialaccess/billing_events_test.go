package commercialaccess

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type purchaseClock struct{ now time.Time }

func (c purchaseClock) Now() time.Time { return c.now }

type purchaseProjectionStore struct {
	got         PurchaseEvidence
	out         PurchaseProjection
	refundFound bool
}

func (s *purchaseProjectionStore) ProjectOneTimePurchase(_ context.Context, evidence PurchaseEvidence) (PurchaseProjection, error) {
	s.got = evidence
	return s.out, nil
}

func (s *purchaseProjectionStore) ProjectPurchaseRefund(_ context.Context, evidence PurchaseRefundEvidence) (PurchaseProjection, bool, error) {
	if evidence.PaymentIntentID != s.out.PaymentIntentID || !s.refundFound {
		return PurchaseProjection{}, false, nil
	}
	if !evidence.Refunded || evidence.AmountRefunded != evidence.Amount {
		return PurchaseProjection{}, false, ErrInvalidPurchaseEvidence
	}
	return s.out, true, nil
}

func (s *purchaseProjectionStore) ProjectSubscriptionCommissioning(context.Context, SubscriptionCommissioningEvidence) (PurchaseProjection, bool, error) {
	return s.out, true, nil
}

func TestOneTimeBillingProjectorUsesFrozenTokenQuantity(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	ledger := memory.NewAITokenLedger()
	issuer, err := aitokenledger.NewIssuer(ledger, func() catalog.PublishedCatalog { return catalog.Default(now) }, ids.RandomGenerator{}, purchaseClock{now})
	if err != nil {
		t.Fatal(err)
	}
	store := &purchaseProjectionStore{refundFound: true, out: PurchaseProjection{Snapshot: PurchaseSnapshot{AccountID: accountID, Kind: billing.PurchaseAITokenTopUp, ItemCode: "tokens_10k_v1", ItemVersion: 1, CatalogVersion: 3, Currency: "USD", AmountMinor: 1000, Quantity: 2_000_000}, PaymentIntentID: "pi_paid_1"}}
	projector, _ := NewBillingEventProjector(store, issuer)
	payload := []byte(`{"data":{"object":{"id":"cs_paid_1","mode":"payment","payment_status":"paid","payment_intent":"pi_paid_1","client_reference_id":"10000000-0000-4000-8000-000000000001","currency":"usd","amount_subtotal":1000,"metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001","spyglass_purchase_kind":"ai_token_top_up","spyglass_item_code":"tokens_10k_v1","spyglass_item_version":"1","spyglass_catalog_version":"3","spyglass_checkout_request_id":"20000000-0000-4000-8000-000000000002"}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "checkout.session.completed", AccountID: accountID}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	balance, err := ledger.Balance(context.Background(), accountID, now)
	if err != nil || balance.Available != 2_000_000 || store.got.AmountSubtotal != 1000 || store.got.RequestID != "20000000-0000-4000-8000-000000000002" {
		t.Fatalf("balance=%+v evidence=%+v err=%v", balance, store.got, err)
	}
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "checkout.session.completed", AccountID: accountID}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	balance, _ = ledger.Balance(context.Background(), accountID, now)
	if balance.Available != 2_000_000 {
		t.Fatalf("replay duplicated grant: %+v", balance)
	}
	refund := []byte(`{"data":{"object":{"id":"ch_refunded_1","payment_intent":"pi_paid_1","amount":1000,"amount_refunded":1000,"refunded":true}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "charge.refunded"}, Payload: refund}); err != nil {
		t.Fatal(err)
	}
	balance, _ = ledger.Balance(context.Background(), accountID, now)
	if balance.Available != 0 || balance.Purchased != 0 {
		t.Fatalf("refund did not reverse unused purchased balance: %+v", balance)
	}
}

func TestOneTimeBillingProjectorWaitsForAsyncPaidCheckout(t *testing.T) {
	if _, found, err := parsePurchaseEvidence([]byte(`{"data":{"object":{"id":"cs_pending","mode":"payment","payment_status":"unpaid"}}}`)); err != nil || found {
		t.Fatalf("found=%v error=%v", found, err)
	}
}

func TestOneTimeBillingProjectorIgnoresSubscriptionCheckout(t *testing.T) {
	if _, found, err := parsePurchaseEvidence([]byte(`{"data":{"object":{"id":"cs_subscription","mode":"subscription","payment_status":"paid"}}}`)); err != nil || found {
		t.Fatalf("found=%v error=%v", found, err)
	}
}

func TestPurchaseRefundParsesPartialEvidenceForLocalPurchaseClassification(t *testing.T) {
	partial, err := parsePurchaseRefund([]byte(`{"data":{"object":{"id":"ch_refunded_1","payment_intent":"pi_paid_1","amount":1000,"amount_refunded":500,"refunded":false}}}`))
	if err != nil || partial.Refunded || partial.AmountRefunded != 500 {
		t.Fatalf("partial=%+v error=%v", partial, err)
	}
	if _, err := parsePurchaseRefund([]byte(`{"data":{"object":{"id":"ch_refunded_1","payment_intent":"pi_paid_1","amount":1000,"amount_refunded":1500,"refunded":true}}}`)); err != ErrInvalidPurchaseEvidence {
		t.Fatalf("over-refund error=%v", err)
	}
}

func TestPartialRefundOnlyFailsWhenItMatchesALocalTopUp(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	ledger := memory.NewAITokenLedger()
	issuer, err := aitokenledger.NewIssuer(ledger, func() catalog.PublishedCatalog { return catalog.Default(now) }, ids.RandomGenerator{}, purchaseClock{now})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"data":{"object":{"id":"ch_refunded_1","payment_intent":"pi_paid_1","amount":1000,"amount_refunded":500,"refunded":false}}}`)
	store := &purchaseProjectionStore{out: PurchaseProjection{PaymentIntentID: "pi_paid_1"}}
	projector, _ := NewBillingEventProjector(store, issuer)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "charge.refunded"}, Payload: payload}); err != nil {
		t.Fatalf("unrelated partial Refund blocked another projector: %v", err)
	}
	store.refundFound = true
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "charge.refunded"}, Payload: payload}); err != ErrInvalidPurchaseEvidence {
		t.Fatalf("local top-up partial Refund error = %v", err)
	}
}

func TestSubscriptionCommissioningRequiresPaidInvoiceAndMappedPriceEvidence(t *testing.T) {
	payload := []byte(`{"data":{"object":{"id":"in_paid_1","amount_paid":30000,"paid":true,"status":"paid","parent":{"subscription_details":{"metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001","spyglass_checkout_request_id":"20000000-0000-4000-8000-000000000002","spyglass_commissioning_code":"commissioning_v1","spyglass_commissioning_version":"1"}}},"lines":{"data":[{"pricing":{"price_details":{"price":"price_commissioning"}}}]}}}}`)
	evidence, found, err := parseSubscriptionCommissioning(payload)
	if err != nil || !found || evidence.InvoiceID != "in_paid_1" || evidence.ItemCode != "commissioning_v1" || len(evidence.ProviderPriceIDs) != 1 || evidence.ProviderPriceIDs[0] != "price_commissioning" {
		t.Fatalf("evidence=%+v found=%v err=%v", evidence, found, err)
	}
	withoutCommissioning := []byte(`{"data":{"object":{"id":"in_paid_2","amount_paid":5000,"paid":true,"status":"paid","parent":{"subscription_details":{"metadata":{"spyglass_account_id":"10000000-0000-4000-8000-000000000001"}}}}}}`)
	if _, found, err := parseSubscriptionCommissioning(withoutCommissioning); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}
