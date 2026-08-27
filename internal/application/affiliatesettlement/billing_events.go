package affiliatesettlement

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
)

type BillingEventProjector struct{ service *Service }

func NewBillingEventProjector(service *Service) (*BillingEventProjector, error) {
	if service == nil {
		return nil, ErrInvalidInvoice
	}
	return &BillingEventProjector{service: service}, nil
}

func (p *BillingEventProjector) Project(ctx context.Context, item billing.WorkItem) error {
	switch item.Entry.EventType {
	case "invoice.created":
		evidence, applies, err := parseCreatedInvoice(item.Payload)
		if err != nil || !applies {
			return err
		}
		if item.Entry.AccountID == "" {
			return nil
		}
		_, err = p.service.CreditInvoice(ctx, InvoiceCreditCommand{AccountID: item.Entry.AccountID,
			ProviderInvoiceID: evidence.InvoiceID, ProviderCustomerID: evidence.CustomerID,
			AmountDueMinor: evidence.AmountDueMinor, Currency: evidence.Currency})
		return err
	case "invoice.paid":
		invoiceID, paid, err := parsePaidInvoice(item.Payload)
		if err != nil || !paid {
			return err
		}
		if _, err = p.service.SettleInvoice(ctx, invoiceID); err != nil {
			return err
		}
		return p.service.ReconcileInvoiceAdversity(ctx, invoiceID)
	case "refund.created", "refund.updated", "charge.dispute.closed", "credit_note.created", "credit_note.updated":
		providerObjectID, applies, err := parseAdverseObject(item.Entry.EventType, item.Payload)
		if err != nil || !applies {
			return err
		}
		_, err = p.service.ReconcileAdverse(ctx, providerObjectID)
		return err
	default:
		return nil
	}
}

func parseAdverseObject(eventType string, payload []byte) (string, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID     string `json:"id"`
				Amount int64  `json:"amount"`
				Status string `json:"status"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return "", false, ErrInvalidInvoice
	}
	object := event.Data.Object
	prefix, applies := "re_", object.Status == "succeeded"
	if eventType == "charge.dispute.closed" {
		prefix, applies = "dp_", object.Status == "lost"
	} else if eventType == "credit_note.created" || eventType == "credit_note.updated" {
		prefix, applies = "cn_", object.Status == "issued"
	}
	if !applies {
		return "", false, nil
	}
	if !strings.HasPrefix(object.ID, prefix) || object.Amount <= 0 || len(object.ID) > 200 || strings.ContainsAny(object.ID, "\r\n\t ") {
		return "", false, ErrInvalidInvoice
	}
	return object.ID, true, nil
}

type createdInvoiceEvidence struct {
	InvoiceID      string
	CustomerID     string
	AmountDueMinor int64
	Currency       string
}

func parseCreatedInvoice(payload []byte) (createdInvoiceEvidence, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID            string `json:"id"`
				Customer      string `json:"customer"`
				AmountDue     int64  `json:"amount_due"`
				Total         int64  `json:"total"`
				Currency      string `json:"currency"`
				Status        string `json:"status"`
				BillingReason string `json:"billing_reason"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return createdInvoiceEvidence{}, false, ErrInvalidInvoice
	}
	invoice := event.Data.Object
	if invoice.BillingReason != "subscription_create" && invoice.BillingReason != "subscription_cycle" {
		return createdInvoiceEvidence{}, false, nil
	}
	amount := invoice.Total
	if amount <= 0 {
		amount = invoice.AmountDue
	}
	currency := strings.ToUpper(invoice.Currency)
	if !strings.HasPrefix(invoice.ID, "in_") || !strings.HasPrefix(invoice.Customer, "cus_") || amount <= 0 || len(currency) != 3 || (invoice.Status != "draft" && invoice.Status != "open") {
		return createdInvoiceEvidence{}, false, ErrInvalidInvoice
	}
	return createdInvoiceEvidence{InvoiceID: invoice.ID, CustomerID: invoice.Customer, AmountDueMinor: amount, Currency: currency}, true, nil
}

func parsePaidInvoice(payload []byte) (string, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID     string `json:"id"`
				Paid   bool   `json:"paid"`
				Status string `json:"status"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil || !strings.HasPrefix(event.Data.Object.ID, "in_") {
		return "", false, ErrInvalidInvoice
	}
	return event.Data.Object.ID, event.Data.Object.Paid && event.Data.Object.Status == "paid", nil
}

var _ billing.EventHandler = (*BillingEventProjector)(nil)
