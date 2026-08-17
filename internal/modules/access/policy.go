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
	DenialPackage            DenialCode = "package_denied"
	DenialCorruptContext     DenialCode = "corrupt_access_context"
)

type DeniedError struct{ Code DenialCode }

func (e *DeniedError) Error() string { return string(e.Code) }

func IsDenied(err error, code DenialCode) bool {
	var denied *DeniedError
	return errors.As(err, &denied) && denied.Code == code
}

type Actor struct {
	UserID ids.UserID
}

type AccountContext struct {
	AccountID           ids.AccountID
	CellID              ids.CellID
	PlacementGeneration uint64
	EntitlementVersion  uint64
	Role                accounts.MembershipRole
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

type Requirement struct {
	Roles    []accounts.MembershipRole
	Package  catalog.PackageCode
	Mutation bool
}

type Authorizer struct{ source StateSource }

func NewAuthorizer(source StateSource) (*Authorizer, error) {
	if source == nil {
		return nil, errors.New("access state source is required")
	}
	return &Authorizer{source: source}, nil
}

func (a *Authorizer) Authorize(ctx context.Context, actor Actor, accountID ids.AccountID, requirement Requirement) (AccountContext, error) {
	if actor.UserID == "" {
		return AccountContext{}, &DeniedError{Code: DenialUnauthenticated}
	}
	state, err := a.source.AccessState(ctx, actor.UserID, accountID)
	if err != nil {
		return AccountContext{}, err
	}
	if state.Account.ID != accountID || state.Membership.AccountID != accountID || state.Entitlements.AccountID != accountID || state.Membership.UserID != actor.UserID {
		return AccountContext{}, &DeniedError{Code: DenialCorruptContext}
	}
	if state.Account.State != accounts.AccountActive {
		return AccountContext{}, &DeniedError{Code: DenialAccountUnavailable}
	}
	if state.Membership.State != accounts.MembershipActive {
		return AccountContext{}, &DeniedError{Code: DenialMembership}
	}
	if len(requirement.Roles) > 0 && !containsRole(requirement.Roles, state.Membership.Role) {
		return AccountContext{}, &DeniedError{Code: DenialRole}
	}
	if requirement.Package != "" && !state.Entitlements.Allows(requirement.Package, requirement.Mutation) {
		return AccountContext{}, &DeniedError{Code: DenialPackage}
	}
	return AccountContext{AccountID: accountID, CellID: state.Account.CellID, PlacementGeneration: state.Account.PlacementGeneration, EntitlementVersion: state.Entitlements.Version, Role: state.Membership.Role}, nil
}

func containsRole(roles []accounts.MembershipRole, role accounts.MembershipRole) bool {
	for _, allowed := range roles {
		if allowed == role {
			return true
		}
	}
	return false
}
