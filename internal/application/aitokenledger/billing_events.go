package aitokenledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalidBillingEvidence = errors.New("AI Token billing evidence is invalid")

// BillingEventProjector appends one included grant for each eligible, positive
// paid service-period invoice. The ledger source key is the immutable provider
// invoice ID, so retries and webhook reordering cannot duplicate issuance.
type BillingEventProjector struct{ issuer *Issuer }

func NewBillingEventProjector(issuer *Issuer) (*BillingEventProjector, error) {
	if issuer == nil {
		return nil, errors.New("AI Token billing issuer is required")
	}
	return &BillingEventProjector{issuer: issuer}, nil
}

func (p *BillingEventProjector) Project(ctx context.Context, item billing.WorkItem) error {
	if item.Entry.EventType != "invoice.paid" {
		return nil
	}
	invoice, err := parseTokenInvoice(item.Payload)
	if err != nil {
		return err
	}
	if invoice.BillingReason != "subscription_create" && invoice.BillingReason != "subscription_cycle" {
		return nil
	}
	accountID := item.Entry.AccountID
	if accountID == "" {
		accountID = invoice.AccountID
	}
	if accountID == "" {
		// Old external Stripe subscriptions may not carry the Account metadata.
		// They cannot be safely credited and must be reconciled explicitly.
		return nil
	}
	_, _, err = p.issuer.IssueIncluded(ctx, accountID, invoice.InvoiceID)
	return err
}

type tokenInvoice struct {
	InvoiceID     string
	BillingReason string
	AccountID     ids.AccountID
}

func parseTokenInvoice(payload []byte) (tokenInvoice, error) {
	var event struct {
		Data struct {
			Object struct {
				ID            string `json:"id"`
				AmountPaid    int64  `json:"amount_paid"`
				BillingReason string `json:"billing_reason"`
				Paid          bool   `json:"paid"`
				Status        string `json:"status"`
				Metadata      struct {
					AccountID string `json:"spyglass_account_id"`
				} `json:"metadata"`
				Parent struct {
					SubscriptionDetails struct {
						Metadata struct {
							AccountID string `json:"spyglass_account_id"`
						} `json:"metadata"`
					} `json:"subscription_details"`
				} `json:"parent"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return tokenInvoice{}, ErrInvalidBillingEvidence
	}
	invoice := event.Data.Object
	if !strings.HasPrefix(invoice.ID, "in_") || invoice.AmountPaid <= 0 || !invoice.Paid || invoice.Status != "paid" || invoice.BillingReason == "" {
		return tokenInvoice{}, ErrInvalidBillingEvidence
	}
	rawAccountID := invoice.Parent.SubscriptionDetails.Metadata.AccountID
	if rawAccountID == "" {
		rawAccountID = invoice.Metadata.AccountID
	}
	var accountID ids.AccountID
	if ids.Validate(rawAccountID) == nil {
		accountID = ids.AccountID(rawAccountID)
	}
	return tokenInvoice{InvoiceID: invoice.ID, BillingReason: invoice.BillingReason, AccountID: accountID}, nil
}

var _ billing.EventHandler = (*BillingEventProjector)(nil)
