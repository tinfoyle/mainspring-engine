package boardroom

import (
	"encoding/json"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
)

func TestAttachParentWorkItemToActionsOverridesModelParent(t *testing.T) {
	result := agent.Result{Structured: agent.ResultEnvelope{ProposedActions: []agent.ProposedAction{{
		ActionType: "tickets.create",
		Payload:    json.RawMessage(`{"title":"Investigate","parent_work_item_id":"wrong"}`),
	}}}}
	result = attachParentWorkItemToActions(result, "parent-123")
	var payload map[string]any
	if err := json.Unmarshal(result.Structured.ProposedActions[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["parent_work_item_id"] != "parent-123" {
		t.Fatalf("parent = %#v", payload["parent_work_item_id"])
	}
}
