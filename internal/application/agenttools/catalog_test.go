package agenttools

import (
	"encoding/json"
	"github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"testing"
)

func TestCatalogHasUniqueExecutableToolsAndCompleteActionPayloads(t *testing.T) {
	names, caps := map[string]bool{}, map[string]bool{}
	for _, v := range Definitions().Tools {
		if names[v.Name] || caps[v.Capability] || v.Name == "" || v.Help == "" || v.TimeoutSeconds <= 0 || (v.Handler != "router" && v.Handler != "schedule_prepare") {
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

// Strict tool calling requires every property to be present. Optional API
// parameters use a nullable branch and still undergo normal handler validation.
// Reference: https://developers.openai.com/api/docs/guides/structured-outputs
func TestEveryCatalogSchemaMatchesStrictProviderSubset(t *testing.T) {
	for _, tool := range Definitions().Tools {
		checkStrictSchema(t, tool.Name, tool.InputSchema)
	}
	for _, action := range Definitions().Actions {
		checkStrictSchema(t, action.Capability, action.InputSchema)
	}
}
func checkStrictSchema(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case []any:
			for _, child := range v {
				walk(child)
			}
		case map[string]any:
			if _, ok := v["uniqueItems"]; ok {
				t.Fatalf("%s: unsupported uniqueItems", name)
			}
			if format, ok := v["format"].(string); ok && format != "uuid" && format != "date-time" {
				t.Fatalf("%s: unsupported format %s", name, format)
			}
			if v["type"] == "object" {
				props := v["properties"].(map[string]any)
				required, ok := v["required"].([]any)
				if !ok || len(required) != len(props) || v["additionalProperties"] != false {
					t.Fatalf("%s: object must be closed with all fields required", name)
				}
				for _, key := range required {
					if _, ok := props[key.(string)]; !ok {
						t.Fatalf("%s: invalid required field", name)
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(root)
}
