package agents

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	accountID      = ids.AccountID("10000000-0000-4000-8000-000000000001")
	userID         = ids.UserID("20000000-0000-4000-8000-000000000002")
	boardroomID    = ids.BoardroomID("30000000-0000-4000-8000-000000000003")
	conversationID = ids.ConversationID("40000000-0000-4000-8000-000000000004")
	runID          = ids.RunID("50000000-0000-4000-8000-000000000005")
	personaID      = ids.PersonaID("60000000-0000-4000-8000-000000000006")
	versionID      = ids.PersonaVersionID("70000000-0000-4000-8000-000000000007")
)

func TestPersonaVersionCanonicalizesAndDetectsMutation(t *testing.T) {
	draft := personaDraft()
	version, err := NewPersonaVersion(draft)
	if err != nil {
		t.Fatal(err)
	}
	if version.Name != "Operations Lead" || string(version.Policy.OutputSchema) != `{"additionalProperties":false,"properties":{"contribution":{"type":"string"}},"required":["contribution"],"type":"object"}` {
		t.Fatalf("not canonical %#v", version)
	}
	if _, err := RestorePersonaVersion(version); err != nil {
		t.Fatal(err)
	}
	draft.Policy.Tools[0].Name = "mutated_by_caller"
	if version.Policy.Tools[0].Name != "read_work" {
		t.Fatal("persona version retained caller-owned tool slice")
	}
	version.SystemInstructions = "Changed instructions that are long enough but were never versioned."
	if _, err := RestorePersonaVersion(version); !errors.Is(err, ErrImmutableVersion) {
		t.Fatalf("expected immutable digest failure, got %v", err)
	}
}

func TestPersonaVersionRejectsToolPolicyMismatchAndDuplicates(t *testing.T) {
	draft := personaDraft()
	draft.Policy.MaximumToolSteps = 0
	if _, err := NewPersonaVersion(draft); !errors.Is(err, ErrInvalidPersona) {
		t.Fatalf("expected disabled-tool rejection, got %v", err)
	}
	draft = personaDraft()
	draft.Policy.Tools = append(draft.Policy.Tools, draft.Policy.Tools[0])
	if _, err := NewPersonaVersion(draft); !errors.Is(err, ErrInvalidPersona) {
		t.Fatalf("expected duplicate tool rejection, got %v", err)
	}
}

func TestRunPlanFreezesOrderedUniquePersonaVersions(t *testing.T) {
	version, _ := NewPersonaVersion(personaDraft())
	plan, err := NewRunPlan(RunPlan{RunID: runID, AccountID: accountID, BoardroomID: boardroomID, ConversationID: conversationID, EntitlementVersion: 4, PolicyVersion: 9, Turns: []PlannedTurn{{Turn: 1, PersonaID: personaID, PersonaVersionID: versionID, PersonaDigest: version.ContentDigest}}, CreatedBy: userID, CreatedAt: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreRunPlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Turns[0].Turn = 2
	if _, err := RestoreRunPlan(plan); !errors.Is(err, ErrInvalidRunPlan) {
		t.Fatalf("expected turn-order failure, got %v", err)
	}
}

func TestResultEnvelopeCanonicalizesActionsAndBoundsDelegations(t *testing.T) {
	result, err := ValidateResult(ResultEnvelope{Contribution: "  We should reconcile the backlog. ", Findings: []string{"  Three items are blocked. "}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{{Kind: "work.create", Reason: "Track the follow-up", Payload: json.RawMessage(`{"priority":"high","title":"Follow up"}`), Evidence: []string{"finding-1"}}}, Delegations: []Delegation{{PersonaID: personaID, Request: "Review the capacity plan."}}, Confidence: ConfidenceHigh})
	if err != nil {
		t.Fatal(err)
	}
	if result.Contribution != "We should reconcile the backlog." || result.Findings[0] != "Three items are blocked." || string(result.ProposedActions[0].Payload) != `{"priority":"high","title":"Follow up"}` {
		t.Fatalf("not canonical %#v", result)
	}
	result.Delegations[0].PersonaID = "bad"
	if _, err := ValidateResult(result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected invalid delegation, got %v", err)
	}
}

func personaDraft() PersonaVersionDraft {
	return PersonaVersionDraft{ID: versionID, PersonaID: personaID, AccountID: accountID, Version: 1, Name: " Operations Lead ", Role: "Operations", Description: "Keeps work moving.", SystemInstructions: "You coordinate operational work and report evidence clearly.", Policy: PersonaPolicy{Provider: "openai", Model: "gpt-5.6", ReasoningEffort: "medium", MaximumInputTokens: 100000, MaximumOutputTokens: 4000, MaximumCostMicros: 500000, MaximumToolSteps: 3, CitationPolicy: "best_effort", ActionPolicy: "propose", Tools: []ToolGrant{{Name: "read_work", Capability: "work.summary.read", Description: "Read the current Work summary.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)}}, OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["contribution"],"properties":{"contribution":{"type":"string"}}}`)}, CreatedBy: userID, CreatedAt: time.Unix(100, 0)}
}
