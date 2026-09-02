package affiliatesettlement_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesettlement"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	settlementAccountID = "10000000-0000-4000-8000-000000000001"
	reservationID       = "10000000-0000-4000-8000-000000000002"
)

type repository struct {
	reservations []affiliatesettlement.Reservation
	adjustments  []affiliatesettlement.ReversalAdjustment
	completed    []string
	adjusted     []string
	settled      string
	objects      []string
}

func (r *repository) PrepareInvoiceCredits(_ context.Context, seed string, accountID ids.AccountID, invoiceID string, amount int64, currency string, _ time.Time) ([]affiliatesettlement.Reservation, error) {
	if len(r.reservations) == 0 {
		r.reservations = []affiliatesettlement.Reservation{{ID: seed, AffiliateID: "10000000-0000-4000-8000-000000000003", SettlementAccountID: accountID,
			ProviderInvoiceID: invoiceID, State: "reserved", AmountMinor: amount, Currency: currency, ProviderCustomerID: "cus_affiliate"}}
	}
	return append([]affiliatesettlement.Reservation{}, r.reservations...), nil
}
func (r *repository) CompleteInvoiceCredit(_ context.Context, id, customerID, transactionID string, _ time.Time) (affiliatesettlement.Reservation, error) {
	r.completed = append(r.completed, id)
	for index := range r.reservations {
		if r.reservations[index].ID == id {
			r.reservations[index].State = "credited"
			r.reservations[index].ProviderCustomerID = customerID
			r.reservations[index].ProviderTransactionID = transactionID
			return r.reservations[index], nil
		}
	}
	return affiliatesettlement.Reservation{}, affiliatesettlement.ErrSettlementConflict
}
func (r *repository) SettleInvoiceCredits(_ context.Context, invoiceID string, _ time.Time) (int64, error) {
	r.settled = invoiceID
	return 1000, nil
}
func (r *repository) PrepareReversalAdjustments(_ context.Context, seed, _ string, _ time.Time) ([]affiliatesettlement.ReversalAdjustment, error) {
	if len(r.adjustments) == 0 {
		r.adjustments = []affiliatesettlement.ReversalAdjustment{{ID: seed,
			ReversalEntryID: "10000000-0000-4000-8000-000000000004", ReservationID: reservationID,
			AffiliateID: "10000000-0000-4000-8000-000000000003", SettlementAccountID: settlementAccountID,
			Kind: "customer_balance_debit", State: "pending", AmountMinor: 1000, Currency: "USD",
			ProviderCustomerID: "cus_affiliate"}}
	}
	return append([]affiliatesettlement.ReversalAdjustment{}, r.adjustments...), nil
}
func (r *repository) CompleteReversalAdjustment(_ context.Context, id, customerID, transactionID string, _ time.Time) (affiliatesettlement.ReversalAdjustment, error) {
	r.adjusted = append(r.adjusted, id)
	for index := range r.adjustments {
		if r.adjustments[index].ID == id {
			r.adjustments[index].State = "applied"
			r.adjustments[index].ProviderCustomerID = customerID
			r.adjustments[index].ProviderTransactionID = transactionID
			return r.adjustments[index], nil
		}
	}
	return affiliatesettlement.ReversalAdjustment{}, affiliatesettlement.ErrSettlementConflict
}
func (r *repository) ReversalProviderObjects(_ context.Context, _ string) ([]string, error) {
	return append([]string{}, r.objects...), nil
}

type provider struct {
	commands []billing.CreateCustomerBalanceCreditCommand
	debits   []billing.CreateCustomerBalanceDebitCommand
}

func (p *provider) CreateCustomerBalanceCredit(_ context.Context, command billing.CreateCustomerBalanceCreditCommand) (billing.CustomerBalanceTransaction, error) {
	p.commands = append(p.commands, command)
	return billing.CustomerBalanceTransaction{ID: "cbtxn_affiliate", CustomerID: command.CustomerID, AmountMinor: command.AmountMinor, Currency: command.Currency}, nil
}
func (p *provider) CreateCustomerBalanceDebit(_ context.Context, command billing.CreateCustomerBalanceDebitCommand) (billing.CustomerBalanceTransaction, error) {
	p.debits = append(p.debits, command)
	return billing.CustomerBalanceTransaction{ID: "cbtxn_reversal", CustomerID: command.CustomerID, AmountMinor: command.AmountMinor, Currency: command.Currency}, nil
}

type generator struct{}

