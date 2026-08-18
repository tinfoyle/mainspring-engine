package work

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	itemID    = "10000000-0000-4000-8000-000000000001"
	accountID = "20000000-0000-4000-8000-000000000002"
	userID    = "30000000-0000-4000-8000-000000000003"
)

func validItem(t *testing.T, state State) Item {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	draft, err := NewDraft(Draft{ID: ids.WorkItemID(itemID), AccountID: ids.AccountID(accountID), Kind: KindTicket, Title: "Confirm quarter close", Priority: PriorityHigh, Assignment: Assignment{Responsibility: ResponsibilityUser, UserID: ids.UserID(userID)}, Provenance: Provenance{Source: SourceManual, CreatedBy: Actor{Kind: ActorUser, ID: userID}}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := Materialize(draft, 42, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	item.State = state
	if state == StateDone {
		item.CompletedAt = &now
	}
	return item
}

func TestTransitionMatrixAndRoles(t *testing.T) {
	states := []State{StateOpen, StateInProgress, StateWaiting, StateDone, StateCanceled}
	allowed := map[[2]State]bool{
		{StateOpen, StateInProgress}: true, {StateOpen, StateCanceled}: true,
		{StateInProgress, StateWaiting}: true, {StateInProgress, StateDone}: true, {StateInProgress, StateCanceled}: true,
		{StateWaiting, StateInProgress}: true, {StateWaiting, StateCanceled}: true,
		{StateDone, StateOpen}: true,
	}
	roles := []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember, accounts.RoleViewer, accounts.RoleBillingAdmin}
	for _, from := range states {
		for _, to := range states {
			for _, role := range roles {
				name := string(from) + "_to_" + string(to) + "_as_" + string(role)
				t.Run(name, func(t *testing.T) {
					item := validItem(t, from)
					reason := "state changed for a recorded operational reason"
					_, err := item.Transition(TransitionCommand{To: to, Role: role, Actor: Actor{Kind: ActorUser, ID: userID}, Reason: reason, ExpectedVersion: item.Version, At: item.UpdatedAt.Add(time.Minute)})
					want := allowed[[2]State{from, to}] && (role == accounts.RoleOwner || role == accounts.RoleAdministrator || (role == accounts.RoleMember && to != StateCanceled && from != StateDone))
					if (err == nil) != want {
						t.Fatalf("transition error = %v, want allowed=%v", err, want)
					}
				})
			}
		}
	}
}

func TestTransitionsRequireReasonAndVersion(t *testing.T) {
	item := validItem(t, StateInProgress)
	_, err := item.Transition(TransitionCommand{To: StateWaiting, Role: accounts.RoleOwner, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: item.Version, At: item.UpdatedAt.Add(time.Minute)})
	if !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("waiting error = %v", err)
	}
	_, err = item.Transition(TransitionCommand{To: StateDone, Role: accounts.RoleOwner, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: item.Version + 1, At: item.UpdatedAt.Add(time.Minute)})
	if !errors.Is(err, ErrTransition) {
		t.Fatalf("stale version error = %v", err)
	}
}

func TestInvalidDraftsCannotBeConstructed(t *testing.T) {
	base := Draft{ID: ids.WorkItemID(itemID), AccountID: ids.AccountID(accountID), Kind: KindTodo, Title: "A valid task", Priority: PriorityNormal, Assignment: Assignment{Responsibility: ResponsibilityShared}, Provenance: Provenance{Source: SourceManual, CreatedBy: Actor{Kind: ActorUser, ID: userID}}}
	cases := map[string]func(*Draft){
		"unknown kind":                   func(d *Draft) { d.Kind = "memo" },
		"self parent":                    func(d *Draft) { d.ParentID = d.ID },
		"persona without persona":        func(d *Draft) { d.Assignment = Assignment{Responsibility: ResponsibilityPersona} },
		"malformed provenance reference": func(d *Draft) { d.Provenance.BaselineRequirementID = "not-an-id" },
		"baseline without requirement":   func(d *Draft) { d.Provenance.Source = SourceBaseline },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			if _, err := NewDraft(value); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
