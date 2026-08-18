package entitlements

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestEvaluateKeepsAccountsIsolated(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	publication := catalog.PublishedCatalog{Version: 1, Packages: []catalog.FeaturePackage{{Code: catalog.PackageWork}}}
	_, err := Evaluate(ids.AccountID("account-a"), 1, publication, []Grant{{AccountID: ids.AccountID("account-b"), PackageCode: catalog.PackageWork, Mode: catalog.ModeEnabled, StartsAt: now}}, now)
	if err == nil {
		t.Fatal("expected cross-account grant to be rejected")
	}
}

func TestEvaluateUsesHighestPriorityAndReadOnlySemantics(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("account-a")
	publication := catalog.PublishedCatalog{Version: 2, Packages: []catalog.FeaturePackage{{Code: catalog.PackageWork}}}
	snapshot, err := Evaluate(accountID, 4, publication, []Grant{
		{ID: "10000000-0000-4000-8000-000000000001", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, Source: SourceSubscription, StartsAt: now.Add(-time.Hour), Priority: 20},
		{ID: "10000000-0000-4000-8000-000000000002", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeReadOnly, Source: SourceSupportOverride, StartsAt: now.Add(-time.Minute), Priority: 100},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Allows(catalog.PackageWork, false) {
		t.Fatal("read should be allowed")
	}
	if snapshot.Allows(catalog.PackageWork, true) {
		t.Fatal("mutation should be denied in read-only mode")
	}
	if snapshot.Allows(catalog.PackageAgents, false) {
		t.Fatal("missing package should be denied")
	}
}

func TestFreePlanGrantsUseCatalogPackageVersionsAndLimits(t *testing.T) {
	now := time.Now().UTC()
	plan := catalog.Plan{Code: "free", Packages: map[catalog.PackageCode]catalog.PackageMode{catalog.PackageKnowledge: catalog.ModeEnabled}}
	packages := []catalog.FeaturePackage{{Code: catalog.PackageKnowledge, Version: 4, DefaultLimits: map[catalog.LimitCode]int64{"documents": 75}}}
	grants, err := FreePlanGrants(ids.AccountID("account-a"), plan, packages, ids.RandomGenerator{}, now)
	if err != nil || len(grants) != 1 || grants[0].PackageVersion != 4 || grants[0].Limits["documents"] != 75 {
		t.Fatalf("free grants = %+v, %v", grants, err)
	}
	packages[0].DefaultLimits["documents"] = 1
	if grants[0].Limits["documents"] != 75 {
		t.Fatal("free grant limits alias mutable Catalog input")
	}
}

func TestEvaluateCombinesLimitsByGovernedRuleAndStableGrantOrder(t *testing.T) {
	now := time.Now().UTC()
	accountID := ids.AccountID("account-a")
	definitions := []catalog.LimitDefinition{
		{Code: "replace", PackageCode: catalog.PackageWork, Name: "Replace", Unit: "item", Kind: catalog.LimitKindCapacity, Combine: catalog.LimitReplace},
		{Code: "add", PackageCode: catalog.PackageWork, Name: "Add", Unit: "item", Kind: catalog.LimitKindCapacity, Combine: catalog.LimitAdd},
		{Code: "maximum", PackageCode: catalog.PackageWork, Name: "Maximum", Unit: "item", Kind: catalog.LimitKindCapacity, Combine: catalog.LimitMaximum},
		{Code: "minimum", PackageCode: catalog.PackageWork, Name: "Minimum", Unit: "item", Kind: catalog.LimitKindCapacity, Combine: catalog.LimitMinimum},
	}
	grants := []Grant{
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 2, Mode: catalog.ModeEnabled, Source: SourcePromotion, StartsAt: now, Priority: 50, Limits: map[catalog.LimitCode]int64{"replace": 8, "add": 8, "maximum": 8, "minimum": 8}},
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeReadOnly, Source: SourceSubscription, StartsAt: now, Priority: 50, Limits: map[catalog.LimitCode]int64{"replace": 5, "add": 5, "maximum": 5, "minimum": 5}},
	}
	publication := catalog.PublishedCatalog{Version: 7, Packages: []catalog.FeaturePackage{{Code: catalog.PackageWork}}, Limits: definitions}
	snapshot, err := Evaluate(accountID, 4, publication, grants, now)
	if err != nil {
		t.Fatal(err)
	}
	access, ok := snapshot.Package(catalog.PackageWork)
	if !ok || access.Version != 1 || access.Mode != catalog.ModeReadOnly {
		t.Fatalf("stable equal-priority winner = %+v", access)
	}
	for code, expected := range map[catalog.LimitCode]int64{"replace": 5, "add": 13, "maximum": 8, "minimum": 5} {
		if access.Limits[code] != expected || access.LimitPolicies[code].Combine == "" {
			t.Fatalf("combined limit %s = %d policy=%+v, want %d", code, access.Limits[code], access.LimitPolicies[code], expected)
		}
	}
}

