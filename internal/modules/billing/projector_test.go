package billing

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type projectionProvider struct {
	current    ProviderSubscription
	retrievals int
}

func (*projectionProvider) CreateCustomer(context.Context, CreateCustomerCommand) (CustomerReference, error) {
	panic("unused")
}
func (*projectionProvider) CreateCheckoutSession(context.Context, CreateCheckoutCommand) (HostedSession, error) {
	panic("unused")
}
func (*projectionProvider) CreatePortalSession(context.Context, CreatePortalCommand) (HostedSession, error) {
	panic("unused")
}
func (p *projectionProvider) RetrieveSubscription(context.Context, string) (ProviderSubscription, error) {
	p.retrievals++
	return p.current, nil
}

type projectionRepository struct {
	mapping MappedOffer
	applied []Projection
}

func (r *projectionRepository) ResolveSubscription(context.Context, ProviderSubscription) (MappedOffer, error) {
	return r.mapping, nil
}
func (r *projectionRepository) ApplyProjection(_ context.Context, p Projection) error {
	r.applied = append(r.applied, p)
	return nil
}

type projectionIDs struct{ next int }

func (g *projectionIDs) New() string {
	g.next++
	return "44444444-4444-4444-8444-" + []string{"444444444441", "444444444442", "444444444443"}[g.next-1]
}

func TestOutOfOrderEventRetrievesCurrentState(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	provider := &projectionProvider{current: ProviderSubscription{ID: "sub_1", CustomerID: "cus_1", State: "canceled", AccountID: testProjectionAccount}}
	repository := &projectionRepository{mapping: testMapping()}
	projector, _ := NewProjector(provider, repository, &projectionIDs{}, projectionClock{now})
	oldEvent := []byte(`{"data":{"object":{"id":"sub_1","status":"active"}}}`)
	for _, eventID := range []string{"evt_new", "evt_old"} {
		if err := projector.Project(context.Background(), WorkItem{Entry: InboxEntry{ProviderEventID: eventID, EventType: "customer.subscription.updated"}, Payload: oldEvent}); err != nil {
			t.Fatal(err)
		}
	}
	if provider.retrievals != 2 || len(repository.applied) != 2 {
		t.Fatalf("expected both invalidations to retrieve/apply: %d %d", provider.retrievals, len(repository.applied))
	}
	for _, applied := range repository.applied {
		if len(applied.Grants) != 0 {
			t.Fatal("stale active event must not restore canceled access")
		}
	}
}

func TestProjectionMakesPastDuePackagesReadOnly(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	provider := &projectionProvider{current: ProviderSubscription{ID: "sub_1", CustomerID: "cus_1", State: "past_due", AccountID: testProjectionAccount, CurrentPeriodStart: now.Add(-time.Hour)}}
	repository := &projectionRepository{mapping: testMapping()}
	projector, _ := NewProjector(provider, repository, &projectionIDs{}, projectionClock{now})
	payload := []byte(`{"data":{"object":{"id":"sub_1"}}}`)
	if err := projector.Project(context.Background(), WorkItem{Entry: InboxEntry{EventType: "customer.subscription.updated"}, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if got := repository.applied[0].Grants; len(got) != 1 || got[0].Mode != catalog.ModeReadOnly || got[0].SourceReference != "sub_1" {
		t.Fatalf("unexpected grants: %+v", got)
	}
}

func TestSubscriptionStatePolicy(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, state string
		paused      bool
		wantGrant   bool
		wantMode    catalog.PackageMode
	}{
		{"active", "active", false, true, catalog.ModeEnabled},
		{"trial", "trialing", false, true, catalog.ModeEnabled},
		{"scheduled cancellation", "active", false, true, catalog.ModeEnabled},
		{"past due", "past_due", false, true, catalog.ModeReadOnly},
		{"collection paused", "active", true, true, catalog.ModeReadOnly},
		{"incomplete", "incomplete", false, false, ""},
		{"expired incomplete", "incomplete_expired", false, false, ""},
		{"unpaid", "unpaid", false, false, ""},
		{"canceled", "canceled", false, false, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subscription := ProviderSubscription{ID: "sub_1", State: test.state, CollectionPaused: test.paused, CurrentPeriodStart: now.Add(-time.Hour)}
			if test.name == "scheduled cancellation" {
				cancelAt := now.Add(time.Hour)
				subscription.CancelAt = &cancelAt
			}
			grants := subscriptionGrants(subscription, testMapping(), &projectionIDs{}, now)
			if !test.wantGrant {
				if len(grants) != 0 {
					t.Fatalf("unexpected grants: %+v", grants)
				}
				return
			}
			if len(grants) != 1 || grants[0].Mode != test.wantMode {
				t.Fatalf("grants=%+v want mode=%q", grants, test.wantMode)
			}
		})
	}
}

const testProjectionAccount ids.AccountID = "55555555-5555-4555-8555-555555555555"

type projectionClock struct{ now time.Time }

func (c projectionClock) Now() time.Time { return c.now }
func testMapping() MappedOffer {
	definition := catalog.FeaturePackage{Code: catalog.PackageWork, Version: 1}
	return MappedOffer{AccountID: testProjectionAccount, CatalogVersion: 2, Offer: catalog.Offer{Code: "team"}, Plan: catalog.Plan{Code: "team", Packages: map[catalog.PackageCode]catalog.PackageMode{catalog.PackageWork: catalog.ModeEnabled}}, Packages: map[catalog.PackageCode]catalog.FeaturePackage{catalog.PackageWork: definition}, Catalog: catalog.PublishedCatalog{Version: 2, Packages: []catalog.FeaturePackage{definition}}}
}
