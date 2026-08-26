package entitlementrollout_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/entitlementrollout"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type store struct {
	work        entitlementrollout.Work
	input       entitlementrollout.Input
	output      entitlementrollout.Output
	claimed     bool
	applyErr    error
	failed      bool
	terminal    bool
	failureCode string
}

func (s *store) EnsureRepairRollout(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (s *store) SeedBatch(context.Context, time.Time, int) (bool, error) { return false, nil }
func (s *store) Claim(context.Context, time.Time, time.Duration) (entitlementrollout.Work, bool, error) {
	return s.work, s.claimed, nil
}
func (s *store) Apply(_ context.Context, _ entitlementrollout.Work, _ time.Time, build func(entitlementrollout.Input) (entitlementrollout.Output, error)) error {
	if s.applyErr != nil {
		return s.applyErr
	}
	output, err := build(s.input)
	s.output = output
	return err
}
func (s *store) MarkFailed(_ context.Context, _ entitlementrollout.Work, _, _ time.Time, code string, terminal bool) error {
	s.failed, s.terminal, s.failureCode = true, terminal, code
	return nil
}

type generator struct{ next int }

func (g *generator) New() string {
	g.next++
	return "10000000-0000-4000-8000-" + string(rune('0'+g.next)) + "00000000000"
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestProcessorReplacesFreePlanAndPreservesIndependentGrants(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	publication := catalog.Default(now)
	publication.Version = 7
	publication.Plans = append(publication.Plans, catalog.Plan{Code: "free", Version: 1, Name: "Legacy Free", Packages: map[catalog.PackageCode]catalog.PackageMode{catalog.PackageKnowledge: catalog.ModeEnabled}})
	for index := range publication.Packages {
		if publication.Packages[index].Code == catalog.PackageKnowledge {
			publication.Packages[index].Version = 4
			publication.Packages[index].DefaultLimits["documents"] = 99
		}
	}
	repository := &store{
		claimed: true,
		work:    entitlementrollout.Work{RolloutID: "rollout", AccountID: accountID, CatalogVersion: 7, AttemptCount: 1},
		input:   entitlementrollout.Input{AccountID: accountID, CurrentVersion: 3, Publication: publication, OtherGrants: []entitlements.Grant{{ID: "subscription", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 2, Mode: catalog.ModeEnabled, Source: entitlements.SourceSubscription, StartsAt: now.Add(-time.Hour), Priority: 50}}},
	}
	processor, _ := entitlementrollout.NewProcessor(repository, &generator{}, clock{now}, time.Minute, 100)
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || len(repository.output.FreeGrants) != 1 {
		t.Fatalf("rollout result: worked=%v output=%+v err=%v", worked, repository.output, err)
	}
	free := repository.output.FreeGrants[0]
	if free.PackageCode != catalog.PackageKnowledge || free.PackageVersion != 4 || free.Limits["documents"] != 99 {
		t.Fatalf("free grant = %+v", free)
	}
	if !repository.output.Snapshot.Allows(catalog.PackageKnowledge, true) || !repository.output.Snapshot.Allows(catalog.PackageWork, true) || repository.output.Snapshot.Version != 4 {
		t.Fatalf("combined snapshot = %+v", repository.output.Snapshot)
	}
}

func TestInvalidCatalogDeadLettersWithoutRetry(t *testing.T) {
	now := time.Now().UTC()
	repository := &store{claimed: true, work: entitlementrollout.Work{RolloutID: "rollout", AccountID: ids.AccountID("account-a"), CatalogVersion: 9, AttemptCount: 1}, input: entitlementrollout.Input{AccountID: ids.AccountID("account-a"), CurrentVersion: 1, Publication: catalog.PublishedCatalog{Version: 9}}}
	processor, _ := entitlementrollout.NewProcessor(repository, &generator{}, clock{now}, time.Minute, 10)
	worked, err := processor.ProcessOne(context.Background())
	if !worked || !errors.Is(err, entitlementrollout.ErrInvalidCatalog) || !repository.failed || !repository.terminal || repository.failureCode != "recompute_failed" {
		t.Fatalf("invalid rollout: worked=%v failed=%v terminal=%v code=%q err=%v", worked, repository.failed, repository.terminal, repository.failureCode, err)
	}
}
