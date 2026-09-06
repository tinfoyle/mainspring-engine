package agenttools

import (
	"encoding/json"
	"github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"testing"
)

func TestCatalogHasUniqueExecutableToolsAndCompleteActionPayloads(t *testing.T) {
	names, caps := map[string]bool{}, map[string]bool{}
	for _, v := range Definitions().Tools {
		if names[v.Name] || caps[v.Capability] || v.Name == "" || v.TimeoutSeconds <= 0 || (v.Handler != "router" && v.Handler != "schedule_prepare") {
			t.Fatalf("invalid tool: %+v", v)
		}
		names[v.Name], caps[v.Capability] = true, true
		var schema map[string]any
		if json.Unmarshal(v.InputSchema, &schema) != nil || schema["type"] != "object" {
			t.Fatalf("invalid schema for %s", v.Name)
		}
	}
	var schema map[string]any
	_ = json.Unmarshal(ResultSchema(agents.ResultSchema(), []string{"schedules.create"}), &schema)
	items := schema["properties"].(map[string]any)["proposed_actions"].(map[string]any)["items"].(map[string]any)
	variants := items["anyOf"].([]any)
	props := variants[0].(map[string]any)["properties"].(map[string]any)
	if len(variants) != 1 || props["payload"].(map[string]any)["properties"].(map[string]any)["email_self"] == nil {
		t.Fatal("schedule proposal cannot carry its payload")
	}
}
