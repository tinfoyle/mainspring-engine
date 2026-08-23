package integrationsync_test

import (
	"context"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type syncAuthoritySource struct{ state access.WorkloadState }

func (source *syncAuthoritySource) WorkloadAccessState(context.Context, ids.AccountID) (access.WorkloadState, error) {
	return source.state, nil
}

func TestCurrentAuthorityRequiresKnowledgeAndIntegrationsButNotMarketing(t *testing.T) {
	accountID := ids.AccountID("e1000000-0000-4000-8000-000000000001")
	state := syncAuthorityState(accountID)
	authority, err := integrationsync.NewCurrentAuthority(&syncAuthoritySource{state: state}, "cell-a")
	if err != nil || authority.AuthorizeAccount(context.Background(), accountID) != nil {
		t.Fatalf("authority=%v err=%v", authority, err)
	}
	state.Entitlements.Packages[0].Mode = catalog.ModeReadOnly
	authority, _ = integrationsync.NewCurrentAuthority(&syncAuthoritySource{state: state}, "cell-a")
	if err := authority.AuthorizeAccount(context.Background(), accountID); !access.IsDenied(err, access.DenialPackageReadOnly) {
		t.Fatalf("read-only Knowledge result=%v", err)
	}
	state = syncAuthorityState(accountID)
	state.Account.CellID = "cell-b"
	authority, _ = integrationsync.NewCurrentAuthority(&syncAuthoritySource{state: state}, "cell-a")
	if err := authority.AuthorizeAccount(context.Background(), accountID); !access.IsDenied(err, access.DenialCorruptContext) {
		t.Fatalf("placement drift result=%v", err)
	}
}

func syncAuthorityState(accountID ids.AccountID) access.WorkloadState {
	return access.WorkloadState{Account: accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: "cell-a", PlacementGeneration: 2, EntitlementVersion: 7},
		Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 7, Packages: []entitlements.PackageAccess{
			{Code: catalog.PackageKnowledge, Version: 1, Mode: catalog.ModeEnabled},
			{Code: catalog.PackageIntegrations, Version: 1, Mode: catalog.ModeEnabled},
		}}}
}
