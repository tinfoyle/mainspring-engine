package architecture_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type packageSurfaceInventory struct {
	SchemaVersion int `json:"schema_version"`
	Packages      []struct {
		Code       string   `json:"code"`
		Executable bool     `json:"executable"`
		Boundaries []string `json:"boundaries"`
	} `json:"packages"`
	AbsentSurfaceKinds           []string `json:"absent_surface_kinds"`
	EnabledExternalAccountStores []string `json:"enabled_external_account_stores"`
}

func TestPackageSurfaceInventoryIsExplicit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "package-surface-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory packageSurfaceInventory
	if err := json.Unmarshal(raw, &inventory); err != nil || inventory.SchemaVersion != 1 {
		t.Fatalf("invalid package surface inventory: version=%d err=%v", inventory.SchemaVersion, err)
	}
	wantPackages := []string{"agents", "finance", "integrations", "knowledge", "marketing", "work"}
	packages := make([]string, 0, len(inventory.Packages))
	for _, item := range inventory.Packages {
		packages = append(packages, item.Code)
		if item.Executable != (item.Code == "work" || item.Code == "agents") {
			t.Fatalf("package %q executable=%t", item.Code, item.Executable)
		}
		if item.Executable && len(item.Boundaries) == 0 || !item.Executable && len(item.Boundaries) != 0 {
			t.Fatalf("package %q has inconsistent boundaries %v", item.Code, item.Boundaries)
		}
	}
	slices.Sort(packages)
	if !slices.Equal(packages, wantPackages) {
		t.Fatalf("packages=%v want=%v", packages, wantPackages)
	}
	for _, absent := range []string{"production-mcp", "customer-schedule", "connector-runtime", "customer-export-api", "object-store", "search-vector-store", "analytics-export-store"} {
		if !slices.Contains(inventory.AbsentSurfaceKinds, absent) {
			t.Errorf("absent surface %q is not declared", absent)
		}
	}
	if len(inventory.EnabledExternalAccountStores) != 0 {
		t.Fatal("an external Account store requires movement, export, erasure, attestation, and restore handlers before enablement")
	}
}
