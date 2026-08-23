package integrationexecution_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type authoritySource struct {
	state access.WorkloadState
	err   error
	calls int
}

func (source *authoritySource) WorkloadAccessState(context.Context, ids.AccountID) (access.WorkloadState, error) {
	source.calls++
	return source.state, source.err
}

func TestCurrentAuthorityRequiresOneCurrentPlacementAndDualEnabledPackages(t *testing.T) {
	claim := integrationexecution.Claim{AccountID: "b1100000-0000-4000-8000-000000000001"}
	source := &authoritySource{state: enabledAuthorityState(claim.AccountID)}
	authority, err := integrationexecution.NewCurrentAuthority(source, "cell-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Authorize(context.Background(), claim); err != nil || source.calls != 1 {
		t.Fatalf("authorization calls=%d err=%v", source.calls, err)
	}
}

func TestCurrentAuthorityFailsClosedOnPlacementAccountOrPackageDrift(t *testing.T) {
	claim := integrationexecution.Claim{AccountID: "b2100000-0000-4000-8000-000000000002"}
	for _, testCase := range []struct {
		name string
		edit func(*access.WorkloadState)
		code access.DenialCode
	}{
		{name: "wrong_cell", code: access.DenialCorruptContext, edit: func(state *access.WorkloadState) { state.Account.CellID = "cell-b" }},
		{name: "stale_entitlements", code: access.DenialCorruptContext, edit: func(state *access.WorkloadState) { state.Entitlements.Version-- }},
		{name: "closing_account", code: access.DenialAccountUnavailable, edit: func(state *access.WorkloadState) { state.Account.State = accounts.AccountClosing }},
		{name: "marketing_read_only", code: access.DenialPackageReadOnly, edit: func(state *access.WorkloadState) { state.Entitlements.Packages[0].Mode = catalog.ModeReadOnly }},
		{name: "integrations_suspended", code: access.DenialPackageNotEntitled, edit: func(state *access.WorkloadState) { state.Entitlements.Packages[1].Mode = catalog.ModeSuspended }},
		{name: "duplicate_package", code: access.DenialCorruptContext, edit: func(state *access.WorkloadState) {
			state.Entitlements.Packages = append(state.Entitlements.Packages, state.Entitlements.Packages[0])
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := enabledAuthorityState(claim.AccountID)
			testCase.edit(&state)
			authority, _ := integrationexecution.NewCurrentAuthority(&authoritySource{state: state}, "cell-a")
			if err := authority.Authorize(context.Background(), claim); !access.IsDenied(err, testCase.code) {
				t.Fatalf("authorization error=%v", err)
			}
		})
	}
}

func TestCurrentAuthorityPreservesSourceFailureAndRejectsInvalidComposition(t *testing.T) {
	claim := integrationexecution.Claim{AccountID: "b3100000-0000-4000-8000-000000000003"}
	sourceError := errors.New("global authority unavailable")
	authority, err := integrationexecution.NewCurrentAuthority(&authoritySource{err: sourceError}, "cell-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Authorize(context.Background(), claim); !errors.Is(err, sourceError) {
		t.Fatalf("source error=%v", err)
	}
	if _, err := integrationexecution.NewCurrentAuthority(nil, "cell-a"); !errors.Is(err, integrationexecution.ErrInvalid) {
		t.Fatalf("nil source=%v", err)
	}
	if _, err := integrationexecution.NewCurrentAuthority(&authoritySource{}, ""); !errors.Is(err, integrationexecution.ErrInvalid) {
		t.Fatalf("invalid cell=%v", err)
	}
}

func enabledAuthorityState(accountID ids.AccountID) access.WorkloadState {
	return access.WorkloadState{
		Account: accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: "cell-a", PlacementGeneration: 4, EntitlementVersion: 9},
		Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 9, Packages: []entitlements.PackageAccess{
			{Code: catalog.PackageMarketing, Version: 1, Mode: catalog.ModeEnabled},
			{Code: catalog.PackageIntegrations, Version: 1, Mode: catalog.ModeEnabled},
		}},
	}
}
