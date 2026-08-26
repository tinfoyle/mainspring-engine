package catalog

import (
	"testing"
	"time"
)

func TestDefaultCatalogIsValid(t *testing.T) {
	value := Default(time.Now())
	if err := value.ValidateGoverned(); err != nil {
		t.Fatal(err)
	}
	if value.Version != 3 || len(value.Plans) != 1 || value.Plans[0].Code != "team" || value.Plans[0].Version != 2 || len(value.Offers) != 1 || value.Offers[0].Code != "team-monthly-v2" || value.Offers[0].AmountMinor != 5000 {
		t.Fatalf("default launch Catalog = %+v", value)
	}
	if len(value.Plans[0].Packages) != len(value.Packages) {
		t.Fatalf("complete-product plan packages=%d definitions=%d", len(value.Plans[0].Packages), len(value.Packages))
	}
}

func TestGovernedCatalogRequiresExplicitDefinitionsForDefaults(t *testing.T) {
	value := Default(time.Now())
	value.Limits = nil
	if err := value.Validate(); err != nil {
		t.Fatalf("legacy published Catalog should remain rollback-compatible: %v", err)
	}
	if err := value.ValidateGoverned(); err == nil {
		t.Fatal("new governed Catalog accepted implicit limit semantics")
	}
}

func TestEffectiveLimitDefinitionsUsesStableEmptyArray(t *testing.T) {
	definitions := (PublishedCatalog{}).EffectiveLimitDefinitions()
	if definitions == nil || len(definitions) != 0 {
		t.Fatalf("effective definitions = %#v, want non-nil empty slice", definitions)
	}
}

func TestCatalogRejectsInvalidLimitDefinition(t *testing.T) {
	value := Default(time.Now())
	value.Limits[0].ReservationTTLSeconds = int64((31 * 24 * time.Hour) / time.Second)
	if err := value.Validate(); err == nil {
		t.Fatal("expected unsafe reservation TTL rejection")
	}
}

func TestCatalogRejectsUnsafeLimitIdentity(t *testing.T) {
	value := Default(time.Now())
	value.Limits[0].Code = "Documents Per Account"
	if err := value.Validate(); err == nil {
		t.Fatal("expected non-machine limit code rejection")
	}
}
func TestCatalogRejectsDependencyCycles(t *testing.T) {
	value := Default(time.Now())
	value.Packages = []FeaturePackage{{Code: PackageWork, Version: 1, Name: "Work", Dependencies: []PackageCode{PackageAgents}}, {Code: PackageAgents, Version: 1, Name: "Agents", Dependencies: []PackageCode{PackageWork}}}
	value.Plans = []Plan{{Code: "free", Version: 1, Name: "Free", Packages: map[PackageCode]PackageMode{PackageWork: ModeEnabled}}}
	if err := value.Validate(); err == nil {
		t.Fatal("expected dependency cycle rejection")
	}
}
func TestCatalogRejectsOfferPlanVersionMismatch(t *testing.T) {
	value := Default(time.Now())
	value.Offers[0].PlanVersion = 99
	if err := value.Validate(); err == nil {
		t.Fatal("expected offer version rejection")
	}
}

func TestCatalogRejectsPlanWithMissingPackageDependency(t *testing.T) {
	catalog := Default(time.Now())
	for index := range catalog.Plans {
		if catalog.Plans[index].Code == "team" {
			delete(catalog.Plans[index].Packages, PackageKnowledge)
		}
	}
	if err := catalog.Validate(); err == nil {
		t.Fatal("expected missing package dependency to be rejected")
	}
}

func TestCatalogRejectsEmptyCommercialSurface(t *testing.T) {
	if err := (PublishedCatalog{Version: 1}).Validate(); err == nil {
		t.Fatal("expected empty catalog rejection")
	}
}

func TestCatalogDoesNotRequireFreePlan(t *testing.T) {
	value := Default(time.Now())
	if _, exists := value.Plan("free"); exists {
		t.Fatal("launch Catalog unexpectedly contains a Free plan")
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("paid-only Catalog rejected: %v", err)
	}
}
