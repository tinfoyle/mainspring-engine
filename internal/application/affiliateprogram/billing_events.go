package affiliateprogram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
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
	if item.Entry.EventType != "invoice.paid" {
		return nil
	}
	evidence, err := parsePaidInvoice(item.Payload)
	if err != nil {
		return err
	}
	if evidence.BillingReason != "subscription_create" && evidence.BillingReason != "subscription_cycle" {
		return nil
	}
	_, err = p.service.RecordPaidInvoice(ctx, PaidInvoice{SubscriptionID: evidence.SubscriptionID,
		InvoiceID: evidence.InvoiceID, AmountPaidMinor: evidence.AmountPaidMinor, Currency: evidence.Currency,
		Initial: evidence.BillingReason == "subscription_create"})
	if errors.Is(err, ErrAttributionNotFound) || errors.Is(err, ErrInvoiceIneligible) || errors.Is(err, affiliates.ErrInvalidCommission) {
		return nil
	}
	return err
}

type paidInvoiceEvidence struct {
	SubscriptionID  string
	InvoiceID       string
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
				Parent        struct {
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
	if !strings.HasPrefix(invoice.ID, "in_") || subscriptionID == "" || invoice.AmountPaid <= 0 ||
		len(currency) != 3 || !invoice.Paid || invoice.Status != "paid" || invoice.BillingReason == "" {
		return paidInvoiceEvidence{}, ErrInvalidInvoiceEvidence
	}
	return paidInvoiceEvidence{SubscriptionID: subscriptionID, InvoiceID: invoice.ID,
		AmountPaidMinor: invoice.AmountPaid, Currency: currency, BillingReason: invoice.BillingReason}, nil
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
