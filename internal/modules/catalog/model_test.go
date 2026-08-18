package catalog

import (
	"testing"
	"time"
)

func TestDefaultCatalogIsValid(t *testing.T) {
	if err := Default(time.Now()).ValidateGoverned(); err != nil {
		t.Fatal(err)
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
		if catalog.Plans[index].Code == "operating" {
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

func TestCatalogRequiresFreePlan(t *testing.T) {
	value := Default(time.Now())
	value.Plans = value.Plans[1:]
	if err := value.Validate(); err == nil {
		t.Fatal("expected missing free plan rejection")
	}
}
