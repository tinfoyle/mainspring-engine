package catalogadmin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

type store struct {
	draft  catalog.PublishedCatalog
	change catalogadmin.Change
	price  string
}

func (s *store) CreateDraft(_ context.Context, content catalog.PublishedCatalog, change catalogadmin.Change) (catalogadmin.Publication, error) {
	s.draft, s.change = content, change
	return catalogadmin.Publication{Version: 3, State: catalogadmin.StateDraft}, nil
}
func (s *store) MapPrice(_ context.Context, _ uint64, _, _, price string, change catalogadmin.Change) error {
	s.price, s.change = price, change
	return nil
}
func (s *store) RequestReview(context.Context, uint64, catalogadmin.Change) (catalogadmin.Publication, error) {
	return catalogadmin.Publication{State: catalogadmin.StateInReview}, nil
}
func (s *store) Approve(context.Context, uint64, catalogadmin.Change) (catalogadmin.Publication, error) {
	return catalogadmin.Publication{State: catalogadmin.StateApproved}, nil
}
func (s *store) Publish(context.Context, uint64, time.Time, catalogadmin.Change) (catalogadmin.Publication, error) {
	return catalogadmin.Publication{State: catalogadmin.StatePublished}, nil
}
func (s *store) Retire(context.Context, uint64, catalogadmin.Change) (catalogadmin.Publication, error) {
	return catalogadmin.Publication{State: catalogadmin.StateRetired}, nil
}

type generator struct{}

func (generator) New() string { return "10000000-0000-4000-8000-000000000001" }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestDraftValidationAndOperatorAttribution(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &store{}
	service, _ := catalogadmin.NewService(repository, generator{}, clock{now})
	content := catalog.Default(now)
	content.Version = 99
	content.PublishedAt = now
	publication, err := service.CreateDraft(context.Background(), content, "operator@example.com", "introduce annual commercial offers")
	if err != nil || publication.Version != 3 {
		t.Fatalf("draft result = %+v, %v", publication, err)
	}
	if repository.draft.Version != 1 || !repository.draft.PublishedAt.IsZero() || repository.change.Actor != "operator@example.com" || repository.change.EventID == "" {
		t.Fatalf("normalized draft=%+v change=%+v", repository.draft, repository.change)
	}
	if _, err := service.CreateDraft(context.Background(), catalog.PublishedCatalog{}, "operator@example.com", "invalid empty catalog"); !errors.Is(err, catalogadmin.ErrInvalidChange) {
		t.Fatalf("invalid catalog result = %v", err)
	}
	implicitLimits := content
	implicitLimits.Limits = nil
	if _, err := service.CreateDraft(context.Background(), implicitLimits, "operator@example.com", "attempt draft with implicit limit policy"); !errors.Is(err, catalogadmin.ErrInvalidChange) {
		t.Fatalf("implicit limit draft result = %v", err)
	}
	if _, err := service.CreateDraft(context.Background(), content, "operator@example.com", "short"); !errors.Is(err, catalogadmin.ErrInvalidChange) {
		t.Fatalf("short reason result = %v", err)
	}
}

func TestPriceAndPublicationInputsFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &store{}
	service, _ := catalogadmin.NewService(repository, generator{}, clock{now})
	if err := service.MapStripePrice(context.Background(), 3, "team-monthly-v1", "test", "price_valid", "operator", "map reviewed test price"); err != nil || repository.price != "price_valid" {
		t.Fatal(err)
	}
	if err := service.MapStripePrice(context.Background(), 3, "team-monthly-v1", "sandbox", "arbitrary", "operator", "map invalid provider price"); !errors.Is(err, catalogadmin.ErrOfferMapping) {
		t.Fatalf("invalid mapping result = %v", err)
	}
	if _, err := service.Publish(context.Background(), 3, now.Add(-2*time.Minute), "operator", "publish approved catalog"); !errors.Is(err, catalogadmin.ErrInvalidChange) {
		t.Fatalf("past publication result = %v", err)
	}
}
