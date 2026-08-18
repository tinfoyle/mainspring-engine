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

type fixedWorkloadSource struct{ state WorkloadState }

func (s fixedWorkloadSource) WorkloadAccessState(context.Context, ids.AccountID) (WorkloadState, error) {
	return s.state, nil
}

type fixedOwnerSecurity struct {
	ready bool
	err   error
}

func (p fixedOwnerSecurity) Ready(context.Context, ids.UserID) (bool, error) { return p.ready, p.err }

func TestAuthorizeSeparatesMembershipRoleAndPackage(t *testing.T) {
	accountID := ids.AccountID("account-a")
	userID := ids.UserID("user-a")
	state := State{
		Account:      accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: ids.CellID("cell-a"), PlacementGeneration: 3, EntitlementVersion: 7},
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
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Package: catalog.PackageWork, Mutation: true}); !IsDenied(err, DenialPackageReadOnly) {
		t.Fatalf("expected mutation package denial, got %v", err)
	}
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Package: catalog.PackageAgents}); !IsDenied(err, DenialPackageNotEntitled) {
		t.Fatalf("expected missing package denial, got %v", err)
	}
	resolved, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{Package: catalog.PackageWork})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CellID != "cell-a" || resolved.PlacementGeneration != 3 || resolved.EntitlementVersion != 7 || resolved.PackageAccess == nil || resolved.PackageAccess.Code != catalog.PackageWork {
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

func TestMembershipAuthorizerDoesNotAcceptWorkloadIdentity(t *testing.T) {
	authorizer, _ := NewAuthorizer(fixedSource{})
	_, err := authorizer.Authorize(context.Background(), Actor{WorkloadID: "schedule-worker"}, ids.AccountID("account-a"), Requirement{})
	if !IsDenied(err, DenialUnauthenticated) {
		t.Fatalf("membership authorizer accepted workload identity: %v", err)
	}
}

func TestWorkloadAuthorizerUsesCurrentAccountAndPackageWithoutHumanRole(t *testing.T) {
	accountID := ids.AccountID("account-a")
	state := WorkloadState{
		Account:      accounts.Account{ID: accountID, DisplayName: "Ocean Ops", State: accounts.AccountActive, CellID: ids.CellID("cell-a"), PlacementGeneration: 3, EntitlementVersion: 7},
		Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 7, Packages: []entitlements.PackageAccess{{Code: catalog.PackageWork, Version: 1, Mode: catalog.ModeReadOnly}}},
	}
	authorizer, err := NewWorkloadAuthorizer(fixedWorkloadSource{state: state})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{WorkloadID: "runner-invocation:30000000-0000-4000-8000-000000000003"}
	resolved, err := authorizer.Authorize(context.Background(), actor, accountID, Requirement{Package: catalog.PackageWork})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Role != "" || resolved.PackageAccess == nil || resolved.PackageAccess.Mode != catalog.ModeReadOnly || resolved.EntitlementVersion != 7 {
		t.Fatalf("unexpected workload context: %#v", resolved)
	}
	if _, err := authorizer.Authorize(context.Background(), actor, accountID, Requirement{Package: catalog.PackageWork, Mutation: true}); !IsDenied(err, DenialPackageReadOnly) {
		t.Fatalf("expected read-only denial, got %v", err)
	}
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: "user-a"}, accountID, Requirement{}); !IsDenied(err, DenialUnauthenticated) {
		t.Fatalf("human actor crossed workload boundary: %v", err)
	}
	if _, err := authorizer.Authorize(context.Background(), actor, accountID, Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); !IsDenied(err, DenialUnauthenticated) {
		t.Fatalf("workload received human role authority: %v", err)
	}
}

func TestWorkloadAuthorizerRejectsStaleOrCrossAccountProjection(t *testing.T) {
	accountID := ids.AccountID("account-a")
	actor := Actor{WorkloadID: "runner-invocation:test"}
	for name, state := range map[string]WorkloadState{
		"cross_account": {Account: accounts.Account{ID: "account-b", State: accounts.AccountActive, EntitlementVersion: 1}, Entitlements: entitlements.Snapshot{AccountID: "account-b", Version: 1}},
		"stale":         {Account: accounts.Account{ID: accountID, State: accounts.AccountActive, EntitlementVersion: 2}, Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			authorizer, _ := NewWorkloadAuthorizer(fixedWorkloadSource{state: state})
			if _, err := authorizer.Authorize(context.Background(), actor, accountID, Requirement{}); !IsDenied(err, DenialCorruptContext) {
				t.Fatalf("expected corrupt projection denial, got %v", err)
			}
		})
	}
}

func TestOwnerCannotCrossAccountBoundaryUntilIdentityRecoveryIsReady(t *testing.T) {
	accountID, userID := ids.AccountID("account-a"), ids.UserID("user-a")
	state := State{
		Account:      accounts.Account{ID: accountID, State: accounts.AccountActive, EntitlementVersion: 1},
		Membership:   accounts.Membership{AccountID: accountID, UserID: userID, Role: accounts.RoleOwner, State: accounts.MembershipActive},
		Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 1},
	}
	authorizer, _ := NewAuthorizer(fixedSource{state: state}, WithOwnerSecurityPolicy(fixedOwnerSecurity{}))
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{}); !IsDenied(err, DenialOwnerEnrollment) {
		t.Fatalf("unenrolled owner error=%v", err)
	}
	authorizer, _ = NewAuthorizer(fixedSource{state: state}, WithOwnerSecurityPolicy(fixedOwnerSecurity{ready: true}))
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{}); err != nil {
		t.Fatalf("ready owner error=%v", err)
	}
	state.Membership.Role = accounts.RoleMember
	authorizer, _ = NewAuthorizer(fixedSource{state: state}, WithOwnerSecurityPolicy(fixedOwnerSecurity{}))
	if _, err := authorizer.Authorize(context.Background(), Actor{UserID: userID}, accountID, Requirement{}); err != nil {
		t.Fatalf("non-owner was incorrectly gated=%v", err)
	}
}
