package boardroom

import (
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestDelegationRequestsAcceptsFocusedAgentDelegation(t *testing.T) {
	requests, err := delegationRequests([]agent.Delegation{{
		Agent: "Sales Agent", Request: "Retrieve this week's sales data.",
	}})
	if err != nil {
		t.Fatalf("delegationRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Agent != "Sales Agent" || requests[0].Request != "Retrieve this week's sales data." {
		t.Fatalf("unexpected requests: %#v", requests)
	}
}

func TestFindDelegatedPersonaAcceptsDisplayedRosterLabel(t *testing.T) {
	jordan := Persona{ID: domain.NewPersonaID(), Name: "Jordan", Role: "Security & Compliance Advisor"}
	persona, found := findDelegatedPersona([]Persona{jordan}, "Jordan — Security & Compliance Advisor")
	if !found || persona.ID != jordan.ID {
		t.Fatalf("displayed roster label did not resolve: found=%v persona=%#v", found, persona)
	}
}

func TestDelegationRequestsRejectsIncompleteDelegation(t *testing.T) {
	_, err := delegationRequests([]agent.Delegation{{Agent: "Sales Agent"}})
	if err == nil {
		t.Fatal("delegationRequests accepted an incomplete delegation")
	}
}

func TestVisibleDelegationSummaryIncludesTheSpecialistRequest(t *testing.T) {
	summary, err := visibleDelegationSummary([]agent.Delegation{{
		Agent: "Sales Agent", Request: "Retrieve this week's sales data.",
	}})
	if err != nil {
		t.Fatalf("visibleDelegationSummary: %v", err)
	}
	if summary != "Delegating to Sales Agent: Retrieve this week's sales data." {
		t.Fatalf("summary = %q", summary)
	}
}
