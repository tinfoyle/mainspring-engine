package commercialaccess

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalidPurchaseEvidence = errors.New("one-time purchase evidence is invalid")

type PurchaseEvidence struct {
	AccountID               ids.AccountID
	RequestID               string
	ProviderSessionID       string
	ProviderPaymentIntentID string
	Kind                    billing.PurchaseKind
	ItemCode                string
	ItemVersion             uint64
	CatalogVersion          uint64
	Currency                string
	AmountSubtotal          int64
}

type PurchaseProjection struct {
	Snapshot        PurchaseSnapshot
	PaymentIntentID string
}

type PurchaseProjectionStore interface {
	ProjectOneTimePurchase(context.Context, PurchaseEvidence) (PurchaseProjection, error)
	ProjectPurchaseRefund(context.Context, PurchaseRefundEvidence) (PurchaseProjection, bool, error)
	ProjectSubscriptionCommissioning(context.Context, SubscriptionCommissioningEvidence) (PurchaseProjection, bool, error)
}

type PurchaseRefundEvidence struct {
	PaymentIntentID string
	Reference       string
	Amount          int64
	AmountRefunded  int64
	Refunded        bool
}

type SubscriptionCommissioningEvidence struct {
	AccountID        ids.AccountID
	RequestID        string
	InvoiceID        string
	ItemCode         string
	ItemVersion      uint64
	ProviderPriceIDs []string
}

// BillingEventProjector validates signed Checkout evidence against the local
// immutable attempt snapshot. Provider metadata selects a row; it never
// supplies price, quantity or fulfillment authority by itself.
type BillingEventProjector struct {
	store  PurchaseProjectionStore
	issuer *aitokenledger.Issuer
}

func NewBillingEventProjector(store PurchaseProjectionStore, issuer *aitokenledger.Issuer) (*BillingEventProjector, error) {
	if store == nil || issuer == nil {
		return nil, errors.New("one-time billing projector dependencies are required")
	}
	return &BillingEventProjector{store: store, issuer: issuer}, nil
}

func (p *BillingEventProjector) Project(ctx context.Context, item billing.WorkItem) error {
	if item.Entry.EventType == "invoice.paid" {
		evidence, found, err := parseSubscriptionCommissioning(item.Payload)
		if err != nil || !found {
			return err
		}
		if item.Entry.AccountID != "" && item.Entry.AccountID != evidence.AccountID {
			return ErrInvalidPurchaseEvidence
		}
		_, _, err = p.store.ProjectSubscriptionCommissioning(ctx, evidence)
		return err
	}
	if item.Entry.EventType == "charge.refunded" {
		evidence, err := parsePurchaseRefund(item.Payload)
		if err != nil {
			return err
		}
		projection, found, err := p.store.ProjectPurchaseRefund(ctx, evidence)
		if err != nil || !found {
			return err
		}
		_, _, err = p.issuer.ReversePurchased(ctx, projection.Snapshot.AccountID, evidence.PaymentIntentID, evidence.Reference)
		return err
	}
	if item.Entry.EventType != "checkout.session.completed" && item.Entry.EventType != "checkout.session.async_payment_succeeded" {
		return nil
	}
	evidence, found, err := parsePurchaseEvidence(item.Payload)
	if err != nil || !found {
		return err
	}
	if evidence.Kind != billing.PurchaseAITokenTopUp && evidence.Kind != billing.PurchaseCommissioning {
		return nil
	}
	if item.Entry.AccountID != "" && item.Entry.AccountID != evidence.AccountID {
		return ErrInvalidPurchaseEvidence
	}
	projection, err := p.store.ProjectOneTimePurchase(ctx, evidence)
	if err != nil {
		return err
	}
	if projection.Snapshot.Kind != billing.PurchaseAITokenTopUp {
		return nil
	}
	_, _, err = p.issuer.IssuePurchasedSnapshot(ctx, projection.Snapshot.AccountID, projection.Snapshot.ItemCode, projection.Snapshot.ItemVersion, projection.Snapshot.CatalogVersion, projection.Snapshot.Quantity, projection.PaymentIntentID)
	return err
}

