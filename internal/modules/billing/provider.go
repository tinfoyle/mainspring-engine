package billing

import (
	"context"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// Provider is the narrow billing boundary used by application services. Stripe
// SDK objects must not cross this interface into domain or transport code.
type Provider interface {
	CreateCustomer(context.Context, CreateCustomerCommand) (CustomerReference, error)
	CreateCheckoutSession(context.Context, CreateCheckoutCommand) (HostedSession, error)
	CreatePortalSession(context.Context, CreatePortalCommand) (HostedSession, error)
	RetrieveSubscription(context.Context, string) (ProviderSubscription, error)
}

type CreateCustomerCommand struct {
	AccountID      ids.AccountID
	Email          string
	Name           string
	IdempotencyKey string
}

type CustomerReference struct{ ID string }

type CreateCheckoutCommand struct {
	AccountID      ids.AccountID
	CustomerID     string
	StripePriceID  string
	OfferCode      string
	OfferVersion   uint64
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

type CreatePortalCommand struct {
	AccountID  ids.AccountID
	CustomerID string
	ReturnURL  string
}

type HostedSession struct {
	ID        string
	URL       string
	ExpiresAt time.Time
}

type ProviderSubscription struct {
	ID                 string
	CustomerID         string
	State              string
	PriceIDs           []string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	CancelAt           *time.Time
	ObjectVersion      string
}
