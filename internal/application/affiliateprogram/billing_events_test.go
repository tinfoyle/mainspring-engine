package affiliateprogram_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestPaidRenewalEventAppendsCommissionWithoutCustomerData(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{rule: affiliates.CommissionRule{ID: "10000000-0000-4000-8000-000000000020", Version: 3,
		OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000,
		InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}}
	repository.attribution = affiliates.Attribution{ID: "10000000-0000-4000-8000-000000000011", AffiliateID: "10000000-0000-4000-8000-000000000010",
		OfferCode: "team-monthly-v1", RuleVersion: 3, State: affiliates.AttributionLocked, SubscriptionID: "sub_paid"}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000012", "10000000-0000-4000-8000-000000000013"}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	payload := []byte(`{"data":{"object":{"id":"in_renewal","payment_intent":"pi_renewal","amount_paid":5000,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","parent":{"subscription_details":{"subscription":"sub_paid"}},"lines":{"data":[{"id":"il_renewal","amount":5000,"currency":"usd","pricing":{"price_details":{"price":"price_team"}}}]}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", Mode: "test", ProviderCreatedAt: now}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(repository.entries) != 1 || repository.entries[0].AmountMinor != 1000 || repository.entries[0].Cycle != 2 {
		t.Fatalf("entries=%+v", repository.entries)
	}
}

func TestPaidInvoiceWithoutAttributionEarnsNothing(t *testing.T) {
	now := time.Now().UTC()
	repository := &repository{rule: affiliates.CommissionRule{ID: ids.CommissionRuleID("10000000-0000-4000-8000-000000000020"), Version: 3, OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000, EffectiveFrom: now}}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000012", "10000000-0000-4000-8000-000000000013"}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	for _, payload := range [][]byte{
		[]byte(`{"data":{"object":{"id":"in_plain","payment_intent":"pi_plain","amount_paid":5000,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","subscription":"sub_missing","lines":{"data":[{"id":"il_plain","amount":5000,"currency":"usd","price":{"id":"price_team"}}]}}}}`),
		[]byte(`{"data":{"object":{"id":"in_proration","payment_intent":"pi_proration","amount_paid":4900,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","subscription":"sub_paid","lines":{"data":[{"id":"il_proration","amount":2500,"currency":"usd","price":{"id":"price_team"}}]}}}}`),
	} {
		if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", Mode: "test", ProviderCreatedAt: now}, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if len(repository.entries) != 0 {
		t.Fatalf("entries=%+v", repository.entries)
	}
}

