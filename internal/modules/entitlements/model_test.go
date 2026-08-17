package entitlements

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestEvaluateKeepsAccountsIsolated(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	_, err := Evaluate(ids.AccountID("account-a"), 1, 1, []Grant{{AccountID: ids.AccountID("account-b"), PackageCode: catalog.PackageWork, Mode: catalog.ModeEnabled, StartsAt: now}}, now)
	if err == nil {
		t.Fatal("expected cross-account grant to be rejected")
	}
}

func TestEvaluateUsesHighestPriorityAndReadOnlySemantics(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("account-a")
	snapshot, err := Evaluate(accountID, 4, 2, []Grant{
		{AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, Source: SourceSubscription, StartsAt: now.Add(-time.Hour), Priority: 20},
		{AccountID: accountID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeReadOnly, Source: SourceSupportOverride, StartsAt: now.Add(-time.Minute), Priority: 100},
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
