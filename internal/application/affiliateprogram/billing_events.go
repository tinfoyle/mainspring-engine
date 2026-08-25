package affiliateprogram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalidInvoiceEvidence = errors.New("Affiliate invoice evidence is invalid")

type BillingEventProjector struct{ service *Service }

func NewBillingEventProjector(service *Service) (*BillingEventProjector, error) {
	if service == nil {
		return nil, errors.New("Affiliate billing service is required")
	}
	return &BillingEventProjector{service: service}, nil
}

func (p *BillingEventProjector) Project(ctx context.Context, item billing.WorkItem) error {
	switch item.Entry.EventType {
	case "invoice.paid":
		evidence, err := parsePaidInvoice(item.Payload)
		if err != nil {
			return err
		}
		if evidence.BillingReason != "subscription_create" && evidence.BillingReason != "subscription_cycle" {
			return nil
		}
		// A legacy or externally paid Invoice can legitimately have no
		// PaymentIntent. It cannot match this PaymentIntent-bound Affiliate
		// ledger and must not poison the shared billing inbox.
		if evidence.PaymentIntentID == "" {
			return nil
		}
		_, err = p.service.RecordPaidInvoice(ctx, PaidInvoice{SubscriptionID: evidence.SubscriptionID,
			InvoiceID: evidence.InvoiceID, PaymentIntentID: evidence.PaymentIntentID, AmountPaidMinor: evidence.AmountPaidMinor,
			Currency: evidence.Currency, Initial: evidence.BillingReason == "subscription_create"})
		if errors.Is(err, ErrAttributionNotFound) || errors.Is(err, ErrInvoiceIneligible) || errors.Is(err, affiliates.ErrInvalidCommission) {
			return nil
		}
		return err
	case "refund.created", "refund.updated", "charge.dispute.closed":
		evidence, applies, err := parseAdverseBilling(item)
		if err != nil {
			return err
		}
		if !applies {
			return nil
		}
		_, _, err = p.service.RecordAdverseBilling(ctx, evidence)
		return err
	default:
		return nil
	}
}

type paidInvoiceEvidence struct {
	SubscriptionID  string
	InvoiceID       string
	PaymentIntentID string
	AmountPaidMinor int64
	Currency        string
	BillingReason   string
}

func parsePaidInvoice(payload []byte) (paidInvoiceEvidence, error) {
	var event struct {
		Data struct {
			Object struct {
				ID            string          `json:"id"`
				Subscription  json.RawMessage `json:"subscription"`
				AmountPaid    int64           `json:"amount_paid"`
				Currency      string          `json:"currency"`
				BillingReason string          `json:"billing_reason"`
				Paid          bool            `json:"paid"`
				Status        string          `json:"status"`
				PaymentIntent json.RawMessage `json:"payment_intent"`
				Payments      struct {
					Data []struct {
						Payment struct {
							PaymentIntent json.RawMessage `json:"payment_intent"`
						} `json:"payment"`
					} `json:"data"`
				} `json:"payments"`
				Parent struct {
					SubscriptionDetails struct {
						Subscription json.RawMessage `json:"subscription"`
					} `json:"subscription_details"`
				} `json:"parent"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return paidInvoiceEvidence{}, ErrInvalidInvoiceEvidence
	}
	invoice := event.Data.Object
	subscriptionID := providerID(invoice.Parent.SubscriptionDetails.Subscription, "sub_")
	if subscriptionID == "" {
		subscriptionID = providerID(invoice.Subscription, "sub_")
	}
	currency := strings.ToUpper(invoice.Currency)
	paymentIntentID := providerID(invoice.PaymentIntent, "pi_")
	if paymentIntentID == "" {
		for _, payment := range invoice.Payments.Data {
			paymentIntentID = providerID(payment.Payment.PaymentIntent, "pi_")
			if paymentIntentID != "" {
				break
			}
		}
	}
	if !strings.HasPrefix(invoice.ID, "in_") || subscriptionID == "" || invoice.AmountPaid <= 0 ||
		len(currency) != 3 || !invoice.Paid || invoice.Status != "paid" || invoice.BillingReason == "" {
		return paidInvoiceEvidence{}, ErrInvalidInvoiceEvidence
	}
	return paidInvoiceEvidence{SubscriptionID: subscriptionID, InvoiceID: invoice.ID,
		PaymentIntentID: paymentIntentID, AmountPaidMinor: invoice.AmountPaid, Currency: currency, BillingReason: invoice.BillingReason}, nil
}

func parseAdverseBilling(item billing.WorkItem) (affiliates.AdverseBillingEvidence, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID            string          `json:"id"`
				Amount        int64           `json:"amount"`
				Currency      string          `json:"currency"`
				PaymentIntent json.RawMessage `json:"payment_intent"`
				Status        string          `json:"status"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(item.Payload, &event) != nil {
		return affiliates.AdverseBillingEvidence{}, false, ErrInvalidInvoiceEvidence
	}
	object := event.Data.Object
	kind := affiliates.AdverseRefund
	applies := object.Status == "succeeded"
	if item.Entry.EventType == "charge.dispute.closed" {
		kind = affiliates.AdverseDispute
		applies = object.Status == "lost"
	}
	if !applies {
		return affiliates.AdverseBillingEvidence{}, false, nil
	}
	paymentIntentID := providerID(object.PaymentIntent, "pi_")
	if paymentIntentID == "" {
		// Refunds and disputes for legacy non-PaymentIntent charges cannot
		// match an Affiliate earning and are valid no-ops for this projector.
		return affiliates.AdverseBillingEvidence{}, false, nil
	}
	evidence := affiliates.AdverseBillingEvidence{EventID: ids.AffiliateProviderEventID(item.Entry.ProviderEventID), Kind: kind,
		ProviderObjectID: object.ID, PaymentIntentID: paymentIntentID, AmountMinor: object.Amount,
		Currency: strings.ToUpper(object.Currency), OccurredAt: item.Entry.ProviderCreatedAt.UTC()}
	if evidence.Validate() != nil {
		return affiliates.AdverseBillingEvidence{}, false, ErrInvalidInvoiceEvidence
	}
	return evidence, true, nil
}

func providerID(raw json.RawMessage, prefix string) string {
	var value string
	if json.Unmarshal(raw, &value) == nil && strings.HasPrefix(value, prefix) {
		return value
	}
	var expanded struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &expanded) == nil && strings.HasPrefix(expanded.ID, prefix) {
		return expanded.ID
	}
	return ""
}

var _ billing.EventHandler = (*BillingEventProjector)(nil)
