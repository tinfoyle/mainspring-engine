package mockconnector

import (
	"strings"
	"testing"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const validRuntimeJSON = `{
  "credentials":[{"id":"e1100000-0000-4000-8000-000000000001","generation":1,"material_base64":"bG9jYWwtb25seQ=="}],
  "objects":{"mock://approved":"YXBwcm92ZWQgY29udGVudA=="},
  "scripts":[{"execution_id":"e1200000-0000-4000-8000-000000000002","steps":[{"mode":"execute","outcome":"succeeded"}]}],
  "connector_timeout":"2s"
}`

func TestParseRuntimeBuildsClosedMockDependencies(t *testing.T) {
	runtime, err := ParseRuntime(strings.NewReader(validRuntimeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Broker == nil || runtime.Contents == nil || len(runtime.Definitions) != 2 ||
		runtime.Definitions[0].Capability != domain.CapabilityEmailSend || runtime.Definitions[1].Capability != domain.CapabilityWebPublish {
		t.Fatalf("runtime=%+v", runtime)
	}
}

func TestParseRuntimeRejectsUnknownOrUnsafeConfiguration(t *testing.T) {
	tests := []string{
		`{"unknown":true}`,
		validRuntimeJSON + `{}`,
		strings.Replace(validRuntimeJSON, `"mode":"execute"`, `"mode":"invalid"`, 1),
		strings.Replace(validRuntimeJSON, `"outcome":"succeeded"`, `"outcome":"failed"`, 1),
		strings.Replace(validRuntimeJSON, `"connector_timeout":"2s"`, `"connector_timeout":"6m"`, 1),
	}
	for _, value := range tests {
		if _, err := ParseRuntime(strings.NewReader(value)); err == nil {
			t.Fatalf("unsafe runtime accepted: %s", value)
		}
	}
}
