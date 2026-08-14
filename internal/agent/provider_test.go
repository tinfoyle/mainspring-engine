package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInvocationJSONOmitsCapabilityToken(t *testing.T) {
	body, err := json.Marshal(Invocation{CapabilityToken: "sensitive-capability-token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sensitive-capability-token") || strings.Contains(string(body), "CapabilityToken") {
		t.Fatalf("provider payload exposed capability token: %s", body)
	}
}

func TestDefaultOutputSchemaIsStrictAndSupportsWebResearch(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(DefaultOutputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	if err := requireEveryObjectProperty(schema, "$"); err != nil {
		t.Fatal(err)
	}
	body := string(DefaultOutputSchema())
	for _, tool := range []string{`"const":"documents.search"`, `"const":"documents.create"`, `"const":"documents.update"`, `"const":"web.search"`, `"const":"web.read"`} {
		if !strings.Contains(body, tool) {
			t.Fatalf("default output schema does not support %s", tool)
		}
	}
}

func requireEveryObjectProperty(value any, path string) error {
	switch item := value.(type) {
	case map[string]any:
		if properties, ok := item["properties"].(map[string]any); ok && item["additionalProperties"] == false {
			required := make(map[string]bool)
			requiredValues, ok := item["required"].([]any)
			if !ok {
				return fmt.Errorf("%s declares properties without a required array", path)
			}
			for _, value := range requiredValues {
				name, ok := value.(string)
				if !ok {
					return fmt.Errorf("%s has a non-string required property", path)
				}
				required[name] = true
			}
			for name := range properties {
				if !required[name] {
					return fmt.Errorf("%s declares property %q without requiring it", path, name)
				}
			}
		}
		for name, child := range item {
			if err := requireEveryObjectProperty(child, path+"."+name); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range item {
			if err := requireEveryObjectProperty(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}
