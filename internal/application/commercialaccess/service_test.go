package commercialaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
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
	profile               AccountProfile
	price                 string
	status                Status
	reservation           CheckoutReservation
	blockCheckout         bool
	commissioningOwned    bool
	purchase              PurchaseSnapshot
	checkoutCommissioning *PurchaseSnapshot
}

func (r *serviceRepository) BeginCheckout(_ context.Context, _ ids.AccountID, _ string, commissioning *PurchaseSnapshot, _, _ string, _ time.Time) (CheckoutReservation, error) {
	r.checkoutCommissioning = commissioning
	if r.reservation.Resume != nil || r.blockCheckout {
		return r.reservation, nil
	}
	return CheckoutReservation{Proceed: true}, nil
}
func (r *serviceRepository) BeginOneTimeCheckout(_ context.Context, purchase PurchaseSnapshot, _, _ string, _ time.Time) (CheckoutReservation, error) {
	r.purchase = purchase
	return CheckoutReservation{Proceed: true}, nil
}
func (r *serviceRepository) CompleteOneTimeCheckout(context.Context, ids.AccountID, string, billing.HostedSession, time.Time) error {
	return nil
}
func (r *serviceRepository) CommissioningPurchased(context.Context, ids.AccountID) (bool, error) {
	return r.commissioningOwned, nil
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
	oneTime                                   billing.CreateOneTimeCheckoutCommand
	portal                                    billing.CreatePortalCommand
}

type referralAttributor struct {
	code, requestID, offerCode string
	accountID                  ids.AccountID
	offerVersion               uint64
	id                         ids.ReferralAttributionID
}

type referralLimiter struct {
	allowed bool
	calls   int
	scope   abuse.Scope
	actor   [32]byte
	policy  abuse.Policy
}

func (l *referralLimiter) Consume(_ context.Context, scope abuse.Scope, actor [32]byte, _ time.Time, policy abuse.Policy) (bool, error) {
	l.calls, l.scope, l.actor, l.policy = l.calls+1, scope, actor, policy
	return l.allowed, nil
}

func (a *referralAttributor) ReserveCheckout(_ context.Context, code string, accountID ids.AccountID, requestID, offerCode string, offerVersion uint64) (ids.ReferralAttributionID, error) {
	a.code, a.accountID, a.requestID, a.offerCode, a.offerVersion = code, accountID, requestID, offerCode, offerVersion
	return a.id, nil
}