func TestEvaluateSuspendsPackageWhenDependencyIsUnavailable(t *testing.T) {
	now := time.Now().UTC()
	accountID := ids.AccountID("account-a")
	publication := catalog.PublishedCatalog{Version: 3, Packages: []catalog.FeaturePackage{
		{Code: catalog.PackageKnowledge},
		{Code: catalog.PackageWork, Dependencies: []catalog.PackageCode{catalog.PackageKnowledge}},
		{Code: catalog.PackageAgents, Dependencies: []catalog.PackageCode{catalog.PackageWork}},
	}}
	snapshot, err := Evaluate(accountID, 1, publication, []Grant{
		{ID: "10000000-0000-4000-8000-000000000001", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, StartsAt: now},
		{ID: "10000000-0000-4000-8000-000000000002", AccountID: accountID, PackageCode: catalog.PackageAgents, PackageVersion: 1, Mode: catalog.ModeEnabled, StartsAt: now},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Allows(catalog.PackageWork, false) || snapshot.Allows(catalog.PackageAgents, false) {
		t.Fatalf("dependent packages escaped unavailable Knowledge dependency: %+v", snapshot.Packages)
	}
}

func TestEvaluatePropagatesReadOnlyDependencyMode(t *testing.T) {
	now := time.Now().UTC()
	accountID := ids.AccountID("account-a")
	publication := catalog.PublishedCatalog{Version: 3, Packages: []catalog.FeaturePackage{
		{Code: catalog.PackageKnowledge},
		{Code: catalog.PackageWork, Dependencies: []catalog.PackageCode{catalog.PackageKnowledge}},
	}}
	snapshot, err := Evaluate(accountID, 1, publication, []Grant{
		{ID: "10000000-0000-4000-8000-000000000001", AccountID: accountID, PackageCode: catalog.PackageKnowledge, PackageVersion: 1, Mode: catalog.ModeReadOnly, StartsAt: now},
		{ID: "10000000-0000-4000-8000-000000000002", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, StartsAt: now},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Allows(catalog.PackageWork, false) || snapshot.Allows(catalog.PackageWork, true) {
		t.Fatalf("Work mode did not inherit read-only dependency: %+v", snapshot.Packages)
	}
}

func TestEvaluateRejectsUndefinedGrantLimit(t *testing.T) {
	now := time.Now().UTC()
	accountID := ids.AccountID("account-a")
	_, err := Evaluate(accountID, 1, catalog.PublishedCatalog{Version: 1, Packages: []catalog.FeaturePackage{{Code: catalog.PackageWork}}}, []Grant{{ID: "10000000-0000-4000-8000-000000000001", AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, Limits: map[catalog.LimitCode]int64{"unreviewed": 5}, StartsAt: now}}, now)
	if err == nil {
		t.Fatal("undefined grant limit was admitted")
	}
}
