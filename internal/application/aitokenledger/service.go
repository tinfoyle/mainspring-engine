// Package aitokenledger is the authorization and idempotency boundary around
// the shared team AI Token ledger. Browser, run admission, and signed billing
// projections use this service instead of editing grant totals directly.
package aitokenledger

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidRequest    = errors.New("AI Token ledger request is invalid")
	ErrRateUnavailable   = errors.New("AI complexity rate is unavailable")
	ErrBundleUnavailable = errors.New("AI Token bundle is unavailable")
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
	grant, err := aitokens.NewGrant(ids.AITokenGrantID(s.ids.New()), accountID, aitokens.OriginPurchased, bundle.Code, publication.Version, sourceReference, bundle.Quantity, nil, now)
	if err != nil {
		return aitokens.Grant{}, aitokens.Balance{}, err
	}
	return s.store.Issue(ctx, grant, false)
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
