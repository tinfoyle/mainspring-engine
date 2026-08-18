package commercialaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testUserID    ids.UserID    = "11111111-1111-4111-8111-111111111111"
	testAccountID ids.AccountID = "22222222-2222-4222-8222-222222222222"
	testRequestID               = "33333333-3333-4333-8333-333333333333"
)

type serviceClock struct{ now time.Time }

func (c serviceClock) Now() time.Time { return c.now }

type stateSource struct{ role accounts.MembershipRole }

func (s stateSource) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return access.State{Account: accounts.Account{ID: testAccountID, DisplayName: "Northstar", State: accounts.AccountActive}, Membership: accounts.Membership{AccountID: testAccountID, UserID: testUserID, Role: s.role, State: accounts.MembershipActive}, Entitlements: entitlements.Snapshot{AccountID: testAccountID}}, nil
}

type serviceRepository struct {
	profile       AccountProfile
	price         string
	status        Status
	reservation   CheckoutReservation
	blockCheckout bool
}

func (r *serviceRepository) BeginCheckout(context.Context, ids.AccountID, string, string, string, time.Time) (CheckoutReservation, error) {
	if r.reservation.Resume != nil || r.blockCheckout {
		return r.reservation, nil
	}
	return CheckoutReservation{Proceed: true}, nil
}
func (r *serviceRepository) CompleteCheckout(context.Context, ids.AccountID, string, billing.HostedSession, time.Time) error {
	return nil
}

func (r *serviceRepository) BillingStatus(context.Context, ids.AccountID, string, string) (Status, error) {
	value := r.status
	if r.profile.CustomerID != "" {
		value.HasCustomer = true
	}
	return value, nil
}

func (r *serviceRepository) AccountProfile(context.Context, ids.AccountID) (AccountProfile, error) {
	return r.profile, nil
}
func (r *serviceRepository) AttachCustomer(_ context.Context, _ ids.AccountID, customerID, _ string, _ time.Time) (string, error) {
	r.profile.CustomerID = customerID
	return customerID, nil
}
func (r *serviceRepository) ProviderPrice(context.Context, uint64, string, string, string) (string, error) {
	if r.price == "" {
		return "", ErrBillingUnavailable
	}
	return r.price, nil
}

type serviceProvider struct {
	customerCalls, checkoutCalls, portalCalls int
	checkout                                  billing.CreateCheckoutCommand
}

func (p *serviceProvider) CreateCustomer(context.Context, billing.CreateCustomerCommand) (billing.CustomerReference, error) {
	p.customerCalls++
	return billing.CustomerReference{ID: "cus_test"}, nil
}
func (p *serviceProvider) CreateCheckoutSession(_ context.Context, command billing.CreateCheckoutCommand) (billing.HostedSession, error) {
	p.checkoutCalls++
	p.checkout = command
	return billing.HostedSession{ID: "cs_test", URL: "https://checkout.stripe.com/test"}, nil
}
func (p *serviceProvider) CreatePortalSession(context.Context, billing.CreatePortalCommand) (billing.HostedSession, error) {
	p.portalCalls++
	return billing.HostedSession{ID: "bps_test", URL: "https://billing.stripe.com/test"}, nil
}
func (p *serviceProvider) RetrieveSubscription(context.Context, string) (billing.ProviderSubscription, error) {
	return billing.ProviderSubscription{}, errors.New("unused")
}

func TestCheckoutResolvesLocalOfferAndCreatesCustomer(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	publication := paidCatalog(now)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, AccountName: "Northstar", BillingEmail: "owner@example.com"}, price: "price_private"}
	provider := &serviceProvider{}
	authorizer, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, err := New(provider, repository, authorizer, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "team-monthly-v1", RequestID: testRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "cs_test" || provider.customerCalls != 1 || provider.checkoutCalls != 1 {
		t.Fatalf("unexpected calls/session: %+v provider=%+v", session, provider)
	}
	if provider.checkout.StripePriceID != "price_private" || provider.checkout.OfferCode != "team-monthly-v1" || provider.checkout.SuccessURL != "https://app.infiniteocean.net/app?status=billing#billing" {
		t.Fatalf("unsafe checkout projection: %+v", provider.checkout)
	}
}

