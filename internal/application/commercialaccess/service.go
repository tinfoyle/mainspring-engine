package commercialaccess

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrOfferUnavailable   = errors.New("offer is unavailable")
	ErrBillingUnavailable = errors.New("billing is unavailable")
	ErrCustomerRequired   = errors.New("billing customer is required")
	ErrInvalidRequestID   = errors.New("request ID must be a UUID")
)

type Clock interface{ Now() time.Time }

type AccountProfile struct {
	AccountID    ids.AccountID
	AccountName  string
	BillingEmail string
	CustomerID   string
}

// Repository owns private provider mappings and billing profiles. The public
// catalog never carries a Stripe Price identifier.
type Repository interface {
	AccountProfile(context.Context, ids.AccountID) (AccountProfile, error)
	AttachCustomer(context.Context, ids.AccountID, string, string, time.Time) (string, error)
	ProviderPrice(context.Context, uint64, string, string, string) (string, error)
}

type Service struct {
	provider   billing.Provider
	repository Repository
	authorizer *access.Authorizer
	catalog    func() catalog.PublishedCatalog
	clock      Clock
	appOrigin  string
	mode       string
}

func New(provider billing.Provider, repository Repository, authorizer *access.Authorizer, catalogSource func() catalog.PublishedCatalog, clock Clock, appOrigin, mode string) (*Service, error) {
	if provider == nil || repository == nil || authorizer == nil || catalogSource == nil || clock == nil {
		return nil, errors.New("commercial access dependencies are required")
	}
	origin, err := url.Parse(appOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("application origin must be an exact HTTPS origin")
	}
	if mode != "test" && mode != "live" {
		return nil, errors.New("billing mode must be test or live")
	}
	return &Service{provider: provider, repository: repository, authorizer: authorizer, catalog: catalogSource, clock: clock, appOrigin: strings.TrimSuffix(appOrigin, "/"), mode: mode}, nil
}

type CheckoutCommand struct {
	ActorUserID ids.UserID
	AccountID   ids.AccountID
	OfferCode   string
	RequestID   string
}

func (s *Service) Checkout(ctx context.Context, command CheckoutCommand) (billing.HostedSession, error) {
	if err := s.authorize(ctx, command.ActorUserID, command.AccountID); err != nil {
		return billing.HostedSession{}, err
	}
	if err := ids.Validate(command.RequestID); err != nil {
		return billing.HostedSession{}, ErrInvalidRequestID
	}
	published := s.catalog()
	offer, ok := findOffer(published, command.OfferCode, s.clock.Now())
	if !ok {
		return billing.HostedSession{}, ErrOfferUnavailable
	}
	priceID, err := s.repository.ProviderPrice(ctx, published.Version, offer.Code, "stripe", s.mode)
	if err != nil || !strings.HasPrefix(priceID, "price_") {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	profile, err := s.repository.AccountProfile(ctx, command.AccountID)
	if err != nil {
		return billing.HostedSession{}, err
	}
	if profile.AccountID != command.AccountID {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	customerID := profile.CustomerID
	if customerID == "" {
		created, createErr := s.provider.CreateCustomer(ctx, billing.CreateCustomerCommand{AccountID: command.AccountID, Email: profile.BillingEmail, Name: profile.AccountName, IdempotencyKey: "spyglass/customer/" + string(command.AccountID)})
		if createErr != nil {
			return billing.HostedSession{}, fmt.Errorf("%w: create customer", ErrBillingUnavailable)
		}
		customerID, err = s.repository.AttachCustomer(ctx, command.AccountID, created.ID, profile.BillingEmail, s.clock.Now())
		if err != nil {
			return billing.HostedSession{}, err
		}
	}
	if !strings.HasPrefix(customerID, "cus_") {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	return s.provider.CreateCheckoutSession(ctx, billing.CreateCheckoutCommand{AccountID: command.AccountID, CustomerID: customerID, StripePriceID: priceID, OfferCode: offer.Code, OfferVersion: published.Version, SuccessURL: s.appOrigin + "/app/account?billing=processing", CancelURL: s.appOrigin + "/app/account?billing=cancelled", IdempotencyKey: "spyglass/checkout/" + string(command.AccountID) + "/" + command.RequestID})
}

type PortalCommand struct {
	ActorUserID ids.UserID
	AccountID   ids.AccountID
	RequestID   string
}

func (s *Service) Portal(ctx context.Context, command PortalCommand) (billing.HostedSession, error) {
	if err := s.authorize(ctx, command.ActorUserID, command.AccountID); err != nil {
		return billing.HostedSession{}, err
	}
	if err := ids.Validate(command.RequestID); err != nil {
		return billing.HostedSession{}, ErrInvalidRequestID
	}
	profile, err := s.repository.AccountProfile(ctx, command.AccountID)
	if err != nil {
		return billing.HostedSession{}, err
	}
	if profile.AccountID != command.AccountID {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	if !strings.HasPrefix(profile.CustomerID, "cus_") {
		return billing.HostedSession{}, ErrCustomerRequired
	}
	return s.provider.CreatePortalSession(ctx, billing.CreatePortalCommand{AccountID: command.AccountID, CustomerID: profile.CustomerID, ReturnURL: s.appOrigin + "/app/account", IdempotencyKey: "spyglass/portal/" + string(command.AccountID) + "/" + command.RequestID})
}

func (s *Service) authorize(ctx context.Context, userID ids.UserID, accountID ids.AccountID) error {
	_, err := s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleBillingAdmin}})
	return err
}

func findOffer(published catalog.PublishedCatalog, code string, now time.Time) (catalog.Offer, bool) {
	for _, offer := range published.Offers {
		if offer.Code == code && offer.Published && offer.AmountMinor > 0 && offer.BillingInterval != "none" && !offer.EffectiveFrom.After(now) {
			return offer, true
		}
	}
	return catalog.Offer{}, false
}
