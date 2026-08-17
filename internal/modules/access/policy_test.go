package access

import (
	"context"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fixedSource struct{ state State }

func (s fixedSource) AccessState(context.Context, ids.UserID, ids.AccountID) (State, error) {
	return s.state, nil
}

func TestAuthorizeSeparatesMembershipRoleAndPackage(t *testing.T) {
	accountID := ids.AccountID("account-a")
	userID := ids.UserID("user-a")
	state := State{
		Account:      accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: ids.CellID("cell-a"), PlacementGeneration: 3},
		Membership:   accounts.Membership{AccountID: accountID, UserID: userID, Role: accounts.RoleMember, State: accounts.MembershipActive},
		Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 7, Packages: []entitlements.PackageAccess{{Code: catalog.PackageWork, Mode: catalog.ModeReadOnly}}},
	}
	authorizer, err := NewAuthorizer(fixedSource{state: state})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); !IsDenied(err, DenialRole) {
		t.Fatalf("expected role denial, got %v", err)
	}
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Package: catalog.PackageWork, Mutation: true}); !IsDenied(err, DenialPackage) {
		t.Fatalf("expected mutation package denial, got %v", err)
	}
	resolved, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Package: catalog.PackageWork})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CellID != "cell-a" || resolved.PlacementGeneration != 3 || resolved.EntitlementVersion != 7 {
		t.Fatalf("unexpected account context: %#v", resolved)
	}
}

func TestAuthorizeRejectsCrossAccountState(t *testing.T) {
	authorizer, err := NewAuthorizer(fixedSource{state: State{
		Account:      accounts.Account{ID: ids.AccountID("account-b"), State: accounts.AccountActive},
		Membership:   accounts.Membership{AccountID: ids.AccountID("account-b"), UserID: ids.UserID("user-a"), State: accounts.MembershipActive},
		Entitlements: entitlements.Snapshot{AccountID: ids.AccountID("account-b")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = authorizer.Authorize(context.Background(), Actor{UserID: ids.UserID("user-a")}, ids.AccountID("account-a"), Requirement{})
	if !IsDenied(err, DenialCorruptContext) {
		t.Fatalf("expected corrupt context denial, got %v", err)
	}
}