func TestCheckoutRejectsRoleUnknownOfferAndBadIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	publication := paidCatalog(now)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	viewer, _ := access.NewAuthorizer(stateSource{role: accounts.RoleViewer})
	service, _ := New(provider, repository, viewer, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	if _, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "team-monthly-v1", RequestID: testRequestID}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("expected role denial, got %v", err)
	}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ = New(provider, repository, owner, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	if _, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "forged", RequestID: testRequestID}); !errors.Is(err, ErrOfferUnavailable) {
		t.Fatalf("expected offer denial, got %v", err)
	}
	if _, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "team-monthly-v1", RequestID: "reused-string"}); !errors.Is(err, ErrInvalidRequestID) {
		t.Fatalf("expected request ID denial, got %v", err)
	}
	if provider.checkoutCalls != 0 {
		t.Fatal("Stripe must not be called for denied requests")
	}
}

func TestStatusIsLocalAndSeparatesVisibilityFromManagement(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}}
	provider := &serviceProvider{}
	viewer, _ := access.NewAuthorizer(stateSource{role: accounts.RoleViewer})
	service, _ := New(provider, repository, viewer, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	status, err := service.Status(context.Background(), testUserID, testAccountID)
	if err != nil || !status.HasCustomer || status.CanManage {
		t.Fatalf("unexpected viewer status: %+v err=%v", status, err)
	}
	if provider.customerCalls+provider.checkoutCalls+provider.portalCalls != 0 {
		t.Fatal("status must not call Stripe")
	}
}

func TestCheckoutRefusesSecondManagedSubscription(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private", status: Status{Subscriptions: []Subscription{{State: "active", OfferCode: "team-monthly-v1"}}}}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	_, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "team-monthly-v1", RequestID: testRequestID})
	if !errors.Is(err, ErrSubscriptionExists) {
		t.Fatalf("expected portal-only change, got %v", err)
	}
	if provider.checkoutCalls != 0 {
		t.Fatal("second subscription must not reach Stripe Checkout")
	}
}

func TestCheckoutResumesDurableHostedSession(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	hosted := billing.HostedSession{ID: "cs_existing", URL: "https://checkout.stripe.com/existing", ExpiresAt: now.Add(time.Hour)}
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private", reservation: CheckoutReservation{Resume: &hosted}}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	result, err := service.Checkout(context.Background(), CheckoutCommand{ActorUserID: testUserID, AccountID: testAccountID, OfferCode: "team-monthly-v1", RequestID: testRequestID})
	if err != nil || result.ID != "cs_existing" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if provider.customerCalls+provider.checkoutCalls != 0 {
		t.Fatal("durable checkout retry must not create new provider objects")
	}
}

func paidCatalog(now time.Time) catalog.PublishedCatalog {
	definition := catalog.FeaturePackage{Code: catalog.PackageWork, Version: 1, Name: "Work", Features: []string{"work.read"}}
	plan := catalog.Plan{Code: "team", Version: 1, Name: "Team", Packages: map[catalog.PackageCode]catalog.PackageMode{catalog.PackageWork: catalog.ModeEnabled}}
	offer := catalog.Offer{Code: "team-monthly-v1", PlanCode: "team", PlanVersion: 1, Currency: "USD", AmountMinor: 4900, BillingInterval: "month", Published: true, EffectiveFrom: now}
	return catalog.PublishedCatalog{Version: 2, PublishedAt: now, Packages: []catalog.FeaturePackage{definition}, Plans: []catalog.Plan{plan}, Offers: []catalog.Offer{offer}}
}
