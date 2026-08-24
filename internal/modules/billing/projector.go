package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrUnmappedSubscription         = errors.New("subscription is not mapped to a Spyglass offer")
	ErrSubscriptionMismatch         = errors.New("subscription account mapping is inconsistent")
	ErrAffiliateAttributionMismatch = errors.New("subscription Affiliate attribution is inconsistent")
)

type MappedOffer struct {
	AccountID      ids.AccountID
	CatalogVersion uint64
	Offer          catalog.Offer
	Plan           catalog.Plan
	Packages       map[catalog.PackageCode]catalog.FeaturePackage
	Catalog        catalog.PublishedCatalog
}

type Projection struct {
	Subscription ProviderSubscription
	Mapping      MappedOffer
	Grants       []entitlements.Grant
	SyncedAt     time.Time
}

type ProjectionRepository interface {
	ResolveSubscription(context.Context, ProviderSubscription) (MappedOffer, error)
	ApplyProjection(context.Context, Projection) error
}

type Projector struct {
	provider   Provider
	repository ProjectionRepository
	ids        ids.Generator
	clock      Clock
}

func NewProjector(provider Provider, repository ProjectionRepository, idGenerator ids.Generator, clock Clock) (*Projector, error) {
	if provider == nil || repository == nil || idGenerator == nil || clock == nil {
		return nil, errors.New("billing projector dependencies are required")
	}
	return &Projector{provider: provider, repository: repository, ids: idGenerator, clock: clock}, nil
}

// Project treats the event as an invalidation signal. It always retrieves the
// current subscription, so delayed and out-of-order events converge on current
// provider state instead of replaying stale event snapshots.
func (p *Projector) Project(ctx context.Context, item WorkItem) error {
	subscriptionID := subscriptionID(item.Entry.EventType, item.Payload)
	if subscriptionID == "" {
		return nil
	}
	return p.Refresh(ctx, subscriptionID)
}

func (p *Projector) Refresh(ctx context.Context, subscriptionID string) error {
	current, err := p.provider.RetrieveSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	mapping, err := p.repository.ResolveSubscription(ctx, current)
	if err != nil {
		return err
	}
	if current.AccountID != "" && current.AccountID != mapping.AccountID {
		return ErrSubscriptionMismatch
	}
	if current.OfferCode != "" && current.OfferCode != mapping.Offer.Code {
		return ErrSubscriptionMismatch
	}
	if current.OfferVersion != 0 && current.OfferVersion != mapping.CatalogVersion {
		return ErrSubscriptionMismatch
	}
	if mapping.Catalog.Version != mapping.CatalogVersion {
		return ErrSubscriptionMismatch
	}
	if current.AffiliateAttributionID != "" && ids.Validate(string(current.AffiliateAttributionID)) != nil {
		return ErrAffiliateAttributionMismatch
	}
	now := p.clock.Now().UTC()
	grants := subscriptionGrants(current, mapping, p.ids, now)
	return p.repository.ApplyProjection(ctx, Projection{Subscription: current, Mapping: mapping, Grants: grants, SyncedAt: now})
}

func subscriptionID(eventType string, payload []byte) string {
	var event struct {
		Data struct {
			Object struct {
				ID           string          `json:"id"`
				Subscription json.RawMessage `json:"subscription"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return ""
	}
	if strings.HasPrefix(eventType, "customer.subscription.") && strings.HasPrefix(event.Data.Object.ID, "sub_") {
		return event.Data.Object.ID
	}
	if eventType == "checkout.session.completed" || strings.HasPrefix(eventType, "invoice.") {
		var id string
		if json.Unmarshal(event.Data.Object.Subscription, &id) == nil && strings.HasPrefix(id, "sub_") {
			return id
		}
		var expanded struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(event.Data.Object.Subscription, &expanded) == nil && strings.HasPrefix(expanded.ID, "sub_") {
			return expanded.ID
		}
	}
	return ""
}

func subscriptionGrants(subscription ProviderSubscription, mapping MappedOffer, idGenerator ids.Generator, now time.Time) []entitlements.Grant {
	modeAllowed := subscription.State == "active" || subscription.State == "trialing" || subscription.State == "past_due"
	if !modeAllowed {
		return nil
	}
	startsAt := subscription.CurrentPeriodStart
	if startsAt.IsZero() || startsAt.After(now) {
		startsAt = now
	}
	grants := make([]entitlements.Grant, 0, len(mapping.Plan.Packages))
	for code, mode := range mapping.Plan.Packages {
		definition, ok := mapping.Packages[code]
		if !ok {
			continue
		}
		if subscription.State == "past_due" || subscription.CollectionPaused {
			mode = catalog.ModeReadOnly
		}
		limits := make(map[catalog.LimitCode]int64, len(definition.DefaultLimits))
		for name, value := range definition.DefaultLimits {
			limits[name] = value
		}
		grants = append(grants, entitlements.Grant{ID: ids.GrantID(idGenerator.New()), AccountID: mapping.AccountID, PackageCode: code, PackageVersion: definition.Version, Mode: mode, Source: entitlements.SourceSubscription, SourceReference: subscription.ID, Limits: limits, StartsAt: startsAt, Priority: 50, Reason: "Stripe subscription " + subscription.State})
	}
	return grants
}

var _ EventHandler = (*Projector)(nil)
