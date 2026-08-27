package commercialaccess

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrOfferUnavailable    = errors.New("offer is unavailable")
	ErrBillingUnavailable  = errors.New("billing is unavailable")
	ErrCustomerRequired    = errors.New("billing customer is required")
	ErrInvalidRequestID    = errors.New("request ID must be a UUID")
	ErrSubscriptionExists  = errors.New("an existing subscription must be managed through the billing portal")
	ErrCheckoutInProgress  = errors.New("a checkout session is already in progress")
	ErrPurchaseUnavailable = errors.New("one-time purchase is unavailable")
	ErrCommissioningOwned  = errors.New("commissioning has already been purchased for this Account")
	ErrReferralRateLimited = errors.New("Affiliate code validation is rate limited")
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
	BeginCheckout(context.Context, ids.AccountID, string, *PurchaseSnapshot, string, string, time.Time) (CheckoutReservation, error)
	CompleteCheckout(context.Context, ids.AccountID, string, billing.HostedSession, time.Time) error
	BeginOneTimeCheckout(context.Context, PurchaseSnapshot, string, string, time.Time) (CheckoutReservation, error)
	CompleteOneTimeCheckout(context.Context, ids.AccountID, string, billing.HostedSession, time.Time) error
	CommissioningPurchased(context.Context, ids.AccountID) (bool, error)
}

type Service struct {
	provider           billing.Provider
	repository         Repository
	authorizer         *access.Authorizer
	catalog            func() catalog.PublishedCatalog
	clock              Clock
	appOrigin          string
	mode               string
	referrals          ReferralAttributor
	referralValidation *abuse.Guard
}

type ReferralAttributor interface {
	ReserveCheckout(context.Context, string, ids.AccountID, string, string, uint64) (ids.ReferralAttributionID, error)
	CheckoutAttribution(context.Context, string) (ids.ReferralAttributionID, bool, error)
}

type Option func(*Service)

func WithReferralAttributor(referrals ReferralAttributor, validation *abuse.Guard) Option {
	return func(service *Service) { service.referrals, service.referralValidation = referrals, validation }
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
	if (service.referrals == nil) != (service.referralValidation == nil) {
		return nil, errors.New("referral attribution and validation budget must be configured together")
	}
	return service, nil
}

type CheckoutCommand struct {
	ActorUserID          ids.UserID
	Session              sessions.Session
	AccountID            ids.AccountID
	OfferCode            string
	AffiliateCode        string
	IncludeCommissioning bool
	RequestID            string
	NetworkActor         [32]byte
}

type CheckoutReservation struct {
	Proceed bool
	Resume  *billing.HostedSession
}

type PurchaseSnapshot struct {
	AccountID      ids.AccountID
	Kind           billing.PurchaseKind
	ItemCode       string
	ItemVersion    uint64
	CatalogVersion uint64
	Currency       string
	AmountMinor    int64
	Quantity       int64
}

type PurchaseCheckoutCommand struct {
	ActorUserID ids.UserID
	Session     sessions.Session
	AccountID   ids.AccountID
	Kind        billing.PurchaseKind
	ItemCode    string
	RequestID   string
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
	HasCustomer      bool                   `json:"has_customer"`
	CanManage        bool                   `json:"can_manage"`
	CanStartCheckout bool                   `json:"can_start_checkout"`
	Subscriptions    []Subscription         `json:"subscriptions"`
	Lifecycle        *SubscriptionLifecycle `json:"lifecycle,omitempty"`
}

type SubscriptionLifecycle struct {
	State         string    `json:"state"`
	Trigger       string    `json:"trigger"`
	EffectiveAt   time.Time `json:"effective_at"`
	RestrictionAt time.Time `json:"restriction_at"`
	DeleteAt      time.Time `json:"delete_at"`
}

