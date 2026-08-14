package boardroom

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
)

func TestUnknownAnswerTicketWithoutDocumentSearchIsSuppressed(t *testing.T) {
	payload, err := json.Marshal(map[string]string{
		"title": "Research licensing", "description": "Work plan:\n1. Research.\n\nDefinition of done: Recorded.",
		"priority": "normal", "origin": "unknown_answer", "search_query": "licensing",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := agent.Result{Structured: agent.ResultEnvelope{ProposedActions: []agent.ProposedAction{
		{ActionType: "tickets.create", Reason: "Track missing evidence", Payload: payload},
	}}}
	result = removeUnsearchedUnknownAnswerActions(result, false)
	if len(result.Structured.ProposedActions) != 0 {
		t.Fatalf("unsupported unknown-answer action was retained: %#v", result.Structured.ProposedActions)
	}
	if !strings.Contains(result.Structured.Contribution, "No work item was proposed") {
		t.Fatalf("suppressed action was not explained: %q", result.Structured.Contribution)
	}
}

func TestApplyUnknownAnswerEscalationDelegatesToComplianceSpecialistBeforeOfferingWork(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{
		Contribution:    "I recommend delegating.",
		Delegations:     []agent.Delegation{{Agent: "invented specialist", Request: "Assess compliance."}},
		ProposedActions: []agent.ProposedAction{agent.LegalComplianceWorkItemAction("legal compliance")},
		Confidence:      "low",
	}}
	result = applyUnknownAnswerEscalation(result, "Are we in legal compliance?", invocationToolTrace{DocumentSearches: 1, LastSearchQuery: "legal compliance"}, "Jordan", true, "propose_only")
	if len(result.Structured.Delegations) != 1 || result.Structured.Delegations[0].Agent != "Jordan" {
		t.Fatalf("expected exact Jordan delegation: %#v", result.Structured.Delegations)
	}
	if len(result.Structured.ProposedActions) != 0 {
		t.Fatalf("initial manager turn offered work before Jordan contributed: %#v", result.Structured.ProposedActions)
	}
	if !strings.Contains(result.Body, "asking Jordan") || !strings.Contains(result.Body, "searched the authorized document library") {
		t.Fatalf("handoff was not visible: %q", result.Body)
	}
}

func TestApplyUnknownAnswerEscalationFallsBackToTicketWhenNoComplianceSpecialistExists(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{Contribution: "No answer.", Confidence: "low"}}
	result = applyUnknownAnswerEscalation(result, "Are we in legal compliance?", invocationToolTrace{DocumentSearches: 1, LastSearchQuery: "legal compliance"}, "", true, "propose_only")
	if len(result.Structured.Delegations) != 0 || len(result.Structured.ProposedActions) != 1 {
		t.Fatalf("fallback escalation = %#v", result.Structured)
	}
	if result.Structured.ProposedActions[0].ActionType != "tickets.create" || !strings.Contains(result.Body, "approve the proposed next step") {
		t.Fatalf("unexpected fallback result: %#v", result)
	}
}

func TestApplyUnknownAnswerEscalationRequiresCompletedSearch(t *testing.T) {
	original := agent.Result{Structured: agent.ResultEnvelope{Contribution: "No answer.", Confidence: "low"}}
	result := applyUnknownAnswerEscalation(original, "Are we in legal compliance?", invocationToolTrace{}, "Jordan", true, "propose_only")
	if len(result.Structured.Delegations) != 0 || len(result.Structured.ProposedActions) != 0 {
		t.Fatalf("escalation occurred without a document search: %#v", result.Structured)
	}
}

func TestFinalUnknownAnswerEscalationOffersWorkAfterSpecialist(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{
		Contribution: "Jordan found an evidence gap.",
		Confidence:   "low",
	}}
	result = applyFinalUnknownAnswerEscalation(result, "Are we in legal compliance?", "legal compliance obligations", "Jordan", true, "propose_only")
	if len(result.Structured.ProposedActions) != 1 || result.Structured.ProposedActions[0].ActionType != "tickets.create" {
		t.Fatalf("final synthesis did not offer work: %#v", result.Structured)
	}
	if !strings.Contains(result.Body, "Jordan's assessment") || !strings.Contains(result.Body, "requires owner approval") {
		t.Fatalf("final synthesis did not preserve specialist and approval context: %q", result.Body)
	}
}

func TestDemoEscalationOverridesLegacyDisabledPolicyAndBadConfidence(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{Contribution: "No evidence, but confidence was misclassified.", Confidence: "high"}}
	result = applyUnknownAnswerEscalation(result, "[demo:unknown-answer:legal-compliance] Are we in legal compliance?", invocationToolTrace{DocumentSearches: 1}, "Jordan", true, "disabled")
	if len(result.Structured.Delegations) != 1 || result.Structured.Delegations[0].Agent != "Jordan" {
		t.Fatalf("demo fallback did not recover legacy configuration: %#v", result.Structured)
	}
}

func TestMissingCredentialEscalationOffersAcquisitionWork(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{
		Contribution: "I searched, but the license document is unavailable.",
		Citations:    []agent.Citation{{ID: "web:official", Label: "Official licensing page"}},
		Confidence:   "medium",
	}}
	trace := invocationToolTrace{DocumentSearches: 1, LastSearchQuery: "business license"}
	result = applyMissingCredentialEscalation(result, "What license do we need?", trace, true, "propose_only")

	if len(result.Structured.ProposedActions) != 1 || result.Structured.ProposedActions[0].ActionType != "tickets.create" {
		t.Fatalf("missing credential did not produce an acquisition work item: %#v", result.Structured)
	}
	for _, expected := range []string{"missing-credential workflow", "already have the credential", "application/renewal record", "until you approve it"} {
		if !strings.Contains(result.Body, expected) {
			t.Fatalf("credential response missing %q: %s", expected, result.Body)
		}
	}
	if len(result.Structured.Questions) != 1 || !strings.Contains(result.Structured.Questions[0], "already have") {
		t.Fatalf("owner confirmation question missing: %#v", result.Structured.Questions)
	}
	if len(result.Structured.Delegations) != 0 {
		t.Fatalf("acquisition fallback retained a handoff instead of making progress: %#v", result.Structured.Delegations)
	}
}

func TestMissingCredentialEscalationStopsWhenCredentialWasFound(t *testing.T) {
	original := agent.Result{Structured: agent.ResultEnvelope{
		Contribution: "The attached license was found.", Confidence: "high",
		Citations: []agent.Citation{{ID: "doc:license:chunk:0", DocumentID: "license"}},
	}}
	result := applyMissingCredentialEscalation(original, "What license do we need?", invocationToolTrace{DocumentSearches: 1}, true, "propose_only")
	if len(result.Structured.ProposedActions) != 0 || result.Body != "" || result.Structured.Contribution != original.Structured.Contribution {
		t.Fatalf("existing credential incorrectly triggered acquisition: %#v", result)
	}
}

func TestMissingCredentialEscalationRespectsDisabledActions(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{Contribution: "No document.", Confidence: "low"}}
	result = applyMissingCredentialEscalation(result, "Which permit is required?", invocationToolTrace{DocumentSearches: 1}, true, "disabled")
	if len(result.Structured.ProposedActions) != 0 || !strings.Contains(result.Body, "Do you already have") {
		t.Fatalf("disabled action policy should preserve guidance without proposing work: %#v", result)
	}
}
