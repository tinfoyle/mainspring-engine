// Package aitokenledger is the authorization and idempotency boundary around
// the shared team AI Token ledger. Browser, run admission, and signed billing
// projections use this service instead of editing grant totals directly.
package aitokenledger

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidRequest         = errors.New("AI Token ledger request is invalid")
	ErrRateUnavailable        = errors.New("AI complexity rate is unavailable")
	ErrBundleUnavailable      = errors.New("AI Token bundle is unavailable")
	ErrPromotionUnavailable   = errors.New("AI Token promotion is unavailable")
	ErrPromotionAccountLimit  = errors.New("AI Token promotion Account limit has been reached")
	ErrPromotionIssuanceLimit = errors.New("AI Token promotion issuance limit has been reached")
	ErrPromotionConflict      = errors.New("AI Token promotion request conflicts with its original redemption")
)

type Usage struct {
	ProviderStarted   bool
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	ToolInvocations   int64
}

type Store interface {
	Balance(context.Context, ids.AccountID, time.Time) (aitokens.Balance, error)
	Reserve(context.Context, aitokens.Reservation, time.Time) (aitokens.Reservation, aitokens.Balance, error)
	Close(context.Context, ids.AccountID, string, Usage, time.Time) (aitokens.Reservation, aitokens.Balance, error)
	Issue(context.Context, aitokens.Grant, bool) (aitokens.Grant, aitokens.Balance, error)
	Promotion(context.Context, ids.AccountID, string, time.Time) (aitokens.Grant, aitokens.Balance, bool, error)
	RedeemPromotion(context.Context, aitokens.Grant, uint64, int64, int64) (aitokens.Grant, aitokens.Balance, error)
	ReverseUnused(context.Context, ids.AccountID, aitokens.GrantOrigin, string, string, time.Time) (aitokens.Grant, aitokens.Balance, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	store      Store
	authorizer Authorizer
	catalog    func() catalog.PublishedCatalog
	ids        ids.Generator
	clock      Clock
	issuer     *Issuer
}

func New(store Store, authorizer Authorizer, catalogSource func() catalog.PublishedCatalog, generator ids.Generator, clock Clock) (*Service, error) {
	if store == nil || authorizer == nil || catalogSource == nil || generator == nil || clock == nil {
		return nil, errors.New("AI Token ledger dependencies are required")
	}
	issuer, _ := NewIssuer(store, catalogSource, generator, clock)
	return &Service{store: store, authorizer: authorizer, catalog: catalogSource, ids: generator, clock: clock, issuer: issuer}, nil
}

// Issuer is the narrower signed-billing projection boundary. It intentionally
// has no human authorizer because callers must already be inside the verified,
// retrieved provider-event worker.
type Issuer struct {
	store   Store
	catalog func() catalog.PublishedCatalog
	ids     ids.Generator
	clock   Clock
}

func NewIssuer(store Store, catalogSource func() catalog.PublishedCatalog, generator ids.Generator, clock Clock) (*Issuer, error) {
	if store == nil || catalogSource == nil || generator == nil || clock == nil {
		return nil, errors.New("AI Token issuer dependencies are required")
	}
	return &Issuer{store: store, catalog: catalogSource, ids: generator, clock: clock}, nil
}

func (s *Service) Balance(ctx context.Context, actor access.Actor, accountID ids.AccountID) (aitokens.Balance, error) {
	if !actor.Valid() || accountID == "" {
		return aitokens.Balance{}, ErrInvalidRequest
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{}); err != nil {
		return aitokens.Balance{}, err
	}
	return s.store.Balance(ctx, accountID, s.clock.Now().UTC())
}

type ReserveCommand struct {
	Actor      access.Actor
	AccountID  ids.AccountID
	RequestID  string
	Complexity catalog.AIComplexity
}

func (s *Service) Reserve(ctx context.Context, command ReserveCommand) (aitokens.Reservation, aitokens.Balance, error) {
	if !command.Actor.Valid() || command.AccountID == "" || ids.Validate(command.RequestID) != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, ErrInvalidRequest
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageAgents, Mutation: true}); err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	rate, exists := complexityRate(s.catalog(), command.Complexity)
	if !exists {
		return aitokens.Reservation{}, aitokens.Balance{}, ErrRateUnavailable
	}
	reservation := aitokens.Reservation{ID: ids.AITokenReservationID(s.ids.New()), AccountID: command.AccountID, RequestID: command.RequestID, Rate: rate, Maximum: rate.MaximumReservation, State: aitokens.ReservationActive}
	return s.store.Reserve(ctx, reservation, s.clock.Now().UTC())
}