func (a *referralAttributor) CheckoutAttribution(context.Context, string) (ids.ReferralAttributionID, bool, error) {
	return "", false, nil
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
func (p *serviceProvider) CreateOneTimeCheckoutSession(_ context.Context, command billing.CreateOneTimeCheckoutCommand) (billing.HostedSession, error) {
	p.oneTime = command
	return billing.HostedSession{ID: "cs_purchase", URL: "https://checkout.stripe.com/purchase"}, nil
}
func (p *serviceProvider) CreatePortalSession(_ context.Context, command billing.CreatePortalCommand) (billing.HostedSession, error) {
	p.portalCalls++
	p.portal = command
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
	session, err := service.Checkout(context.Background(), checkoutCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "cs_test" || provider.customerCalls != 1 || provider.checkoutCalls != 1 {
		t.Fatalf("unexpected calls/session: %+v provider=%+v", session, provider)
	}
	if provider.checkout.StripePriceID != "price_private" || provider.checkout.OfferCode != "team-monthly-v1" || provider.checkout.SuccessURL != "https://app.infiniteocean.net/app/checkout?offer=team-monthly-v1&status=billing" || provider.checkout.CancelURL != "https://app.infiniteocean.net/app/checkout?offer=team-monthly-v1&status=billing_cancelled" {
		t.Fatalf("unsafe checkout projection: %+v", provider.checkout)
	}
}

func TestPurchaseCheckoutFreezesTokenBundleBeforeStripe(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	publication := catalog.Default(now.Add(-time.Hour))
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_tokens"}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	command := PurchaseCheckoutCommand{ActorUserID: testUserID, Session: checkoutCommand(now).Session, AccountID: testAccountID, Kind: billing.PurchaseAITokenTopUp, ItemCode: "tokens_10k_v1", RequestID: testRequestID}

	result, err := service.PurchaseCheckout(context.Background(), command)
	if err != nil || result.ID != "cs_purchase" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if repository.purchase.Quantity != 10_000 || repository.purchase.AmountMinor != 1000 || repository.purchase.CatalogVersion != publication.Version || provider.oneTime.Kind != billing.PurchaseAITokenTopUp || provider.oneTime.ItemVersion != 1 || provider.oneTime.SuccessURL != "https://app.infiniteocean.net/app/billing?purchase=ai_token_top_up&status=purchase_returned" {
		t.Fatalf("snapshot=%+v provider=%+v", repository.purchase, provider.oneTime)
	}
}

func TestInitialCheckoutCanIncludeCommissioningOnlyOnce(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	publication := catalog.Default(now.Add(-time.Hour))
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	command := checkoutCommand(now)
	command.OfferCode, command.IncludeCommissioning = "team-monthly-v2", true
	if _, err := service.Checkout(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if repository.checkoutCommissioning == nil || repository.checkoutCommissioning.AmountMinor != 25_000 || provider.checkout.CommissioningCode != "commissioning_v1" || provider.checkout.CommissioningPriceID != "price_private" || provider.checkout.RequestID != testRequestID {
		t.Fatalf("snapshot=%+v provider=%+v", repository.checkoutCommissioning, provider.checkout)
	}
	repository.commissioningOwned = true
	if _, err := service.Checkout(context.Background(), command); !errors.Is(err, ErrCommissioningOwned) {
		t.Fatalf("duplicate commissioning error=%v", err)
	}
}

func TestCheckoutFreezesAffiliateAttributionIntoProviderMetadata(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	referrals := &referralAttributor{id: "44444444-4444-4444-8444-444444444444"}
	limiter := &referralLimiter{allowed: true}
	guard, _ := abuse.NewGuard(limiter)
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test", WithReferralAttributor(referrals, guard))
	command := checkoutCommand(now)
	command.AffiliateCode = "IO-PARTNER1"
	if _, err := service.Checkout(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if referrals.code != "IO-PARTNER1" || referrals.accountID != testAccountID || referrals.requestID != testRequestID || referrals.offerCode != "team-monthly-v1" || referrals.offerVersion != 2 {
		t.Fatalf("referral=%+v", referrals)
	}
	if provider.checkout.AffiliateAttributionID != referrals.id {
		t.Fatalf("provider attribution=%q want=%q", provider.checkout.AffiliateAttributionID, referrals.id)
	}
	if limiter.calls != 1 || limiter.scope != abuse.ScopeAffiliateCode || limiter.policy != abuse.AffiliateCodePolicy ||
		limiter.actor != referralBudgetActor(command.NetworkActor, command.AccountID) {
		t.Fatalf("limiter=%+v", limiter)
	}
}

func TestCheckoutRateLimitsAffiliateValidationBeforeLookup(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	referrals := &referralAttributor{id: "44444444-4444-4444-8444-444444444444"}
	limiter := &referralLimiter{allowed: false}
	guard, _ := abuse.NewGuard(limiter)
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now},
		"https://app.infiniteocean.net", "test", WithReferralAttributor(referrals, guard))
	command := checkoutCommand(now)
	command.AffiliateCode = "IO-GUESS01"
	if _, err := service.Checkout(context.Background(), command); !errors.Is(err, ErrReferralRateLimited) {
		t.Fatalf("error=%v", err)
	}
	if limiter.calls != 1 || referrals.code != "" || provider.checkoutCalls != 0 {
		t.Fatalf("limiter=%+v referral=%+v provider=%+v", limiter, referrals, provider)
	}
}

func TestReferralBudgetActorIsAccountScopedAndFailsClosed(t *testing.T) {
	network := [32]byte{1, 2, 3}
	first := referralBudgetActor(network, testAccountID)
	second := referralBudgetActor(network, "44444444-4444-4444-8444-444444444444")
	if first == ([32]byte{}) || second == ([32]byte{}) || first == second {
		t.Fatalf("first=%x second=%x", first, second)
	}
	if missing := referralBudgetActor([32]byte{}, testAccountID); missing != ([32]byte{}) {
		t.Fatalf("missing network actor=%x", missing)
	}
}

func TestCheckoutRejectsRoleUnknownOfferAndBadIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	publication := paidCatalog(now)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	viewer, _ := access.NewAuthorizer(stateSource{role: accounts.RoleViewer})
	service, _ := New(provider, repository, viewer, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	if _, err := service.Checkout(context.Background(), checkoutCommand(now)); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("expected role denial, got %v", err)
	}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ = New(provider, repository, owner, func() catalog.PublishedCatalog { return publication }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	forged := checkoutCommand(now)
	forged.OfferCode = "forged"
	if _, err := service.Checkout(context.Background(), forged); !errors.Is(err, ErrOfferUnavailable) {
		t.Fatalf("expected offer denial, got %v", err)
	}
	badRequestID := checkoutCommand(now)
	badRequestID.RequestID = "reused-string"
	if _, err := service.Checkout(context.Background(), badRequestID); !errors.Is(err, ErrInvalidRequestID) {
		t.Fatalf("expected request ID denial, got %v", err)
	}
	if provider.checkoutCalls != 0 {
		t.Fatal("Stripe must not be called for denied requests")
	}
}

func TestStatusIsLocalAndSeparatesVisibilityFromManagement(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, commissioningOwned: true}
	provider := &serviceProvider{}
	viewer, _ := access.NewAuthorizer(stateSource{role: accounts.RoleViewer})
	service, _ := New(provider, repository, viewer, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	status, err := service.Status(context.Background(), testUserID, testAccountID)
	if err != nil || !status.HasCustomer || status.CanManage || !status.CommissioningPurchased {
		t.Fatalf("unexpected viewer status: %+v err=%v", status, err)
	}
	if provider.customerCalls+provider.checkoutCalls+provider.portalCalls != 0 {
		t.Fatal("status must not call Stripe")
	}
}

func TestPortalReturnsToVueBillingAndKeepsOpaqueRetryIdentity(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	command := PortalCommand{ActorUserID: testUserID, Session: checkoutCommand(now).Session, AccountID: testAccountID, RequestID: testRequestID}

	result, err := service.Portal(context.Background(), command)
	if err != nil || result.ID != "bps_test" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if provider.portal.ReturnURL != "https://app.infiniteocean.net/app/billing?status=portal_returned" || provider.portal.IdempotencyKey != "spyglass/portal/"+string(testAccountID)+"/"+testRequestID {
		t.Fatalf("portal command=%+v", provider.portal)
	}
}

func TestCheckoutRefusesSecondManagedSubscription(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private", status: Status{Subscriptions: []Subscription{{State: "active", OfferCode: "team-monthly-v1"}}}}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")
	_, err := service.Checkout(context.Background(), checkoutCommand(now))
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
	result, err := service.Checkout(context.Background(), checkoutCommand(now))
	if err != nil || result.ID != "cs_existing" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if provider.customerCalls+provider.checkoutCalls != 0 {
		t.Fatal("durable checkout retry must not create new provider objects")
	}
}

func TestAffiliateValidationBudgetDoesNotBreakDurableCheckoutReplay(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	hosted := billing.HostedSession{ID: "cs_existing", URL: "https://checkout.stripe.com/existing", ExpiresAt: now.Add(time.Hour)}
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private",
		reservation: CheckoutReservation{Resume: &hosted}}
	referrals := &referralAttributor{}
	limiter := &referralLimiter{allowed: false}
	guard, _ := abuse.NewGuard(limiter)
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(&serviceProvider{}, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now},
		"https://app.infiniteocean.net", "test", WithReferralAttributor(referrals, guard))
	command := checkoutCommand(now)
	command.AffiliateCode = "IO-PARTNER1"
	result, err := service.Checkout(context.Background(), command)
	if err != nil || result.ID != hosted.ID || limiter.calls != 0 || referrals.code != "" {
		t.Fatalf("result=%+v limiter=%+v referral=%+v err=%v", result, limiter, referrals, err)
	}
}

