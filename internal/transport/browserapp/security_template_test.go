package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
)

func TestSecurityRecoveryWorksWhileLostCredentialsRemainRegistered(t *testing.T) {
	parsed, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	data := pageData{
		Title:                   "Identity security",
		PasskeysConfigured:      true,
		RecoveryCodesConfigured: true,
		Passkeys:                []passkeys.CredentialSummary{{ID: "registered-but-lost", Name: "Lost device"}},
		RecoveryCodeStatus:      recoverycodes.Status{Configured: true, Version: 1, Remaining: 4},
	}
	if err := parsed.ExecuteTemplate(&output, "security", data); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{"Lost access to every passkey?", "works even while the lost credentials still appear", `action="/app/security/recovery-codes/consume"`, "Infinite Ocean support cannot view or recreate recovery codes"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("security recovery policy missing %q: %s", expected, body)
		}
	}
}

func TestSecurityPageExposesActiveMCPGrantRevocation(t *testing.T) {
	parsed, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	lastUsed := time.Date(2026, 8, 22, 23, 30, 0, 0, time.UTC)
	var output bytes.Buffer
	data := pageData{
		Title:               "Identity security",
		MCPGrantsConfigured: true,
		MCPGrants: []mcpauth.GrantSummary{{
			ID: "40000000-0000-4000-8000-000000000004", ClientID: "https://client.example/oauth/metadata.json",
			ClientName: "Desktop MCP Client", CreatedAt: lastUsed.Add(-time.Hour), LastUsedAt: &lastUsed,
		}},
	}
	if err := parsed.ExecuteTemplate(&output, "security", data); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{"Desktop MCP Client", "https://client.example/oauth/metadata.json", `action="/app/security/mcp-grants/revoke"`, `name="grant_id" value="40000000-0000-4000-8000-000000000004"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("MCP grant control missing %q: %s", expected, body)
		}
	}
}