// Close is worker-facing. It uses the rate snapshot frozen in the durable
// reservation and therefore needs no current membership or Catalog lookup.
func (s *Service) Close(ctx context.Context, accountID ids.AccountID, requestID string, usage Usage) (aitokens.Reservation, aitokens.Balance, error) {
	if accountID == "" || ids.Validate(requestID) != nil || usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.OutputTokens < 0 || usage.ToolInvocations < 0 || usage.CachedInputTokens > usage.InputTokens {
		return aitokens.Reservation{}, aitokens.Balance{}, ErrInvalidRequest
	}
	return s.store.Close(ctx, accountID, requestID, usage, s.clock.Now().UTC())
}

// IssueIncluded is called only from a signed, retrieved Stripe projection for
// a positive paid service period. The store expires prior included cohorts and
// enforces exact-once source identity in the same transaction.
func (s *Service) IssueIncluded(ctx context.Context, accountID ids.AccountID, sourceReference string) (aitokens.Grant, aitokens.Balance, error) {
	return s.issuer.IssueIncluded(ctx, accountID, sourceReference)
}

func (s *Issuer) IssueIncluded(ctx context.Context, accountID ids.AccountID, sourceReference string) (aitokens.Grant, aitokens.Balance, error) {
	publication := s.catalog()
	definition := publication.AITokenRenewalGrant
	if accountID == "" || sourceReference == "" || definition == nil {
		return aitokens.Grant{}, aitokens.Balance{}, ErrInvalidRequest
	}
	now := s.clock.Now().UTC()
	grant, err := aitokens.NewGrant(ids.AITokenGrantID(s.ids.New()), accountID, aitokens.OriginIncluded, definition.Code, publication.Version, sourceReference, definition.Quantity, nil, now)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return s.store.Issue(ctx, grant, true)
}

func (s *Service) IssuePurchased(ctx context.Context, accountID ids.AccountID, bundleCode, sourceReference string) (aitokens.Grant, aitokens.Balance, error) {
	return s.issuer.IssuePurchased(ctx, accountID, bundleCode, sourceReference)
}

func (s *Issuer) IssuePurchased(ctx context.Context, accountID ids.AccountID, bundleCode, sourceReference string) (aitokens.Grant, aitokens.Balance, error) {
	publication, now := s.catalog(), s.clock.Now().UTC()
	bundle, exists := tokenBundle(publication, bundleCode, now)
	if accountID == "" || sourceReference == "" || !exists {
		return aitokens.Grant{}, aitokens.Balance{}, ErrBundleUnavailable
	}
	return s.IssuePurchasedSnapshot(ctx, accountID, bundle.Code, bundle.Version, publication.Version, bundle.Quantity, sourceReference)
}

// IssuePurchasedSnapshot is fed only by the verified local Checkout-attempt
// projection. It deliberately avoids the current Catalog so a price or
// quantity publication made after the customer left for Stripe cannot change
// what their completed purchase grants.
func (s *Issuer) IssuePurchasedSnapshot(ctx context.Context, accountID ids.AccountID, bundleCode string, bundleVersion, catalogVersion uint64, quantity int64, sourceReference string) (aitokens.Grant, aitokens.Balance, error) {
	now := s.clock.Now().UTC()
	if accountID == "" || !validLedgerCode(bundleCode) || bundleVersion == 0 || catalogVersion == 0 || quantity <= 0 || sourceReference == "" {
		return aitokens.Grant{}, aitokens.Balance{}, ErrInvalidRequest
	}
	grant, err := aitokens.NewGrant(ids.AITokenGrantID(s.ids.New()), accountID, aitokens.OriginPurchased, bundleCode, catalogVersion, sourceReference, quantity, nil, now)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return s.store.Issue(ctx, grant, false)
}

