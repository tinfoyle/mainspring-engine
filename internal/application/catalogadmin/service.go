// Package catalogadmin owns the reviewed, audited Catalog publication workflow.
package catalogadmin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type State string

const (
	StateDraft     State = "draft"
	StateInReview  State = "in_review"
	StateApproved  State = "approved"
	StatePublished State = "published"
	StateRetired   State = "retired"
)

var (
	ErrInvalidChange     = errors.New("catalog operator change is invalid")
	ErrInvalidTransition = errors.New("catalog state transition is invalid")
	ErrReviewSeparation  = errors.New("catalog reviewer must differ from draft creator")
	ErrOfferMapping      = errors.New("catalog offer mapping is invalid or incomplete")
)

type Publication struct {
	Version      uint64
	State        State
	ContentHash  []byte
	CreatedAt    time.Time
	CreatedBy    string
	PublishedAt  *time.Time
	ReviewedBy   string
	PublishedBy  string
	ChangeReason string
}

type Change struct {
	EventID string
	Actor   string
	Reason  string
	At      time.Time
}

type Store interface {
	CreateDraft(context.Context, catalog.PublishedCatalog, Change) (Publication, error)
	MapPrice(context.Context, uint64, string, string, string, Change) error
	RequestReview(context.Context, uint64, Change) (Publication, error)
	Approve(context.Context, uint64, Change) (Publication, error)
	Publish(context.Context, uint64, time.Time, Change) (Publication, error)
	Retire(context.Context, uint64, Change) (Publication, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	store Store
	ids   ids.Generator
	clock Clock
}

func NewService(store Store, generator ids.Generator, clock Clock) (*Service, error) {
	if store == nil || generator == nil || clock == nil {
		return nil, errors.New("catalog administration dependencies are required")
	}
	return &Service{store: store, ids: generator, clock: clock}, nil
}

func (s *Service) CreateDraft(ctx context.Context, content catalog.PublishedCatalog, actor, reason string) (Publication, error) {
	content.Version = 1 // The store replaces this under its serialized version lock.
	content.PublishedAt = time.Time{}
	for index := range content.Offers {
		content.Offers[index].Published = false
	}
	if err := content.ValidateGoverned(); err != nil {
		return Publication{}, errors.Join(ErrInvalidChange, err)
	}
	change, err := s.change(actor, reason)
	if err != nil {
		return Publication{}, err
	}
	return s.store.CreateDraft(ctx, content, change)
}

func (s *Service) MapStripePrice(ctx context.Context, version uint64, offerCode, mode, priceID, actor, reason string) error {
	if version == 0 || strings.TrimSpace(offerCode) == "" || (mode != "test" && mode != "live") || !strings.HasPrefix(priceID, "price_") || len(priceID) > 255 {
		return ErrOfferMapping
	}
	change, err := s.change(actor, reason)
	if err != nil {
		return err
	}
	return s.store.MapPrice(ctx, version, offerCode, mode, priceID, change)
}

func (s *Service) RequestReview(ctx context.Context, version uint64, actor, reason string) (Publication, error) {
	return s.transition(ctx, version, actor, reason, s.store.RequestReview)
}

func (s *Service) Approve(ctx context.Context, version uint64, actor, reason string) (Publication, error) {
	return s.transition(ctx, version, actor, reason, s.store.Approve)
}

func (s *Service) Publish(ctx context.Context, version uint64, effectiveAt time.Time, actor, reason string) (Publication, error) {
	if effectiveAt.IsZero() {
		effectiveAt = s.clock.Now().UTC()
	}
	if effectiveAt.Before(s.clock.Now().UTC().Add(-time.Minute)) {
		return Publication{}, errors.Join(ErrInvalidChange, errors.New("catalog effective time is in the past"))
	}
	change, err := s.change(actor, reason)
	if err != nil {
		return Publication{}, err
	}
	return s.store.Publish(ctx, version, effectiveAt.UTC(), change)
}

func (s *Service) Retire(ctx context.Context, version uint64, actor, reason string) (Publication, error) {
	return s.transition(ctx, version, actor, reason, s.store.Retire)
}

func (s *Service) transition(ctx context.Context, version uint64, actor, reason string, action func(context.Context, uint64, Change) (Publication, error)) (Publication, error) {
	if version == 0 {
		return Publication{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason)
	if err != nil {
		return Publication{}, err
	}
	return action(ctx, version, change)
}

func (s *Service) change(actor, reason string) (Change, error) {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") || len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: s.ids.New(), Actor: actor, Reason: reason, At: s.clock.Now().UTC()}, nil
}