func TestBillingMutationsRejectPasswordStaleAndCrossUserEvidence(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{profile: AccountProfile{AccountID: testAccountID, CustomerID: "cus_test"}, price: "price_private"}
	provider := &serviceProvider{}
	owner, _ := access.NewAuthorizer(stateSource{role: accounts.RoleOwner})
	service, _ := New(provider, repository, owner, func() catalog.PublishedCatalog { return paidCatalog(now) }, serviceClock{now}, "https://app.infiniteocean.net", "test")

	cases := map[string]sessions.Session{
		"password":           {UserID: testUserID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword},
		"stale passkey":      {UserID: testUserID, ReauthenticatedAt: now.Add(-strongauth.MaximumAge - time.Second), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		"cross-user passkey": {UserID: ids.UserID("44444444-4444-4444-8444-444444444444"), ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
	}
	for name, evidence := range cases {
		t.Run(name, func(t *testing.T) {
			checkout := checkoutCommand(now)
			checkout.Session = evidence
			if _, err := service.Checkout(context.Background(), checkout); !errors.Is(err, strongauth.ErrRequired) {
				t.Fatalf("checkout error = %v", err)
			}
			portal := PortalCommand{ActorUserID: testUserID, Session: evidence, AccountID: testAccountID, RequestID: testRequestID}
			if _, err := service.Portal(context.Background(), portal); !errors.Is(err, strongauth.ErrRequired) {
				t.Fatalf("portal error = %v", err)
			}
		})
	}
	if provider.customerCalls+provider.checkoutCalls+provider.portalCalls != 0 {
		t.Fatal("rejected authentication evidence reached Stripe")
	}
}

func paidCatalog(now time.Time) catalog.PublishedCatalog {
	definition := catalog.FeaturePackage{Code: catalog.PackageWork, Version: 1, Name: "Work", Features: []string{"work.read"}}
	plan := catalog.Plan{Code: "team", Version: 1, Name: "Team", Packages: map[catalog.PackageCode]catalog.PackageMode{catalog.PackageWork: catalog.ModeEnabled}}
	offer := catalog.Offer{Code: "team-monthly-v1", PlanCode: "team", PlanVersion: 1, Currency: "USD", AmountMinor: 4900, BillingInterval: "month", Published: true, EffectiveFrom: now}
	return catalog.PublishedCatalog{Version: 2, PublishedAt: now, Packages: []catalog.FeaturePackage{definition}, Plans: []catalog.Plan{plan}, Offers: []catalog.Offer{offer}}
}

func checkoutCommand(now time.Time) CheckoutCommand {
	return CheckoutCommand{
		ActorUserID: testUserID,
		Session: sessions.Session{
			UserID:                 testUserID,
			ReauthenticatedAt:      now,
			ReauthenticationMethod: sessions.AuthenticationMethodPasskey,
		},
		AccountID:    testAccountID,
		OfferCode:    "team-monthly-v1",
		RequestID:    testRequestID,
		NetworkActor: [32]byte{9},
	}
}