func (generator) New() string { return reservationID }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestInvoiceCreditUsesOneDurableNegativeCustomerBalanceAdjustment(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository, provider := &repository{}, &provider{}
	service, err := affiliatesettlement.New(repository, provider, generator{}, clock{now})
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := service.CreditInvoice(context.Background(), affiliatesettlement.InvoiceCreditCommand{
		AccountID: settlementAccountID, ProviderInvoiceID: "in_affiliate", ProviderCustomerID: "cus_affiliate", AmountDueMinor: 1000, Currency: "usd",
	})
	if err != nil || len(reservations) != 1 || reservations[0].State != "credited" || len(provider.commands) != 1 || len(repository.completed) != 1 {
		t.Fatalf("reservations=%+v commands=%+v completed=%v err=%v", reservations, provider.commands, repository.completed, err)
	}
	command := provider.commands[0]
	if command.AmountMinor != 1000 || command.Currency != "USD" || command.Reference != reservationID || command.IdempotencyKey != "spyglass-affiliate-credit:"+reservationID {
		t.Fatalf("command=%+v", command)
	}
	reservations, err = service.CreditInvoice(context.Background(), affiliatesettlement.InvoiceCreditCommand{
		AccountID: settlementAccountID, ProviderInvoiceID: "in_affiliate", ProviderCustomerID: "cus_affiliate", AmountDueMinor: 1000, Currency: "USD",
	})
	if err != nil || len(provider.commands) != 1 || reservations[0].ProviderTransactionID != "cbtxn_affiliate" {
		t.Fatalf("retry reservations=%+v commands=%d err=%v", reservations, len(provider.commands), err)
	}
}

func TestBillingEventsPrepareAfterTaxCreditAndSettleOnPaidInvoice(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository, provider := &repository{}, &provider{}
	service, _ := affiliatesettlement.New(repository, provider, generator{}, clock{now})
	projector, _ := affiliatesettlement.NewBillingEventProjector(service)
	created := []byte(`{"data":{"object":{"id":"in_affiliate","customer":"cus_affiliate","total":5350,"amount_due":5350,"currency":"usd","status":"draft","billing_reason":"subscription_cycle"}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{AccountID: settlementAccountID, EventType: "invoice.created"}, Payload: created}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(provider.commands) != 1 || provider.commands[0].AmountMinor != 5350 {
		t.Fatalf("commands=%+v", provider.commands)
	}
	paid := []byte(`{"data":{"object":{"id":"in_affiliate","status":"paid"}}}`)
	if err := projector.Project(context.Background(), billing.WorkItem{Entry: billing.InboxEntry{EventType: "invoice.paid"}, Payload: paid}); err != nil {
		t.Fatal(err)
	}
	if repository.settled != "in_affiliate" {
		t.Fatalf("settled=%q", repository.settled)
	}
}

func TestBillingEventsIgnoreTerminalCreatedInvoiceSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository, provider := &repository{}, &provider{}
	service, _ := affiliatesettlement.New(repository, provider, generator{}, clock{now})
	projector, _ := affiliatesettlement.NewBillingEventProjector(service)
	created := []byte(`{"data":{"object":{"id":"in_affiliate","customer":"cus_affiliate","total":5350,"amount_due":5350,"currency":"usd","status":"paid","billing_reason":"subscription_create"}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{AccountID: settlementAccountID, EventType: "invoice.created"}, Payload: created}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(provider.commands) != 0 {
		t.Fatalf("terminal invoice created %d balance credits", len(provider.commands))
	}
}

func TestAdverseEventCreatesOneDurableCompensatingCustomerBalanceDebit(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository, provider := &repository{}, &provider{}
	service, _ := affiliatesettlement.New(repository, provider, generator{}, clock{now})
	projector, _ := affiliatesettlement.NewBillingEventProjector(service)
	payload := []byte(`{"data":{"object":{"id":"re_affiliate","amount":5000,"status":"succeeded"}}}`)
	item := billing.WorkItem{Entry: billing.InboxEntry{EventType: "refund.updated"}, Payload: payload}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(provider.debits) != 1 || len(repository.adjusted) != 1 || provider.debits[0].AmountMinor != 1000 ||
		provider.debits[0].IdempotencyKey != "spyglass-affiliate-reversal:"+reservationID {
		t.Fatalf("debits=%+v adjusted=%v", provider.debits, repository.adjusted)
	}
	if err := projector.Project(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if len(provider.debits) != 1 {
		t.Fatalf("adverse replay created %d debits", len(provider.debits))
	}
}

var _ affiliatesettlement.Repository = (*repository)(nil)
var _ billing.CustomerBalanceProvider = (*provider)(nil)
