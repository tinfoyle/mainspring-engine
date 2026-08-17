package accountaccess_test

import (
	"context"
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

func (r repository) Choices(context.Context, ids.UserID) ([]accountaccess.Choice, error) {
	return r.choices, nil
}
func (r repository) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return r.state, nil
}

func TestListAndSelectKeepAccountIdentityExplicit(t *testing.T) {
	accountID := ids.AccountID("account-a")
	userID := ids.UserID("user-a")
	r := repository{choices: []accountaccess.Choice{{AccountID: accountID, DisplayName: "Northstar"}}, state: access.State{Account: accounts.Account{ID: accountID, State: accounts.AccountActive, CellID: ids.CellID("cell-a"), PlacementGeneration: 2}, Membership: accounts.Membership{AccountID: accountID, UserID: userID, State: accounts.MembershipActive, Role: accounts.RoleOwner}, Entitlements: entitlements.Snapshot{AccountID: accountID, Version: 3}}}
	authorizer, err := access.NewAuthorizer(r)
	if err != nil {
		t.Fatal(err)
	}
	service, err := accountaccess.NewService(r, authorizer)
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
