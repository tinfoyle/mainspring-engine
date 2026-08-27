package affiliatesettlement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidInvoice      = errors.New("Affiliate settlement invoice is invalid")
	ErrSettlementConflict  = errors.New("Affiliate settlement evidence conflicts with the existing reservation")
	ErrCustomerUnavailable = errors.New("Affiliate settlement Account has no billing customer")
)

type Clock interface{ Now() time.Time }

type Reservation struct {
	ID                    string
	AffiliateID           ids.AffiliateID
	SettlementAccountID   ids.AccountID
	ProviderInvoiceID     string
	PolicyVersion         uint64
	State                 string
	AmountMinor           int64
	Currency              string
	ProviderCustomerID    string
	ProviderTransactionID string
}

type ReversalAdjustment struct {
	ID                    string
	ReversalEntryID       ids.CommissionEntryID
	ReservationID         string
	AffiliateID           ids.AffiliateID
	SettlementAccountID   ids.AccountID
	Kind                  string
	State                 string
	AmountMinor           int64
	Currency              string
	ProviderCustomerID    string
	ProviderTransactionID string
}

type Repository interface {
	PrepareInvoiceCredits(context.Context, string, ids.AccountID, string, int64, string, time.Time) ([]Reservation, error)
	CompleteInvoiceCredit(context.Context, string, string, string, time.Time) (Reservation, error)
	SettleInvoiceCredits(context.Context, string, time.Time) (int64, error)
	PrepareReversalAdjustments(context.Context, string, string, time.Time) ([]ReversalAdjustment, error)
	CompleteReversalAdjustment(context.Context, string, string, string, time.Time) (ReversalAdjustment, error)
	ReversalProviderObjects(context.Context, string) ([]string, error)
}

type Service struct {
	repository Repository
	provider   billing.CustomerBalanceProvider
	ids        ids.Generator
	clock      Clock
}

func New(repository Repository, provider billing.CustomerBalanceProvider, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || provider == nil || generator == nil || clock == nil {
		return nil, errors.New("Affiliate settlement dependencies are required")
	}
	return &Service{repository: repository, provider: provider, ids: generator, clock: clock}, nil
}

type InvoiceCreditCommand struct {
	AccountID          ids.AccountID
	ProviderInvoiceID  string
	ProviderCustomerID string
	AmountDueMinor     int64
	Currency           string
}

func (s *Service) CreditInvoice(ctx context.Context, command InvoiceCreditCommand) ([]Reservation, error) {
	currency := strings.ToUpper(command.Currency)
	if ids.Validate(string(command.AccountID)) != nil || !strings.HasPrefix(command.ProviderInvoiceID, "in_") || !strings.HasPrefix(command.ProviderCustomerID, "cus_") || command.AmountDueMinor <= 0 || len(currency) != 3 {
		return nil, ErrInvalidInvoice
	}
	reservations, err := s.repository.PrepareInvoiceCredits(ctx, s.ids.New(), command.AccountID, command.ProviderInvoiceID, command.AmountDueMinor, currency, s.clock.Now())
	if err != nil {
		return nil, err
	}
	for index := range reservations {
		reservation := reservations[index]
		if reservation.ProviderCustomerID != command.ProviderCustomerID {
			return nil, ErrSettlementConflict
		}
		if reservation.State == "credited" || reservation.State == "settled" || reservation.State == "released" {
			continue
		}
		transaction, err := s.provider.CreateCustomerBalanceCredit(ctx, billing.CreateCustomerBalanceCreditCommand{
			AccountID: reservation.SettlementAccountID, CustomerID: reservation.ProviderCustomerID,
			AmountMinor: reservation.AmountMinor, Currency: reservation.Currency, Reference: reservation.ID,
			IdempotencyKey: "spyglass-affiliate-credit:" + reservation.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("apply Affiliate customer balance credit: %w", err)
		}
		completed, err := s.repository.CompleteInvoiceCredit(ctx, reservation.ID, transaction.CustomerID, transaction.ID, s.clock.Now())
		if err != nil {
			return nil, err
		}
		reservations[index] = completed
	}
	return reservations, nil
}

func (s *Service) SettleInvoice(ctx context.Context, providerInvoiceID string) (int64, error) {
	if !strings.HasPrefix(providerInvoiceID, "in_") {
		return 0, ErrInvalidInvoice
	}
	return s.repository.SettleInvoiceCredits(ctx, providerInvoiceID, s.clock.Now())
}

// ReconcileAdverse compensates customer-balance credit that was already sent
// to Stripe. A completed Support check is instead represented as an internal
// recovery offset against future available earnings; no check operation is
// attempted here.
func (s *Service) ReconcileAdverse(ctx context.Context, providerObjectID string) ([]ReversalAdjustment, error) {
	if (!strings.HasPrefix(providerObjectID, "re_") && !strings.HasPrefix(providerObjectID, "dp_") && !strings.HasPrefix(providerObjectID, "cn_")) || len(providerObjectID) > 200 || strings.ContainsAny(providerObjectID, "\r\n\t ") {
		return nil, ErrInvalidInvoice
	}
	adjustments, err := s.repository.PrepareReversalAdjustments(ctx, s.ids.New(), providerObjectID, s.clock.Now())
	if err != nil {
		return nil, err
	}
	for index := range adjustments {
		adjustment := adjustments[index]
		if adjustment.Kind != "customer_balance_debit" || adjustment.State == "applied" {
			continue
		}
		transaction, err := s.provider.CreateCustomerBalanceDebit(ctx, billing.CreateCustomerBalanceDebitCommand{
			AccountID: adjustment.SettlementAccountID, CustomerID: adjustment.ProviderCustomerID,
			AmountMinor: adjustment.AmountMinor, Currency: adjustment.Currency, Reference: adjustment.ID,
			IdempotencyKey: "spyglass-affiliate-reversal:" + adjustment.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("reverse Affiliate customer balance credit: %w", err)
		}
		completed, err := s.repository.CompleteReversalAdjustment(ctx, adjustment.ID, transaction.CustomerID, transaction.ID, s.clock.Now())
		if err != nil {
			return nil, err
		}
		adjustments[index] = completed
	}
	return adjustments, nil
}

func (s *Service) ReconcileInvoiceAdversity(ctx context.Context, providerInvoiceID string) error {
	if !strings.HasPrefix(providerInvoiceID, "in_") || len(providerInvoiceID) > 200 || strings.ContainsAny(providerInvoiceID, "\r\n\t ") {
		return ErrInvalidInvoice
	}
	objects, err := s.repository.ReversalProviderObjects(ctx, providerInvoiceID)
	if err != nil {
		return err
	}
	for _, providerObjectID := range objects {
		if _, err := s.ReconcileAdverse(ctx, providerObjectID); err != nil {
			return err
		}
	}
	return nil
}