type RedeemPromotionCommand struct {
	ActorUserID   ids.UserID
	Session       sessions.Session
	AccountID     ids.AccountID
	PromotionCode string
	RequestID     string
}

// RedeemPromotion converts a currently effective Catalog campaign into one
// expiring Account grant. Durable request identity is checked before the
// current Catalog so an exact retry remains stable after a later publication.
func (s *Service) RedeemPromotion(ctx context.Context, command RedeemPromotionCommand) (aitokens.Grant, aitokens.Balance, error) {
	if command.ActorUserID == "" || command.AccountID == "" || ids.Validate(command.RequestID) != nil || !validLedgerCode(command.PromotionCode) {
		return aitokens.Grant{}, aitokens.Balance{}, ErrInvalidRequest
	}
	if _, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleBillingAdmin}}); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	now := s.clock.Now().UTC()
	if grant, balance, exists, err := s.store.Promotion(ctx, command.AccountID, command.RequestID, now); err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	} else if exists {
		if grant.DefinitionCode != command.PromotionCode {
			return aitokens.Grant{}, aitokens.Balance{}, ErrPromotionConflict
		}
		return grant, balance, nil
	}
	publication := s.catalog()
	promotion, exists := tokenPromotion(publication, command.PromotionCode, now)
	if !exists {
		return aitokens.Grant{}, aitokens.Balance{}, ErrPromotionUnavailable
	}
	expiresAt := now.AddDate(0, 0, int(promotion.ExpiresAfterDays))
	grant, err := aitokens.NewGrant(ids.AITokenGrantID(s.ids.New()), command.AccountID, aitokens.OriginPromotion, promotion.Code, publication.Version, command.RequestID, promotion.Quantity, &expiresAt, now)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return s.store.RedeemPromotion(ctx, grant, promotion.Version, promotion.RedemptionsPerAccount, promotion.IssuanceCap)
}

func validLedgerCode(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

// ReversePurchased removes only the surviving unused remainder associated
// with one paid top-up. Settled consumption remains immutable and the store
// refuses to race an active reservation.
func (s *Issuer) ReversePurchased(ctx context.Context, accountID ids.AccountID, paymentIntentID, adversityReference string) (aitokens.Grant, aitokens.Balance, error) {
	if accountID == "" || !strings.HasPrefix(paymentIntentID, "pi_") || adversityReference == "" {
		return aitokens.Grant{}, aitokens.Balance{}, ErrInvalidRequest
	}
	return s.store.ReverseUnused(ctx, accountID, aitokens.OriginPurchased, paymentIntentID, adversityReference, s.clock.Now().UTC())
}

func complexityRate(publication catalog.PublishedCatalog, complexity catalog.AIComplexity) (catalog.AIComplexityRate, bool) {
	for _, rate := range publication.AIComplexityRates {
		if rate.Complexity == complexity {
			return rate, true
		}
	}
	return catalog.AIComplexityRate{}, false
}

func tokenBundle(publication catalog.PublishedCatalog, code string, now time.Time) (catalog.AITokenBundle, bool) {
	for _, bundle := range publication.AITokenBundles {
		if bundle.Code == code && !bundle.EffectiveFrom.After(now) {
			return bundle, true
		}
	}
	return catalog.AITokenBundle{}, false
}

func tokenPromotion(publication catalog.PublishedCatalog, code string, now time.Time) (catalog.AITokenPromotion, bool) {
	for _, promotion := range publication.AITokenPromotions {
		if promotion.Code == code && !promotion.EffectiveFrom.After(now) && promotion.EffectiveUntil.After(now) {
			return promotion, true
		}
	}
	return catalog.AITokenPromotion{}, false
}
