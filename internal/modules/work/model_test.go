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

func TestProvenanceLinksAreAdditiveVersionedAndCreationImmutable(t *testing.T) {
	item := validItem(t, StateDone)
	now := item.UpdatedAt.Add(time.Minute)
	links := []struct {
		kind ProvenanceLinkKind
		id   string
		read func(Provenance) string
	}{
		{ProvenanceBaselineRequirement, "40000000-0000-4000-8000-000000000004", func(value Provenance) string { return value.BaselineRequirementID }},
		{ProvenanceSchedule, "50000000-0000-4000-8000-000000000005", func(value Provenance) string { return value.ScheduleID }},
		{ProvenanceRun, "60000000-0000-4000-8000-000000000006", func(value Provenance) string { return value.RunID }},
	}
	for index, link := range links {
		updated, err := item.AttachProvenance(ProvenanceLinkCommand{Kind: link.kind, ReferenceID: link.id, Role: accounts.RoleMember, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: item.Version, At: now.Add(time.Duration(index) * time.Minute)})
		if err != nil || link.read(updated.Provenance) != link.id || updated.Version != item.Version+1 || updated.Provenance.Source != SourceManual || updated.Provenance.CreatedBy != item.Provenance.CreatedBy {
			t.Fatalf("attach %s = %+v, %v", link.kind, updated, err)
		}
		item = updated
	}
	conversationID := "70000000-0000-4000-8000-000000000007"
	linked, err := item.LinkConversation(ConversationLinkCommand{ConversationID: conversationID, Role: accounts.RoleAdministrator, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: item.Version, At: now.Add(4 * time.Minute)})
	if err != nil || linked.Provenance.ConversationID != conversationID || linked.Provenance.Source != SourceManual || linked.Provenance.CreatedBy != item.Provenance.CreatedBy {
		t.Fatalf("link Conversation = %+v, %v", linked, err)
	}
	if _, err := linked.LinkConversation(ConversationLinkCommand{ConversationID: "80000000-0000-4000-8000-000000000008", Role: accounts.RoleOwner, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: linked.Version, At: now.Add(5 * time.Minute)}); !errors.Is(err, ErrLinkExists) {
		t.Fatalf("replace Conversation error = %v", err)
	}
	if _, err := linked.AttachProvenance(ProvenanceLinkCommand{Kind: ProvenanceRun, ReferenceID: "80000000-0000-4000-8000-000000000008", Role: accounts.RoleOwner, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: linked.Version, At: now.Add(5 * time.Minute)}); !errors.Is(err, ErrLinkExists) {
		t.Fatalf("replace Run error = %v", err)
	}
}

func TestProvenanceLinksRejectInvalidAuthorityAndReferences(t *testing.T) {
	item := validItem(t, StateOpen)
	now := item.UpdatedAt.Add(time.Minute)
	base := ProvenanceLinkCommand{Kind: ProvenanceRun, ReferenceID: "60000000-0000-4000-8000-000000000006", Role: accounts.RoleMember, Actor: Actor{Kind: ActorUser, ID: userID}, ExpectedVersion: item.Version, At: now}
	for name, mutate := range map[string]func(*ProvenanceLinkCommand){
		"unknown kind":  func(value *ProvenanceLinkCommand) { value.Kind = "conversation" },
		"malformed id":  func(value *ProvenanceLinkCommand) { value.ReferenceID = "not-an-id" },
		"stale version": func(value *ProvenanceLinkCommand) { value.ExpectedVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			command := base
			mutate(&command)
			if _, err := item.AttachProvenance(command); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	base.Role = accounts.RoleViewer
	if _, err := item.AttachProvenance(base); !errors.Is(err, ErrRole) {
		t.Fatalf("viewer error = %v", err)
	}
}
