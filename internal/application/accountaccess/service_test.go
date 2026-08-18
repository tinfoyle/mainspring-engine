package accountaccess_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type repository struct {
	choices []accountaccess.Choice
	state   access.State
}

type ownerSecurity struct {
	ready bool
	err   error
}

func (p ownerSecurity) Ready(context.Context, ids.UserID) (bool, error) { return p.ready, p.err }

func (r repository) Choices(context.Context, ids.UserID) ([]accountaccess.Choice, error) {
	return r.choices, nil
}
func (r repository) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return r.state, nil
}

func TestListAndSelectKeepAccountIdentityExplicit(t *testing.T) {
	accountID := ids.AccountID("account-a")
	userID := ids.UserID("user-a")
	r := repository{choices: []accountaccess.Choice{{AccountID: accountID, DisplayName: "Northstar"}}, state: access.State{Account: accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: ids.CellID("cell-a"), PlacementGeneration: 2, EntitlementVersion: 3}, Membership: accounts.Membership{AccountID: accountID, UserID: userID, State: accounts.MembershipActive, Role: accounts.RoleOwner}, Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 3}}}
	authorizer, err := access.NewAuthorizer(r)
	if err != nil {
		t.Fatal(err)
	}
	service, err := accountaccess.NewService(r, authorizer, ownerSecurity{ready: true})
	if err != nil {
		t.Fatal(err)
	}
	choices, err := service.List(context.Background(), userID)
	if err != nil || len(choices) != 1 {
		t.Fatalf("list: %#v %v", choices, err)
	}
	selected, err := service.Select(context.Background(), userID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.AccountID != accountID || selected.CellID != "cell-a" || selected.PlacementGeneration != 2 {
		t.Fatalf("unexpected context: %#v", selected)
	}
}

func TestListMarksUnenrolledOwnerWithoutHidingOtherMemberships(t *testing.T) {
	userID := ids.UserID("user-a")
	repository := repository{choices: []accountaccess.Choice{
		{AccountID: "owner-account", Role: accounts.RoleOwner},
		{AccountID: "member-account", Role: accounts.RoleMember},
	}}
	authorizer, _ := access.NewAuthorizer(repository)
	service, _ := accountaccess.NewService(repository, authorizer, ownerSecurity{})
	choices, err := service.List(context.Background(), userID)
	if err != nil || len(choices) != 2 || !choices[0].OwnerEnrollmentRequired || choices[1].OwnerEnrollmentRequired {
		t.Fatalf("choices=%+v err=%v", choices, err)
	}
}

func TestListDoesNotDependOnOwnerPostureForNonOwnerMemberships(t *testing.T) {
	userID := ids.UserID("user-a")
	repository := repository{choices: []accountaccess.Choice{{AccountID: "member-account", Role: accounts.RoleMember}}}
	authorizer, _ := access.NewAuthorizer(repository)
	service, _ := accountaccess.NewService(repository, authorizer, ownerSecurity{err: errors.New("posture unavailable")})
	choices, err := service.List(context.Background(), userID)
	if err != nil || len(choices) != 1 || choices[0].OwnerEnrollmentRequired {
		t.Fatalf("choices=%+v err=%v", choices, err)
	}
}
