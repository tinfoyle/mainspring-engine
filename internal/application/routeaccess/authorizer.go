// Package routeaccess translates verified cell route claims into the same
// authorization contract used by feature application services.
package routeaccess

import (
	"context"
	"errors"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type Authorizer struct{}

func NewAuthorizer() *Authorizer { return &Authorizer{} }

func (a *Authorizer) Authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	claims, ok := routecontext.FromContext(ctx)
	if !ok || !actor.Valid() {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	authority := claims.Authority
	if authority.AccountID != accountID {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialCorruptContext}
	}
	if !actorMatches(actor, authority) && !delegatedActorMatches(actor, authority, requirement) {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	role := accounts.MembershipRole(authority.Role)
	if len(requirement.Roles) > 0 && !containsRole(requirement.Roles, role) {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialRole}
	}
	var packageAccess *entitlements.PackageAccess
	if requirement.Package != "" {
		claimed := authority.PackageAccess
		if claimed == nil || catalog.PackageCode(claimed.Code) != requirement.Package {
			claimed = nil
			for index := range authority.PackageAccesses {
				if catalog.PackageCode(authority.PackageAccesses[index].Code) == requirement.Package {
					claimed = &authority.PackageAccesses[index]
					break
				}
			}
		}
		if claimed == nil {
			return access.AccountContext{}, &access.DeniedError{Code: access.DenialPackageNotEntitled, Package: requirement.Package}
		}
		converted, err := convertPackage(*claimed)
		if err != nil {
			return access.AccountContext{}, &access.DeniedError{Code: access.DenialCorruptContext, Package: requirement.Package}
		}
		if converted.Mode == catalog.ModeSuspended {
			return access.AccountContext{}, &access.DeniedError{Code: access.DenialPackageNotEntitled, Package: requirement.Package}
		}
		if requirement.Mutation && converted.Mode != catalog.ModeEnabled {
			return access.AccountContext{}, &access.DeniedError{Code: access.DenialPackageReadOnly, Package: requirement.Package}
		}
		packageAccess = &converted
	}
	return access.AccountContext{AccountID: accountID, CellID: authority.CellID, PlacementGeneration: authority.PlacementGeneration, EntitlementVersion: authority.EntitlementVersion, Role: role, PackageAccess: packageAccess}, nil
}

func delegatedActorMatches(actor access.Actor, authority routecontext.Authority, requirement access.Requirement) bool {
	if actor.UserID != "" || actor.WorkloadID == "" || requirement.Package != catalog.PackageKnowledge || !requirement.Mutation {
		return false
	}
	for _, workloadID := range authority.DelegatedWorkloadIDs {
		if actor.WorkloadID == workloadID {
			return true
		}
	}
	return false
}

func actorMatches(actor access.Actor, authority routecontext.Authority) bool {
	if authority.ActorKind == "user" {
		return actor.UserID != "" && string(actor.UserID) == authority.ActorID && actor.WorkloadID == ""
	}
	return authority.ActorKind == "workload" && actor.WorkloadID != "" && actor.WorkloadID == authority.ActorID && actor.UserID == ""
}

func convertPackage(value routecontext.PackageAccess) (entitlements.PackageAccess, error) {
	limits := make(map[catalog.LimitCode]int64, len(value.Limits))
	for code, limit := range value.Limits {
		limits[catalog.LimitCode(code)] = limit
	}
	policies := make(map[catalog.LimitCode]entitlements.LimitPolicy, len(value.LimitPolicies))
	for code, policy := range value.LimitPolicies {
		policies[catalog.LimitCode(code)] = entitlements.LimitPolicy{Kind: catalog.LimitKind(policy.Kind), Combine: catalog.LimitCombineRule(policy.Combine), ReservationTTLSeconds: policy.ReservationTTLSeconds}
	}
	result := entitlements.PackageAccess{Code: catalog.PackageCode(value.Code), Version: value.Version, Mode: catalog.PackageMode(value.Mode), Limits: limits, LimitPolicies: policies}
	if result.Code == "" || result.Version == 0 || (result.Mode != catalog.ModeEnabled && result.Mode != catalog.ModeReadOnly && result.Mode != catalog.ModeSuspended) {
		return entitlements.PackageAccess{}, errors.New("invalid routed package access")
	}
	return result, nil
}

func containsRole(roles []accounts.MembershipRole, role accounts.MembershipRole) bool {
	for _, allowed := range roles {
		if role == allowed {
			return true
		}
	}
	return false
}

var _ interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
} = (*Authorizer)(nil)
