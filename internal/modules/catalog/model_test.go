package catalog

import (
	"testing"
	"time"
)

func TestDefaultCatalogIsValid(t *testing.T) {
	if err := Default(time.Now()).Validate(); err != nil {
		t.Fatal(err)
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
