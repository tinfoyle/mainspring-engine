package access

import (
	"context"
	"errors"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type DenialCode string

const (
	DenialUnauthenticated    DenialCode = "unauthenticated"
	DenialMembership         DenialCode = "membership_required"
	DenialAccountUnavailable DenialCode = "account_unavailable"
	DenialRole               DenialCode = "role_denied"
	DenialOwnerEnrollment    DenialCode = "owner_security_enrollment_required"
	DenialPackageNotEntitled DenialCode = "package_not_entitled"
	DenialPackageReadOnly    DenialCode = "package_read_only"
	DenialLimitNotDefined    DenialCode = "limit_not_defined"
	DenialLimitExceeded      DenialCode = "limit_exceeded"
	DenialCorruptContext     DenialCode = "corrupt_access_context"

	// DenialPackage remains an alias for callers compiled against the initial
	// policy vocabulary. New surfaces should expose the specific stable code.
	DenialPackage DenialCode = DenialPackageNotEntitled
)

type DeniedError struct {
	Code    DenialCode
	Package catalog.PackageCode
	Limit   catalog.LimitCode
	Current int64
	Maximum int64
}

func (e *DeniedError) Error() string { return string(e.Code) }

func IsDenied(err error, code DenialCode) bool {
	var denied *DeniedError
	return errors.As(err, &denied) && denied.Code == code
}

type Actor struct {
	UserID     ids.UserID
	WorkloadID string
}

func (a Actor) Valid() bool { return (a.UserID != "") != (a.WorkloadID != "") }

type AccountContext struct {
	AccountID           ids.AccountID               `json:"account_id"`
	AccountName         string                      `json:"account_name"`
	CellID              ids.CellID                  `json:"cell_id"`
	PlacementGeneration uint64                      `json:"placement_generation"`
	EntitlementVersion  uint64                      `json:"entitlement_version"`
	Role                accounts.MembershipRole     `json:"role"`
	AccountState        accounts.AccountState       `json:"account_state"`
	PackageAccess       *entitlements.PackageAccess `json:"package_access,omitempty"`
}

type State struct {
	Account      accounts.Account
	Membership   accounts.Membership
	Entitlements entitlements.Snapshot
}

// StateSource is owned by this policy consumer. Adapters must load all three
// values consistently enough to reject mismatched versions and account IDs.
type StateSource interface {
	AccessState(context.Context, ids.UserID, ids.AccountID) (State, error)
}

// WorkloadState deliberately excludes Membership. A verified workload acts for
// one already-admitted Account operation; it must never inherit a human role.
type WorkloadState struct {
	Account      accounts.Account
	Entitlements entitlements.Snapshot
}

type WorkloadStateSource interface {
	WorkloadAccessState(context.Context, ids.AccountID) (WorkloadState, error)
}

type OwnerSecurityPolicy interface {
	Ready(context.Context, ids.UserID) (bool, error)
}

type Requirement struct {
	Roles           []accounts.MembershipRole
	Package         catalog.PackageCode
	Mutation        bool
	AllowRestricted bool
}

type Authorizer struct {
	source        StateSource
	ownerSecurity OwnerSecurityPolicy
}

type WorkloadAuthorizer struct{ source WorkloadStateSource }

type Option func(*Authorizer)

func WithOwnerSecurityPolicy(policy OwnerSecurityPolicy) Option {
	return func(authorizer *Authorizer) { authorizer.ownerSecurity = policy }
}

func NewAuthorizer(source StateSource, options ...Option) (*Authorizer, error) {
	if source == nil {
		return nil, errors.New("access state source is required")
	}
	authorizer := &Authorizer{source: source}
	for _, option := range options {
		option(authorizer)
	}
	return authorizer, nil
}

func NewWorkloadAuthorizer(source WorkloadStateSource) (*WorkloadAuthorizer, error) {
	if source == nil {
		return nil, errors.New("workload access state source is required")
	}
	return &WorkloadAuthorizer{source: source}, nil
}

func (a *Authorizer) Authorize(ctx context.Context, actor Actor, accountID ids.AccountID, requirement Requirement) (AccountContext, error) {
	if !actor.Valid() || actor.WorkloadID != "" {
		return AccountContext{}, &DeniedError{Code: DenialUnauthenticated}
	}
	state, err := a.source.AccessState(ctx, actor.UserID, accountID)
	if err != nil {
		return AccountContext{}, err
	}
	if state.Account.ID != accountID || state.Membership.AccountID != accountID || state.Entitlements.AccountID != accountID || state.Membership.UserID != actor.UserID || state.Account.EntitlementVersion != state.Entitlements.Version {
		return AccountContext{}, &DeniedError{Code: DenialCorruptContext}
	}
	if state.Account.State != accounts.AccountActive && !(requirement.AllowRestricted && state.Account.State == accounts.AccountRestricted) {
		return AccountContext{}, &DeniedError{Code: DenialAccountUnavailable}
	}
	if state.Membership.State != accounts.MembershipActive {
		return AccountContext{}, &DeniedError{Code: DenialMembership}
	}
	if state.Membership.Role == accounts.RoleOwner && a.ownerSecurity != nil {
		ready, err := a.ownerSecurity.Ready(ctx, actor.UserID)
		if err != nil {
			return AccountContext{}, err
		}
		if !ready {
			return AccountContext{}, &DeniedError{Code: DenialOwnerEnrollment}
		}
	}
	if len(requirement.Roles) > 0 && !containsRole(requirement.Roles, state.Membership.Role) {
		return AccountContext{}, &DeniedError{Code: DenialRole}
	}
	var packageAccess *entitlements.PackageAccess
	if requirement.Package != "" {
		effective, exists := state.Entitlements.Package(requirement.Package)
		if !exists || effective.Mode == catalog.ModeSuspended {
			return AccountContext{}, &DeniedError{Code: DenialPackageNotEntitled, Package: requirement.Package}
		}
		if requirement.Mutation && effective.Mode != catalog.ModeEnabled {
			return AccountContext{}, &DeniedError{Code: DenialPackageReadOnly, Package: requirement.Package}
		}
		packageAccess = &effective
	}
	return AccountContext{AccountID: accountID, AccountName: state.Account.DisplayName, CellID: state.Account.CellID, PlacementGeneration: state.Account.PlacementGeneration, EntitlementVersion: state.Entitlements.Version, Role: state.Membership.Role, AccountState: state.Account.State, PackageAccess: packageAccess}, nil
}

// Authorize accepts only a named workload and returns no Membership role. The
// caller remains responsible for authenticating and binding that workload to
// the exact Account operation before invoking this policy.
func (a *WorkloadAuthorizer) Authorize(ctx context.Context, actor Actor, accountID ids.AccountID, requirement Requirement) (AccountContext, error) {
	if !actor.Valid() || actor.WorkloadID == "" || len(requirement.Roles) != 0 {
		return AccountContext{}, &DeniedError{Code: DenialUnauthenticated}
	}
	state, err := a.source.WorkloadAccessState(ctx, accountID)
	if err != nil {
		return AccountContext{}, err
	}
	if state.Account.ID != accountID || state.Entitlements.AccountID != accountID || state.Account.EntitlementVersion != state.Entitlements.Version {
		return AccountContext{}, &DeniedError{Code: DenialCorruptContext}
	}
	if state.Account.State != accounts.AccountActive {
		return AccountContext{}, &DeniedError{Code: DenialAccountUnavailable}
	}
	var packageAccess *entitlements.PackageAccess
	if requirement.Package != "" {
		effective, exists := state.Entitlements.Package(requirement.Package)
		if !exists || effective.Mode == catalog.ModeSuspended {
			return AccountContext{}, &DeniedError{Code: DenialPackageNotEntitled, Package: requirement.Package}
		}
		if requirement.Mutation && effective.Mode != catalog.ModeEnabled {
			return AccountContext{}, &DeniedError{Code: DenialPackageReadOnly, Package: requirement.Package}
		}
		packageAccess = &effective
	}
	return AccountContext{AccountID: accountID, AccountName: state.Account.DisplayName, CellID: state.Account.CellID, PlacementGeneration: state.Account.PlacementGeneration, EntitlementVersion: state.Entitlements.Version, AccountState: state.Account.State, PackageAccess: packageAccess}, nil
}

func containsRole(roles []accounts.MembershipRole, role accounts.MembershipRole) bool {
	for _, allowed := range roles {
		if allowed == role {
			return true
		}
	}
	return false
}
