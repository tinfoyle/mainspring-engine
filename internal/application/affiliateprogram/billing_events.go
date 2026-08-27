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
		_, err = p.service.RecordPaidInvoice(ctx, PaidInvoice{SubscriptionID: evidence.SubscriptionID,
			InvoiceID: evidence.InvoiceID, PaymentIntentID: evidence.PaymentIntentID, AmountPaidMinor: evidence.AmountPaidMinor,
			PaymentIntentIDs: evidence.PaymentIntentIDs, Currency: evidence.Currency, Initial: evidence.BillingReason == "subscription_create",
			Mode: item.Entry.Mode, OccurredAt: item.Entry.ProviderCreatedAt, Lines: evidence.Lines})
		if errors.Is(err, ErrAttributionNotFound) || errors.Is(err, ErrInvoiceIneligible) || errors.Is(err, affiliates.ErrInvalidCommission) {
			return nil
		}
		return err
	case "refund.created", "refund.updated", "charge.dispute.closed", "credit_note.created", "credit_note.updated":
		evidence, applies, err := parseAdverseBilling(item)
		if err != nil {
			return err
		}
		if !applies {
			return nil
		}
		_, _, err = p.service.RecordAdverseBilling(ctx, evidence)
		return err
	case "customer.subscription.deleted":
		subscriptionID, err := parseTerminatedSubscription(item.Payload)
		if err != nil {
			return err
		}
		_, _, err = p.service.RecordSubscriptionTermination(ctx, subscriptionID, item.Entry.ProviderCreatedAt)
		return err
	default:
		return nil
	}
}

type paidInvoiceEvidence struct {
	SubscriptionID   string
	InvoiceID        string
	PaymentIntentID  string
	PaymentIntentIDs []string
	AmountPaidMinor  int64
	Currency         string
	BillingReason    string
	Lines            []InvoiceLine
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
				Lines struct {
					Data []struct {
						ID       string `json:"id"`
						Amount   int64  `json:"amount"`
						Currency string `json:"currency"`
						Pricing  struct {
							PriceDetails struct {
								Price string `json:"price"`
							} `json:"price_details"`
						} `json:"pricing"`
						Price struct {
							ID string `json:"id"`
						} `json:"price"`
					} `json:"data"`
				} `json:"lines"`
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
	paymentIntentIDs := make([]string, 0, len(invoice.Payments.Data)+1)
	seenPaymentIntents := map[string]struct{}{}
	appendPaymentIntent := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seenPaymentIntents[value]; exists {
			return
		}
		seenPaymentIntents[value] = struct{}{}
		paymentIntentIDs = append(paymentIntentIDs, value)
	}
	appendPaymentIntent(providerID(invoice.PaymentIntent, "pi_"))
	for _, payment := range invoice.Payments.Data {
		appendPaymentIntent(providerID(payment.Payment.PaymentIntent, "pi_"))
	}
	lines := make([]InvoiceLine, 0, len(invoice.Lines.Data))
	for _, raw := range invoice.Lines.Data {
		priceID := raw.Pricing.PriceDetails.Price
		if priceID == "" {
			priceID = raw.Price.ID
		}
		currency := strings.ToUpper(raw.Currency)
		if !strings.HasPrefix(raw.ID, "il_") || !strings.HasPrefix(priceID, "price_") || raw.Amount == 0 || (currency != "" && len(currency) != 3) {
			return paidInvoiceEvidence{}, ErrInvalidInvoiceEvidence
		}
		lines = append(lines, InvoiceLine{ID: raw.ID, ProviderPriceID: priceID, AmountMinor: raw.Amount, Currency: currency})
	}
	if !strings.HasPrefix(invoice.ID, "in_") || subscriptionID == "" || invoice.AmountPaid <= 0 ||
		len(currency) != 3 || !invoice.Paid || invoice.Status != "paid" || invoice.BillingReason == "" {
		return paidInvoiceEvidence{}, ErrInvalidInvoiceEvidence
	}
	firstPaymentIntent := ""
	if len(paymentIntentIDs) > 0 {
		firstPaymentIntent = paymentIntentIDs[0]
	}
	return paidInvoiceEvidence{SubscriptionID: subscriptionID, InvoiceID: invoice.ID,
		PaymentIntentID: firstPaymentIntent, PaymentIntentIDs: paymentIntentIDs, AmountPaidMinor: invoice.AmountPaid, Currency: currency, BillingReason: invoice.BillingReason, Lines: lines}, nil
}

func parseTerminatedSubscription(payload []byte) (string, error) {
	var event struct {
		Data struct {
			Object struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil || !strings.HasPrefix(event.Data.Object.ID, "sub_") || (event.Data.Object.Status != "canceled" && event.Data.Object.Status != "incomplete_expired") {
		return "", ErrInvalidInvoiceEvidence
	}
	return event.Data.Object.ID, nil
}

func parseAdverseBilling(item billing.WorkItem) (affiliates.AdverseBillingEvidence, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID            string          `json:"id"`
				Amount        int64           `json:"amount"`
				Currency      string          `json:"currency"`
				PaymentIntent json.RawMessage `json:"payment_intent"`
				Invoice       json.RawMessage `json:"invoice"`
				Status        string          `json:"status"`
				Lines         struct {
					HasMore bool `json:"has_more"`
					Data    []struct {
						Amount          int64  `json:"amount"`
						Type            string `json:"type"`
						InvoiceLineItem string `json:"invoice_line_item"`
					} `json:"data"`
				} `json:"lines"`
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
	} else if item.Entry.EventType == "credit_note.created" || item.Entry.EventType == "credit_note.updated" {
		kind = affiliates.AdverseCreditNote
		applies = object.Status == "issued"
	}
	if !applies {
		return affiliates.AdverseBillingEvidence{}, false, nil
	}
	paymentIntentID := providerID(object.PaymentIntent, "pi_")
	invoiceID := ""
	invoiceLineIDs := []string(nil)
	if kind == affiliates.AdverseCreditNote {
		if object.Lines.HasMore {
			return affiliates.AdverseBillingEvidence{}, false, ErrInvalidInvoiceEvidence
		}
		invoiceID = providerID(object.Invoice, "in_")
		for _, line := range object.Lines.Data {
			if line.Type != "invoice_line_item" || line.Amount <= 0 {
				continue
			}
			if !strings.HasPrefix(line.InvoiceLineItem, "il_") {
				return affiliates.AdverseBillingEvidence{}, false, ErrInvalidInvoiceEvidence
			}
			invoiceLineIDs = append(invoiceLineIDs, line.InvoiceLineItem)
		}
		if invoiceID == "" || len(invoiceLineIDs) == 0 {
			return affiliates.AdverseBillingEvidence{}, false, nil
		}
	}
	if paymentIntentID == "" {
		// Refunds and disputes for legacy non-PaymentIntent charges cannot
		// match an Affiliate earning and are valid no-ops for this projector.
		if kind != affiliates.AdverseCreditNote {
			return affiliates.AdverseBillingEvidence{}, false, nil
		}
	}
	evidence := affiliates.AdverseBillingEvidence{EventID: ids.AffiliateProviderEventID(item.Entry.ProviderEventID), Kind: kind,
		ProviderObjectID: object.ID, PaymentIntentID: paymentIntentID, AmountMinor: object.Amount,
		InvoiceID: invoiceID, InvoiceLineIDs: invoiceLineIDs, Currency: strings.ToUpper(object.Currency), OccurredAt: item.Entry.ProviderCreatedAt.UTC()}
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