func parseSubscriptionCommissioning(payload []byte) (SubscriptionCommissioningEvidence, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID         string `json:"id"`
				AmountPaid int64  `json:"amount_paid"`
				Paid       bool   `json:"paid"`
				Status     string `json:"status"`
				Parent     struct {
					SubscriptionDetails struct {
						Metadata struct {
							AccountID            string `json:"spyglass_account_id"`
							RequestID            string `json:"spyglass_checkout_request_id"`
							CommissioningCode    string `json:"spyglass_commissioning_code"`
							CommissioningVersion string `json:"spyglass_commissioning_version"`
						} `json:"metadata"`
					} `json:"subscription_details"`
				} `json:"parent"`
				Lines struct {
					Data []struct {
						Pricing struct {
							PriceDetails struct {
								Price string `json:"price"`
							} `json:"price_details"`
						} `json:"pricing"`
						Price struct {
							ID string `json:"id"`
						} `json:"price"`
					} `json:"data"`
				} `json:"lines"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return SubscriptionCommissioningEvidence{}, false, ErrInvalidPurchaseEvidence
	}
	invoice, metadata := event.Data.Object, event.Data.Object.Parent.SubscriptionDetails.Metadata
	if metadata.CommissioningCode == "" {
		return SubscriptionCommissioningEvidence{}, false, nil
	}
	version, versionErr := strconv.ParseUint(metadata.CommissioningVersion, 10, 64)
	if !strings.HasPrefix(invoice.ID, "in_") || invoice.AmountPaid <= 0 || !invoice.Paid || invoice.Status != "paid" || ids.Validate(metadata.AccountID) != nil || ids.Validate(metadata.RequestID) != nil || versionErr != nil || version == 0 {
		return SubscriptionCommissioningEvidence{}, false, ErrInvalidPurchaseEvidence
	}
	prices := make([]string, 0, len(invoice.Lines.Data))
	for _, line := range invoice.Lines.Data {
		price := line.Pricing.PriceDetails.Price
		if price == "" {
			price = line.Price.ID
		}
		if strings.HasPrefix(price, "price_") {
			prices = append(prices, price)
		}
	}
	return SubscriptionCommissioningEvidence{AccountID: ids.AccountID(metadata.AccountID), RequestID: metadata.RequestID, InvoiceID: invoice.ID, ItemCode: metadata.CommissioningCode, ItemVersion: version, ProviderPriceIDs: prices}, true, nil
}

func parsePurchaseRefund(payload []byte) (PurchaseRefundEvidence, error) {
	var event struct {
		Data struct {
			Object struct {
				ID             string `json:"id"`
				PaymentIntent  string `json:"payment_intent"`
				Amount         int64  `json:"amount"`
				AmountRefunded int64  `json:"amount_refunded"`
				Refunded       bool   `json:"refunded"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil || !strings.HasPrefix(event.Data.Object.ID, "ch_") || !strings.HasPrefix(event.Data.Object.PaymentIntent, "pi_") || event.Data.Object.Amount <= 0 || event.Data.Object.AmountRefunded <= 0 || event.Data.Object.AmountRefunded > event.Data.Object.Amount {
		return PurchaseRefundEvidence{}, ErrInvalidPurchaseEvidence
	}
	return PurchaseRefundEvidence{PaymentIntentID: event.Data.Object.PaymentIntent, Reference: "refund:" + event.Data.Object.ID, Amount: event.Data.Object.Amount, AmountRefunded: event.Data.Object.AmountRefunded, Refunded: event.Data.Object.Refunded}, nil
}

func parsePurchaseEvidence(payload []byte) (PurchaseEvidence, bool, error) {
	var event struct {
		Data struct {
			Object struct {
				ID                string `json:"id"`
				Mode              string `json:"mode"`
				PaymentStatus     string `json:"payment_status"`
				PaymentIntent     string `json:"payment_intent"`
				ClientReferenceID string `json:"client_reference_id"`
				Currency          string `json:"currency"`
				AmountSubtotal    int64  `json:"amount_subtotal"`
				Metadata          struct {
					AccountID      string `json:"spyglass_account_id"`
					PurchaseKind   string `json:"spyglass_purchase_kind"`
					ItemCode       string `json:"spyglass_item_code"`
					ItemVersion    string `json:"spyglass_item_version"`
					CatalogVersion string `json:"spyglass_catalog_version"`
					RequestID      string `json:"spyglass_checkout_request_id"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return PurchaseEvidence{}, false, ErrInvalidPurchaseEvidence
	}
	value := event.Data.Object
	if value.Mode != "payment" {
		return PurchaseEvidence{}, false, nil
	}
	if !strings.HasPrefix(value.ID, "cs_") {
		return PurchaseEvidence{}, false, ErrInvalidPurchaseEvidence
	}
	if value.PaymentStatus != "paid" {
		return PurchaseEvidence{}, false, nil
	}
	account := value.Metadata.AccountID
	if account == "" {
		account = value.ClientReferenceID
	}
	itemVersion, itemErr := strconv.ParseUint(value.Metadata.ItemVersion, 10, 64)
	catalogVersion, catalogErr := strconv.ParseUint(value.Metadata.CatalogVersion, 10, 64)
	kind := billing.PurchaseKind(value.Metadata.PurchaseKind)
	if !strings.HasPrefix(value.PaymentIntent, "pi_") || ids.Validate(account) != nil || ids.Validate(value.Metadata.RequestID) != nil || (kind != billing.PurchaseAITokenTopUp && kind != billing.PurchaseCommissioning) || value.Metadata.ItemCode == "" || itemErr != nil || itemVersion == 0 || catalogErr != nil || catalogVersion == 0 || strings.ToLower(value.Currency) != "usd" || value.AmountSubtotal <= 0 {
		return PurchaseEvidence{}, false, ErrInvalidPurchaseEvidence
	}
	return PurchaseEvidence{AccountID: ids.AccountID(account), RequestID: value.Metadata.RequestID, ProviderSessionID: value.ID, ProviderPaymentIntentID: value.PaymentIntent, Kind: kind, ItemCode: value.Metadata.ItemCode, ItemVersion: itemVersion, CatalogVersion: catalogVersion, Currency: "USD", AmountSubtotal: value.AmountSubtotal}, true, nil
}

var _ billing.EventHandler = (*BillingEventProjector)(nil)