func TestSuccessfulRefundProjectsImmutableCommissionReversalEvidence(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &repository{rule: affiliates.CommissionRule{ID: "10000000-0000-4000-8000-000000000020", Version: 3,
		OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000,
		InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}}
	repository.attribution = affiliates.Attribution{ID: "10000000-0000-4000-8000-000000000011", AffiliateID: "10000000-0000-4000-8000-000000000010",
		OfferCode: "team-monthly-v1", RuleVersion: 3, State: affiliates.AttributionLocked, SubscriptionID: "sub_paid"}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{
		"10000000-0000-4000-8000-000000000012", "10000000-0000-4000-8000-000000000013", "10000000-0000-4000-8000-000000000014",
	}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	invoice := []byte(`{"data":{"object":{"id":"in_renewal","amount_paid":5000,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","parent":{"subscription_details":{"subscription":"sub_paid"}},"payments":{"data":[{"payment":{"payment_intent":"pi_renewal"}}]},"lines":{"data":[{"id":"il_renewal","amount":5000,"currency":"usd","pricing":{"price_details":{"price":"price_team"}}}]}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", Mode: "test", ProviderCreatedAt: now}, Payload: invoice}); err != nil {
		t.Fatal(err)
	}
	refund := []byte(`{"data":{"object":{"id":"re_full","amount":5000,"currency":"usd","payment_intent":"pi_renewal","status":"succeeded"}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{ProviderEventID: "evt_refund", EventType: "refund.created", ProviderCreatedAt: now}, Payload: refund}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(repository.entries) != 2 || repository.entries[1].Kind != affiliates.CommissionReversal || repository.entries[1].ReversesID == nil {
		t.Fatalf("entries=%+v", repository.entries)
	}
}

func TestLostDisputeProjectsImmutableCommissionReversalEvidence(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &repository{rule: affiliates.CommissionRule{ID: "10000000-0000-4000-8000-000000000020", Version: 3,
		OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000,
		InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}}
	repository.attribution = affiliates.Attribution{ID: "10000000-0000-4000-8000-000000000011", AffiliateID: "10000000-0000-4000-8000-000000000010",
		OfferCode: "team-monthly-v1", RuleVersion: 3, State: affiliates.AttributionLocked, SubscriptionID: "sub_paid"}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{
		"10000000-0000-4000-8000-000000000012", "10000000-0000-4000-8000-000000000013", "10000000-0000-4000-8000-000000000014",
	}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	invoice := []byte(`{"data":{"object":{"id":"in_renewal","payment_intent":"pi_renewal","amount_paid":5000,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","parent":{"subscription_details":{"subscription":"sub_paid"}},"lines":{"data":[{"id":"il_renewal","amount":5000,"currency":"usd","pricing":{"price_details":{"price":"price_team"}}}]}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", Mode: "test", ProviderCreatedAt: now}, Payload: invoice}); err != nil {
		t.Fatal(err)
	}
	dispute := []byte(`{"data":{"object":{"id":"dp_lost","amount":5000,"currency":"usd","payment_intent":"pi_renewal","status":"lost"}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{ProviderEventID: "evt_dispute", EventType: "charge.dispute.closed", ProviderCreatedAt: now}, Payload: dispute}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(repository.entries) != 2 || repository.entries[1].Kind != affiliates.CommissionReversal || repository.entries[1].ReversesID == nil {
		t.Fatalf("entries=%+v", repository.entries)
	}
}

func TestIssuedCreditNoteOnEligibleInvoiceLineProjectsReversal(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &repository{rule: affiliates.CommissionRule{ID: "10000000-0000-4000-8000-000000000020", Version: 3,
		OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000,
		InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}}
	repository.attribution = affiliates.Attribution{ID: "10000000-0000-4000-8000-000000000011", AffiliateID: "10000000-0000-4000-8000-000000000010",
		OfferCode: "team-monthly-v1", RuleVersion: 3, State: affiliates.AttributionLocked, SubscriptionID: "sub_paid"}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{
		"10000000-0000-4000-8000-000000000012", "10000000-0000-4000-8000-000000000013", "10000000-0000-4000-8000-000000000014",
	}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	invoice := []byte(`{"data":{"object":{"id":"in_renewal","payment_intent":"pi_renewal","amount_paid":5000,"currency":"usd","billing_reason":"subscription_cycle","paid":true,"status":"paid","parent":{"subscription_details":{"subscription":"sub_paid"}},"lines":{"data":[{"id":"il_renewal","amount":5000,"currency":"usd","pricing":{"price_details":{"price":"price_team"}}}]}}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid", Mode: "test", ProviderCreatedAt: now}, Payload: invoice}); err != nil {
		t.Fatal(err)
	}
	creditNote := []byte(`{"data":{"object":{"id":"cn_eligible","invoice":"in_renewal","amount":1,"currency":"usd","status":"issued","lines":{"has_more":false,"data":[{"amount":1,"type":"invoice_line_item","invoice_line_item":"il_renewal"}]}}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{ProviderEventID: "evt_credit_note", EventType: "credit_note.created", ProviderCreatedAt: now}, Payload: creditNote}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(repository.entries) != 2 || repository.entries[1].Kind != affiliates.CommissionReversal {
		t.Fatalf("entries=%+v", repository.entries)
	}
}

func TestCreditNoteWithIncompleteLinePageFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	service, _ := affiliateprogram.New(&repository{}, &generator{}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	payload := []byte(`{"data":{"object":{"id":"cn_more","invoice":"in_renewal","amount":1,"currency":"usd","status":"issued","lines":{"has_more":true,"data":[]}}}}`)
	err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{ProviderEventID: "evt_credit_note", EventType: "credit_note.updated", ProviderCreatedAt: now}, Payload: payload})
	if !errors.Is(err, affiliateprogram.ErrInvalidInvoiceEvidence) {
		t.Fatalf("incomplete credit-note lines error=%v", err)
	}
}

func TestNonFinalRefundAndWonDisputeDoNotReverse(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &repository{}
	service, _ := affiliateprogram.New(repository, &generator{}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	projector, _ := affiliateprogram.NewBillingEventProjector(service)
	items := []billing.WorkItem{
		{Entry: billing.InboxEntry{ProviderEventID: "evt_pending", EventType: "refund.updated", ProviderCreatedAt: now}, Payload: []byte(`{"data":{"object":{"id":"re_pending","amount":5000,"currency":"usd","payment_intent":"pi_paid","status":"pending"}}}`)},
		{Entry: billing.InboxEntry{ProviderEventID: "evt_won", EventType: "charge.dispute.closed", ProviderCreatedAt: now}, Payload: []byte(`{"data":{"object":{"id":"dp_won","amount":5000,"currency":"usd","payment_intent":"pi_paid","status":"won"}}}`)},
		{Entry: billing.InboxEntry{ProviderEventID: "evt_legacy", EventType: "refund.created", ProviderCreatedAt: now}, Payload: []byte(`{"data":{"object":{"id":"re_legacy","amount":5000,"currency":"usd","payment_intent":null,"status":"succeeded"}}}`)},
	}
	for _, item := range items {
		if err := projector.Project(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	if len(repository.entries) != 0 {
		t.Fatalf("entries=%+v", repository.entries)
	}
}
