package routeaccess

import (
	"context"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	accountID = "10000000-0000-4000-8000-000000000001"
	userID    = "20000000-0000-4000-8000-000000000002"
)

func TestRoutedAuthorityEnforcesActorAccountRoleAndPackage(t *testing.T) {
	authorizer := NewAuthorizer()
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(accountID), ActorKind: "user", ActorID: userID, Role: "member", CellID: "cell-us-east-01", PlacementGeneration: 3, EntitlementVersion: 7, PackageAccess: &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "enabled", Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]routecontext.LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}}}
	ctx := routecontext.WithClaims(context.Background(), claims)
	result, err := authorizer.Authorize(ctx, access.Actor{UserID: ids.UserID(userID)}, ids.AccountID(accountID), access.Requirement{Package: catalog.PackageWork, Roles: []accounts.MembershipRole{accounts.RoleMember}})
	if err != nil || result.PackageAccess == nil || result.PackageAccess.Limits["active_items"] != 100 || result.PlacementGeneration != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := authorizer.Authorize(ctx, access.Actor{UserID: "30000000-0000-4000-8000-000000000003"}, ids.AccountID(accountID), access.Requirement{}); !access.IsDenied(err, access.DenialUnauthenticated) {
		t.Fatalf("actor error=%v", err)
	}
	if _, err := authorizer.Authorize(ctx, access.Actor{UserID: ids.UserID(userID)}, "30000000-0000-4000-8000-000000000003", access.Requirement{}); !access.IsDenied(err, access.DenialCorruptContext) {
		t.Fatalf("account error=%v", err)
	}
	if _, err := authorizer.Authorize(ctx, access.Actor{UserID: ids.UserID(userID)}, ids.AccountID(accountID), access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("role error=%v", err)
	}
	if _, err := authorizer.Authorize(ctx, access.Actor{UserID: ids.UserID(userID)}, ids.AccountID(accountID), access.Requirement{Package: catalog.PackageFinance}); !access.IsDenied(err, access.DenialPackageNotEntitled) {
		t.Fatalf("package error=%v", err)
	}
}

func TestRoutedReadOnlyPackageRejectsMutation(t *testing.T) {
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(accountID), ActorKind: "user", ActorID: userID, Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "read_only"}}}
	_, err := NewAuthorizer().Authorize(routecontext.WithClaims(context.Background(), claims), access.Actor{UserID: ids.UserID(userID)}, ids.AccountID(accountID), access.Requirement{Package: catalog.PackageWork, Mutation: true})
	if !access.IsDenied(err, access.DenialPackageReadOnly) {
		t.Fatalf("error=%v", err)
	}
}
