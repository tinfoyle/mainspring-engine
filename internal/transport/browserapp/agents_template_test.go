package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAgentsTemplateExposesRoutedBoardroomWorkspaceForAvailablePackage(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{
		Title: "Agents", Page: "agents",
		Selected:        &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"},
		Choices:         []accountaccess.Choice{{AccountID: accountID, DisplayName: "Northstar Studio"}},
		AgentsAvailable: true, Script: "/assets/agents.js",
	}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "agents", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{
		`src="/assets/agents.js?v=2"`,
		`class="active" aria-current="page" href="/app/agents"`,
		`data-account-id="01J00000000000000000000000"`,
		`data-read-only="false"`,
		`id="agents-room-list"`,
		`id="agents-personas"`,
		`id="agents-run-form"`,
		`id="agents-persona-picker"`,
		`id="agents-conversation-list"`,
		`id="agents-message-list"`,
		`id="agents-run-recovery"`,
		`value="retry_failed"`,
		`value="accept_failure"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Agents shell missing %q: %s", expected, body)
		}
	}
	for _, internal := range []string{"structured_result", "plan_digest", "entitlement_version", "invocation_id"} {
		if strings.Contains(body, internal) {
			t.Fatalf("Agents shell exposed internal execution field %q: %s", internal, body)
		}
	}
}

func TestAgentsTemplateKeepsRunControlsOutOfReadOnlyAccess(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{Title: "Agents", Page: "agents", Selected: &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"}, AgentsAvailable: true, AgentsReadOnly: true, Script: "/assets/agents.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "agents", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if !strings.Contains(body, `data-read-only="true"`) || !strings.Contains(body, `id="agents-message-list"`) || strings.Contains(body, `id="agents-run-form"`) || strings.Contains(body, `id="agents-new-conversation"`) || strings.Contains(body, `id="agents-run-recovery"`) {
		t.Fatalf("read-only Agents mutation surface: %s", body)
	}
}

func TestAgentsScriptUsesSafeRoutedBrowserBoundary(t *testing.T) {
	raw, err := assets.ReadFile("assets/agents.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{
		"/api/v1/accounts/${encodeURIComponent(accountID)}",
		`credentials: "same-origin"`,
		`pendingOperations.set(fingerprint, operationID)`,
		`"Idempotency-Key": operationID`,
		`/resolutions`,
		`"retry_failed"`,
		`"accept_failure"`,
		"document.createElement",
		"textContent",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Agents client is missing boundary %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "localStorage", "sessionStorage", "document.cookie", "cell_id", "plan_digest", "structured_result"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Agents client contains forbidden browser behavior or internal field %q", forbidden)
		}
	}
}