func (s *Service) Status(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (Status, error) {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{AllowRestricted: true})
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
	if err := s.authorizeBilling(ctx, command.ActorUserID, command.AccountID); err != nil {
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
	commissioningCode, commissioningPriceID := "", ""
	var commissioningVersion uint64
	var commissioningSnapshot *PurchaseSnapshot
	if command.IncludeCommissioning {
		item := published.CommissioningOffer
		if item == nil || item.EffectiveFrom.After(s.clock.Now()) {
			return billing.HostedSession{}, ErrPurchaseUnavailable
		}
		owned, ownedErr := s.repository.CommissioningPurchased(ctx, command.AccountID)
		if ownedErr != nil {
			return billing.HostedSession{}, ownedErr
		}
		if owned {
			return billing.HostedSession{}, ErrCommissioningOwned
		}
		commissioningPriceID, err = s.repository.ProviderPrice(ctx, published.Version, item.Code, "stripe", s.mode)
		if err != nil || !strings.HasPrefix(commissioningPriceID, "price_") {
			return billing.HostedSession{}, ErrBillingUnavailable
		}
		commissioningCode, commissioningVersion = item.Code, item.Version
		commissioningSnapshot = &PurchaseSnapshot{AccountID: command.AccountID, Kind: billing.PurchaseCommissioning, ItemCode: item.Code, ItemVersion: item.Version, CatalogVersion: published.Version, Currency: item.Currency, AmountMinor: item.AmountMinor}
	}
	reservation, err := s.repository.BeginCheckout(ctx, command.AccountID, offer.Code, commissioningSnapshot, s.mode, command.RequestID, s.clock.Now())
	if err != nil {
		return billing.HostedSession{}, err
	}
	if reservation.Resume != nil {
		return *reservation.Resume, nil
	}
	if !reservation.Proceed {
		return billing.HostedSession{}, ErrCheckoutInProgress
	}
	if s.referrals != nil && strings.TrimSpace(command.AffiliateCode) != "" {
		allowed, limitErr := s.referralValidation.Allow(ctx, abuse.ScopeAffiliateCode,
			referralBudgetActor(command.NetworkActor, command.AccountID), s.clock.Now(), abuse.AffiliateCodePolicy)
		if limitErr != nil {
			return billing.HostedSession{}, fmt.Errorf("%w: Affiliate validation budget", ErrBillingUnavailable)
		}
		if !allowed {
			return billing.HostedSession{}, ErrReferralRateLimited
		}
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
	returnPath := "/app/checkout?offer=" + url.QueryEscape(offer.Code)
	session, err := s.provider.CreateCheckoutSession(ctx, billing.CreateCheckoutCommand{AccountID: command.AccountID, CustomerID: customerID, StripePriceID: priceID, OfferCode: offer.Code, OfferVersion: published.Version, AffiliateAttributionID: attributionID, RequestID: command.RequestID, CommissioningPriceID: commissioningPriceID, CommissioningCode: commissioningCode, CommissioningVersion: commissioningVersion, SuccessURL: s.appOrigin + returnPath + "&status=billing", CancelURL: s.appOrigin + returnPath + "&status=billing_cancelled", IdempotencyKey: "spyglass/checkout/" + string(command.AccountID) + "/" + command.RequestID})
	if err != nil {
		return billing.HostedSession{}, err
	}
	if err := s.repository.CompleteCheckout(ctx, command.AccountID, command.RequestID, session, s.clock.Now()); err != nil {
		return billing.HostedSession{}, err
	}
	return session, nil
}

func (s *Service) PurchaseCheckout(ctx context.Context, command PurchaseCheckoutCommand) (billing.HostedSession, error) {
	if err := s.authorize(ctx, command.ActorUserID, command.AccountID); err != nil {
		return billing.HostedSession{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return billing.HostedSession{}, err
	}
	if ids.Validate(command.RequestID) != nil {
		return billing.HostedSession{}, ErrInvalidRequestID
	}
	publication, now := s.catalog(), s.clock.Now().UTC()
	snapshot := PurchaseSnapshot{AccountID: command.AccountID, Kind: command.Kind, ItemCode: command.ItemCode, CatalogVersion: publication.Version}
	switch command.Kind {
	case billing.PurchaseAITokenTopUp:
		bundle, exists := findTokenBundle(publication, command.ItemCode, now)
		if !exists {
			return billing.HostedSession{}, ErrPurchaseUnavailable
		}
		snapshot.ItemVersion, snapshot.Currency, snapshot.AmountMinor, snapshot.Quantity = bundle.Version, bundle.Currency, bundle.AmountMinor, bundle.Quantity
	case billing.PurchaseCommissioning:
		item := publication.CommissioningOffer
		if item == nil || item.Code != command.ItemCode || item.EffectiveFrom.After(now) {
			return billing.HostedSession{}, ErrPurchaseUnavailable
		}
		owned, err := s.repository.CommissioningPurchased(ctx, command.AccountID)
		if err != nil {
			return billing.HostedSession{}, err
		}
		if owned {
			return billing.HostedSession{}, ErrCommissioningOwned
		}
		snapshot.ItemVersion, snapshot.Currency, snapshot.AmountMinor = item.Version, item.Currency, item.AmountMinor
	default:
		return billing.HostedSession{}, ErrPurchaseUnavailable
	}
	priceID, err := s.repository.ProviderPrice(ctx, publication.Version, snapshot.ItemCode, "stripe", s.mode)
	if err != nil || !strings.HasPrefix(priceID, "price_") {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	reservation, err := s.repository.BeginOneTimeCheckout(ctx, snapshot, s.mode, command.RequestID, now)
	if err != nil {
		return billing.HostedSession{}, err
	}
	if reservation.Resume != nil {
		return *reservation.Resume, nil
	}
	if !reservation.Proceed {
		return billing.HostedSession{}, ErrCheckoutInProgress
	}
	profile, err := s.repository.AccountProfile(ctx, command.AccountID)
	if err != nil || profile.AccountID != command.AccountID {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	customerID := profile.CustomerID
	if customerID == "" {
		created, createErr := s.provider.CreateCustomer(ctx, billing.CreateCustomerCommand{AccountID: command.AccountID, Email: profile.BillingEmail, Name: profile.AccountName, IdempotencyKey: "spyglass/customer/" + string(command.AccountID)})
		if createErr != nil {
			return billing.HostedSession{}, fmt.Errorf("%w: create customer", ErrBillingUnavailable)
		}
		customerID, err = s.repository.AttachCustomer(ctx, command.AccountID, created.ID, profile.BillingEmail, now)
		if err != nil {
			return billing.HostedSession{}, err
		}
	}
	if !strings.HasPrefix(customerID, "cus_") {
		return billing.HostedSession{}, ErrBillingUnavailable
	}
	returnPath := "/app/billing?purchase=" + url.QueryEscape(string(command.Kind))
	session, err := s.provider.CreateOneTimeCheckoutSession(ctx, billing.CreateOneTimeCheckoutCommand{AccountID: command.AccountID, CustomerID: customerID, StripePriceID: priceID, Kind: command.Kind, ItemCode: snapshot.ItemCode, ItemVersion: snapshot.ItemVersion, CatalogVersion: snapshot.CatalogVersion, RequestID: command.RequestID, SuccessURL: s.appOrigin + returnPath + "&status=purchase_returned", CancelURL: s.appOrigin + returnPath + "&status=purchase_cancelled", IdempotencyKey: "spyglass/purchase/" + string(command.AccountID) + "/" + command.RequestID})
	if err != nil {
		return billing.HostedSession{}, err
	}
	if err := s.repository.CompleteOneTimeCheckout(ctx, command.AccountID, command.RequestID, session, now); err != nil {
		return billing.HostedSession{}, err
	}
	return session, nil
}

func findTokenBundle(publication catalog.PublishedCatalog, code string, now time.Time) (catalog.AITokenBundle, bool) {
	for _, bundle := range publication.AITokenBundles {
		if bundle.Code == code && !bundle.EffectiveFrom.After(now) {
			return bundle, true
		}
	}
	return catalog.AITokenBundle{}, false
}

func referralBudgetActor(networkActor [32]byte, accountID ids.AccountID) [32]byte {
	if networkActor == ([32]byte{}) || ids.Validate(string(accountID)) != nil {
		return [32]byte{}
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("spyglass-affiliate-code-validation-v1\x00"))
	_, _ = digest.Write(networkActor[:])
	_, _ = digest.Write([]byte(accountID))
	var actor [32]byte
	copy(actor[:], digest.Sum(nil))
	return actor
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
	if err := s.authorizeBilling(ctx, command.ActorUserID, command.AccountID); err != nil {
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
	return s.provider.CreatePortalSession(ctx, billing.CreatePortalCommand{AccountID: command.AccountID, CustomerID: profile.CustomerID, ReturnURL: s.appOrigin + "/app/billing?status=portal_returned", IdempotencyKey: "spyglass/portal/" + string(command.AccountID) + "/" + command.RequestID})
}

func (s *Service) authorize(ctx context.Context, userID ids.UserID, accountID ids.AccountID) error {
	_, err := s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleBillingAdmin}})
	return err
}

func (s *Service) authorizeBilling(ctx context.Context, userID ids.UserID, accountID ids.AccountID) error {
	_, err := s.authorizer.Authorize(ctx, access.Actor{UserID: userID}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleBillingAdmin}, AllowRestricted: true})
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
