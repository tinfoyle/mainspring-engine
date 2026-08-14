package boardroom

import (
	"encoding/json"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
)

// attachParentWorkItemToActions binds every ticket or owner-input request in a ticket
// workspace to that workspace's parent. The model cannot redirect the action
// to an unrelated ticket because this application-owned value wins.
func attachParentWorkItemToActions(result agent.Result, parentID string) agent.Result {
	for index, action := range result.Structured.ProposedActions {
		if action.ActionType != "tickets.create" && action.ActionType != "work.input" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(action.Payload, &payload) != nil {
			continue
		}
		payload["parent_work_item_id"] = parentID
		encoded, err := json.Marshal(payload)
		if err == nil {
			result.Structured.ProposedActions[index].Payload = encoded
		}
	}
	return result
}
