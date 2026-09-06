// Package agenttools owns the platform tool definitions shared by publishing,
// dispatch, broker registration, and the generated agent editor.
package agenttools

import (
	_ "embed"
	"encoding/json"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/modules/agents"
)

//go:embed catalog.json
var CatalogJSON []byte

type Tool struct {
	Help string `json:"help"`
	agents.ToolGrant
	Label          string                  `json:"label"`
	Effect         runnercapability.Effect `json:"effect"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
	Handler        string                  `json:"handler"`
}
type Action struct {
	Capability  string          `json:"capability"`
	Label       string          `json:"label"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type Catalog struct {
	Tools   []Tool   `json:"tools"`
	Actions []Action `json:"actions"`
}

func Definitions() Catalog {
	var catalog Catalog
	if err := json.Unmarshal(CatalogJSON, &catalog); err != nil {
		panic(err)
	}
	return catalog
}
func Lookup(capability string) (Tool, bool) {
	for _, tool := range Definitions().Tools {
		if tool.Capability == capability {
			return tool, true
		}
	}
	return Tool{}, false
}
func LookupAction(capability string) (Action, bool) {
	for _, action := range Definitions().Actions {
		if action.Capability == capability {
			return action, true
		}
	}
	return Action{}, false
}

// ResultSchema supplies complete, closed payload schemas for the granted action
// kinds. The immutable result-policy check remains the authorization boundary.
func ResultSchema(base json.RawMessage, capabilities []string) json.RawMessage {
	var schema map[string]any
	if json.Unmarshal(base, &schema) != nil {
		return base
	}
	variants := []any{}
	for _, capability := range capabilities {
		action, ok := LookupAction(capability)
		if !ok {
			continue
		}
		var payload any
		_ = json.Unmarshal(action.InputSchema, &payload)
		variants = append(variants, map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"kind", "reason", "payload", "evidence"},
			"properties": map[string]any{
				"kind":     map[string]any{"type": "string", "enum": []string{capability}},
				"reason":   map[string]any{"type": "string", "minLength": 3, "maxLength": 4000},
				"payload":  payload,
				"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}},
			},
		})
	}
	if len(variants) == 0 {
		return base
	}
	schema["properties"].(map[string]any)["proposed_actions"].(map[string]any)["items"] = map[string]any{"anyOf": variants}
	result, _ := json.Marshal(schema)
	return result
}
