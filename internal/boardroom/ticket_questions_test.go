package boardroom

import (
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

func TestOwnerQuestionsBecomeApprovalGatedSubtasks(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{
		Contribution: "I completed the public research and need one private business fact.",
		Questions:    []string{"Who is responsible for approving production access?"},
		Confidence:   "medium",
	}}
	result = ownerQuestionsAsWork(result, true, "propose")
	if len(result.Structured.ProposedActions) != 1 {
		t.Fatalf("proposed action count = %d, want 1", len(result.Structured.ProposedActions))
	}
	action := result.Structured.ProposedActions[0]
	if action.ActionType != toolbroker.WorkInputAction {
		t.Fatalf("action type = %q", action.ActionType)
	}
	result = attachParentWorkItemToActions(result, "00000000-0000-0000-0000-000000000123")
	payload, err := toolbroker.DecodeWorkInputPayload(result.Structured.ProposedActions[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ParentWorkItemID == "" || len(payload.Questions) != 1 || !strings.Contains(payload.Questions[0], "production access") {
		t.Fatalf("owner subtask payload = %#v", payload)
	}
}

func TestOwnerQuestionsStayInResultWhenActionsAreDisabled(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{Questions: []string{"Need owner input"}}}
	result = ownerQuestionsAsWork(result, true, "disabled")
	if len(result.Structured.ProposedActions) != 0 {
		t.Fatal("disabled action policy created an owner subtask")
	}
}

func TestOwnerQuestionsAreConsolidatedIntoOneSubtask(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{Questions: []string{
		"Which accounting system is authoritative?",
		"Which reporting period should be used?",
	}}}
	result = ownerQuestionsAsWork(result, true, "propose")
	if len(result.Structured.ProposedActions) != 1 {
		t.Fatalf("proposed action count = %d, want 1", len(result.Structured.ProposedActions))
	}
	result = attachParentWorkItemToActions(result, "00000000-0000-0000-0000-000000000123")
	payload, err := toolbroker.DecodeWorkInputPayload(result.Structured.ProposedActions[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Questions) != 2 || !strings.Contains(payload.Questions[0], "accounting system") || !strings.Contains(payload.Questions[1], "reporting period") {
		t.Fatalf("consolidated questions = %#v", payload.Questions)
	}
}
