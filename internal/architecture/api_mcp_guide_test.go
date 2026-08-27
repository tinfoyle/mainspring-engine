package architecture_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAPIMCPGuideMatchesGeneratedInventories(t *testing.T) {
	root := filepath.Join("..", "..")
	guide := readGuideFile(t, filepath.Join(root, "docs", "production", "api-mcp-interaction-guide.md"))

	var contract struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(readGuideFile(t, filepath.Join(root, "api", "spyglass.openapi.json"))), &contract); err != nil {
		t.Fatal(err)
	}
	operations := 0
	for _, methods := range contract.Paths {
		for method := range methods {
			switch strings.ToUpper(method) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				operations++
			}
		}
	}
	if operations != 212 || !strings.Contains(guide, "HTTP contract: `api/spyglass.openapi.json` (212 operations)") {
		t.Fatalf("HTTP operation inventory=%d or guide count is stale", operations)
	}

	toolPattern := regexp.MustCompile(`spyglass_[a-z0-9_]+`)
	toolSources := readGuideFile(t, filepath.Join(root, "internal", "transport", "mcpapi", "registry.go")) +
		readGuideFile(t, filepath.Join(root, "internal", "transport", "mcpgateway", "account_exports.go"))
	tools := make(map[string]struct{})
	for _, name := range toolPattern.FindAllString(toolSources, -1) {
		tools[name] = struct{}{}
	}
	if len(tools) != 94 {
		t.Fatalf("published MCP tool inventory=%d want=94", len(tools))
	}
	for name := range tools {
		if !strings.Contains(guide, "`"+name+"`") {
			t.Errorf("guide is missing MCP tool %s", name)
		}
	}

	for _, required := range []string{
		"https://app.infiniteocean.localhost:8444",
		"https://mcp.infiniteocean.localhost:8444",
		"Idempotency-Key",
		"If-Match",
		"2026-07-28",
		"Mcp-Method",
		"Mcp-Name",
		"spyglass:mcp",
		"package_read_only",
		"package_not_entitled",
		"make verify-google-oauth",
		"make verify-imap",
		"make verify-web-research",
		"make verify-integration-connector",
	} {
		if !strings.Contains(guide, required) {
			t.Errorf("guide is missing required contract %q", required)
		}
	}

	jsonFence := regexp.MustCompile("(?s)```json\\n(.*?)\\n```")
	for _, block := range jsonFence.FindAllStringSubmatch(guide, -1) {
		var value any
		if err := json.Unmarshal([]byte(block[1]), &value); err != nil {
			t.Errorf("guide contains invalid JSON example: %v", err)
		}
	}
}

func readGuideFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
