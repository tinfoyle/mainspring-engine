package agents

import (
	"encoding/json"
	"errors"
	"slices"
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

func TestPersonaPolicyProjectsDurableAndLegacyComplexityWithoutChangingLegacyContent(t *testing.T) {
	draft := personaDraft()
	draft.Policy.Complexity = PersonaComplexityThorough
	version, err := NewPersonaVersion(draft)
	if err != nil || version.Policy.CustomerComplexity() != PersonaComplexityThorough {
		t.Fatalf("complexity=%q err=%v", version.Policy.CustomerComplexity(), err)
	}
	legacy := personaDraft()
	legacy.Policy.Complexity = ""
	legacy.Policy.Model = "advanced"
	legacyVersion, err := NewPersonaVersion(legacy)
	if err != nil || legacyVersion.Policy.Complexity != "" || legacyVersion.Policy.CustomerComplexity() != PersonaComplexityAdvanced {
		t.Fatalf("legacy=%+v err=%v", legacyVersion.Policy, err)
	}
	legacy.Policy.Model = "gpt-legacy"
	legacyVersion, err = NewPersonaVersion(legacy)
	if err != nil || legacyVersion.Policy.CustomerComplexity() != PersonaComplexityBalanced {
		t.Fatalf("legacy default=%q err=%v", legacyVersion.Policy.CustomerComplexity(), err)
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

func TestPersonaVersionFreezesOrderedUniqueFallbackModels(t *testing.T) {
	draft := personaDraft()
	draft.Policy.FallbackModels = []string{" gpt-5.5 ", "gpt-5.4"}
	version, err := NewPersonaVersion(draft)
	if err != nil || !slices.Equal(version.Policy.ModelTargets(), []string{"gpt-5.6", "gpt-5.5", "gpt-5.4"}) {
		t.Fatalf("targets=%v err=%v", version.Policy.ModelTargets(), err)
	}
	draft.Policy.FallbackModels[0] = "mutated"
	if version.Policy.FallbackModels[0] != "gpt-5.5" {
		t.Fatal("persona version retained caller-owned fallback slice")
	}
	draft = personaDraft()
	draft.Policy.FallbackModels = []string{draft.Policy.Model}
	if _, err := NewPersonaVersion(draft); !errors.Is(err, ErrInvalidPersona) {
		t.Fatalf("expected duplicate model rejection, got %v", err)
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

func TestResultSchemaIsAcceptedAsImmutablePersonaPolicy(t *testing.T) {
	draft := personaDraft()
	draft.Policy.OutputSchema = ResultSchema()
	version, err := NewPersonaVersion(draft)
	if err != nil || len(version.Policy.OutputSchema) == 0 || version.Policy.OutputSchema[0] != '{' {
		t.Fatalf("version=%+v err=%v", version, err)
	}
}

func TestResultEnvelopeValidatesConversationalBaselineGuidance(t *testing.T) {
	result := ResultEnvelope{Contribution: "Tell me how a new job reaches you.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, Delegations: []Delegation{}, Confidence: ConfidenceMedium,
		Baseline: &BaselineInterview{BusinessType: "Field service business", BusinessTypeConfidence: ConfidenceMedium, CapturedTopics: []string{"Residential plumbing"}, NextQuestionKey: "baseline.revenue_workflow", NextQuestion: "How does a new service call reach you?", QuestionReason: "This shows how demand becomes scheduled work.", AutomationOffers: []BaselineAutomationOffer{}, ApprovedWork: []BaselineApprovedWork{}, Ready: false, ReadinessReason: "Still learning how jobs move.", MissingTopics: []string{"Scheduling", "Billing"}}}
	validated, err := ValidateResult(result)
	if err != nil || validated.Baseline == nil || validated.Baseline.NextQuestionKey != "baseline.revenue_workflow" {
		t.Fatalf("validated=%+v err=%v", validated, err)
	}
	result.Baseline.ApprovedWork = []BaselineApprovedWork{
		{Key: "schedule.dispatch", Title: "Set up dispatch review", Description: "Choose the calendar and exception owner.", Priority: "high"},
		{Key: "billing.follow_up", Title: "Set up invoice follow-up", Description: "Choose when overdue reminders begin.", Priority: "normal"},
	}
	if _, err := ValidateResult(result); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected one-work-per-turn bound, got %v", err)
	}
}

func TestResultEnvelopeRejectsPrematureBaselineReadinessAndForeignQuestionKeys(t *testing.T) {
	base := ResultEnvelope{Contribution: "Your setup is ready.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []Citation{}, ProposedActions: []ProposedAction{}, Delegations: []Delegation{}, Confidence: ConfidenceHigh,
		Baseline: &BaselineInterview{BusinessType: "Field service business", BusinessTypeConfidence: ConfidenceHigh, CapturedTopics: []string{"Customers", "Work flow", "Scheduling", "Existing records"}, NextQuestionKey: "", NextQuestion: "", QuestionReason: "", AutomationOffers: []BaselineAutomationOffer{}, ApprovedWork: []BaselineApprovedWork{}, Ready: true, ReadinessReason: "Enough operating context is in place.", MissingTopics: []string{}}}
	if _, err := ValidateResult(base); err != nil {
		t.Fatalf("expected mature baseline readiness, got %v", err)
	}
	premature := base
	premature.Baseline = &BaselineInterview{BusinessType: "Field service business", BusinessTypeConfidence: ConfidenceHigh, CapturedTopics: []string{"Customers"}, AutomationOffers: []BaselineAutomationOffer{}, ApprovedWork: []BaselineApprovedWork{}, Ready: true, ReadinessReason: "Ready too early.", MissingTopics: []string{}}
	if _, err := ValidateResult(premature); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected premature readiness rejection, got %v", err)
	}
	foreign := base
	foreign.Baseline = &BaselineInterview{BusinessType: "Field service business", BusinessTypeConfidence: ConfidenceMedium, CapturedTopics: []string{"Customers"}, NextQuestionKey: "knowledge.customer", NextQuestion: "Who hires you?", QuestionReason: "This identifies the customer.", AutomationOffers: []BaselineAutomationOffer{}, ApprovedWork: []BaselineApprovedWork{}, Ready: false, ReadinessReason: "Still learning.", MissingTopics: []string{"Work flow"}}
	if _, err := ValidateResult(foreign); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected non-baseline question-key rejection, got %v", err)
	}
}

func personaDraft() PersonaVersionDraft {
	return PersonaVersionDraft{ID: versionID, PersonaID: personaID, AccountID: accountID, Version: 1, Name: " Operations Lead ", Role: "Operations", Description: "Keeps work moving.", SystemInstructions: "You coordinate operational work and report evidence clearly.", Policy: PersonaPolicy{Complexity: PersonaComplexityBalanced, Provider: "openai", Model: "gpt-5.6", ReasoningEffort: "medium", MaximumInputTokens: 100000, MaximumOutputTokens: 4000, MaximumCostMicros: 500000, MaximumToolSteps: 3, CitationPolicy: "best_effort", ActionPolicy: "propose", Tools: []ToolGrant{{Name: "read_work", Capability: "work.summary.read", Description: "Read the current Work summary.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)}}, OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["contribution"],"properties":{"contribution":{"type":"string"}}}`)}, CreatedBy: userID, CreatedAt: time.Unix(100, 0)}
}
