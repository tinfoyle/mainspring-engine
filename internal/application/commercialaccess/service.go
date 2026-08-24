package commercialaccess

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrOfferUnavailable   = errors.New("offer is unavailable")
	ErrBillingUnavailable = errors.New("billing is unavailable")
	ErrCustomerRequired   = errors.New("billing customer is required")
	ErrInvalidRequestID   = errors.New("request ID must be a UUID")
	ErrSubscriptionExists = errors.New("an existing subscription must be managed through the billing portal")
	ErrCheckoutInProgress = errors.New("a checkout session is already in progress")
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
	BillingStatus(context.Context, ids.AccountID, string, string) (Status, error)
	BeginCheckout(context.Context, ids.AccountID, string, string, string, time.Time) (CheckoutReservation, error)
	CompleteCheckout(context.Context, ids.AccountID, string, billing.HostedSession, time.Time) error
}

type Service struct {
	provider   billing.Provider
	repository Repository
	authorizer *access.Authorizer
	catalog    func() catalog.PublishedCatalog
	clock      Clock
	appOrigin  string
	mode       string
	referrals  ReferralAttributor
}

type ReferralAttributor interface {
	ReserveCheckout(context.Context, string, ids.AccountID, string, string, uint64) (ids.ReferralAttributionID, error)
	CheckoutAttribution(context.Context, string) (ids.ReferralAttributionID, bool, error)
}

type Option func(*Service)

func WithReferralAttributor(referrals ReferralAttributor) Option {
	return func(service *Service) { service.referrals = referrals }
}

func New(provider billing.Provider, repository Repository, authorizer *access.Authorizer, catalogSource func() catalog.PublishedCatalog, clock Clock, appOrigin, mode string, options ...Option) (*Service, error) {
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
	service := &Service{provider: provider, repository: repository, authorizer: authorizer, catalog: catalogSource, clock: clock, appOrigin: strings.TrimSuffix(appOrigin, "/"), mode: mode}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

type CheckoutCommand struct {
	ActorUserID   ids.UserID
	Session       sessions.Session
	AccountID     ids.AccountID
	OfferCode     string
	AffiliateCode string
	RequestID     string
}

type CheckoutReservation struct {
	Proceed bool
	Resume  *billing.HostedSession
}

type Subscription struct {
	State              string     `json:"state"`
	OfferCode          string     `json:"offer_code"`
	CatalogVersion     uint64     `json:"catalog_version"`
	CurrentPeriodStart *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
	CancelAt           *time.Time `json:"cancel_at,omitempty"`
	LastSyncedAt       time.Time  `json:"last_synced_at"`
}

type Status struct {
	HasCustomer      bool           `json:"has_customer"`
	CanManage        bool           `json:"can_manage"`
	CanStartCheckout bool           `json:"can_start_checkout"`
	Subscriptions    []Subscription `json:"subscriptions"`
}

func (s *Service) Status(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (Status, error) {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{})
	if err != nil {
		return Status{}, err
	}
	status, err := s.repository.BillingStatus(ctx, accountID, "stripe", s.mode)
	if err != nil {
		return Status{}, err
	}
	status.CanManage = accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleBillingAdmin
	status.CanStartCheckout = status.CanManage && !hasManagedSubscription(status.Subscriptions)
	return status, nil
}

func (s *Service) Checkout(ctx context.Context, command CheckoutCommand) (billing.HostedSession, error) {
	if err := s.authorize(ctx, command.ActorUserID, command.AccountID); err != nil {
		return billing.HostedSession{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
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
	status, err := s.repository.BillingStatus(ctx, command.AccountID, "stripe", s.mode)
	if err != nil {
		return billing.HostedSession{}, err
	}
	if hasManagedSubscription(status.Subscriptions) {
		return billing.HostedSession{}, ErrSubscriptionExists
	}
	priceID, err := s.repository.ProviderPrice(ctx, published.Version, offer.Code, "stripe", s.mode)
	if err != nil || !strings.HasPrefix(priceID, "price_") {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	reservation, err := s.repository.BeginCheckout(ctx, command.AccountID, offer.Code, s.mode, command.RequestID, s.clock.Now())
	if err != nil {
		return billing.HostedSession{}, err
	}
	if reservation.Resume != nil {
		return *reservation.Resume, nil
	}
	if !reservation.Proceed {
		return billing.HostedSession{}, ErrCheckoutInProgress
	}
	var attributionID ids.ReferralAttributionID
	if s.referrals != nil {
		if strings.TrimSpace(command.AffiliateCode) != "" {
			attributionID, err = s.referrals.ReserveCheckout(ctx, command.AffiliateCode, command.AccountID,
				command.RequestID, offer.Code, published.Version)
		} else {
			var found bool
			attributionID, found, err = s.referrals.CheckoutAttribution(ctx, command.RequestID)
			if !found {
				attributionID = ""
			}
		}
		if err != nil {
			return billing.HostedSession{}, err
		}
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
	session, err := s.provider.CreateCheckoutSession(ctx, billing.CreateCheckoutCommand{AccountID: command.AccountID, CustomerID: customerID, StripePriceID: priceID, OfferCode: offer.Code, OfferVersion: published.Version, AffiliateAttributionID: attributionID, SuccessURL: s.appOrigin + "/app?status=billing#billing", CancelURL: s.appOrigin + "/app?status=billing_cancelled#billing", IdempotencyKey: "spyglass/checkout/" + string(command.AccountID) + "/" + command.RequestID})
	if err != nil {
		return billing.HostedSession{}, err
	}
	if err := s.repository.CompleteCheckout(ctx, command.AccountID, command.RequestID, session, s.clock.Now()); err != nil {
		return billing.HostedSession{}, err
	}
	return session, nil
}

func hasManagedSubscription(subscriptions []Subscription) bool {
	for _, subscription := range subscriptions {
		if subscription.State != "canceled" && subscription.State != "incomplete_expired" {
			return true
		}
	}
	return false
}

type PortalCommand struct {
	ActorUserID ids.UserID
	Session     sessions.Session
	AccountID   ids.AccountID
	RequestID   string
}

func (s *Service) Portal(ctx context.Context, command PortalCommand) (billing.HostedSession, error) {
	if err := s.authorize(ctx, command.ActorUserID, command.AccountID); err != nil {
		return billing.HostedSession{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
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
	return s.provider.CreatePortalSession(ctx, billing.CreatePortalCommand{AccountID: command.AccountID, CustomerID: profile.CustomerID, ReturnURL: s.appOrigin + "/app#billing", IdempotencyKey: "spyglass/portal/" + string(command.AccountID) + "/" + command.RequestID})
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
