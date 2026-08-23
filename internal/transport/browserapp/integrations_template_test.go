package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestIntegrationsTemplateExposesGovernedWorkspaceAndDualControl(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	data := pageData{Title: "Integrations", Page: "integrations", ActorUserID: "9b200000-0000-4000-8000-000000000002",
		Selected:              &accountaccess.Choice{AccountID: ids.AccountID("9b100000-0000-4000-8000-000000000001"), DisplayName: "Northstar"},
		IntegrationsAvailable: true, Script: "/assets/integrations.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "integrations", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{`src="/assets/integrations.js?v=3"`, `href="/app/integrations"`, `aria-current="page"`,
		`id="integrations-app"`, `data-user-id="9b200000-0000-4000-8000-000000000002"`, `id="integrations-connection-list"`,
		`id="integrations-execution-list"`, `id="integrations-resolution-form"`, `DUAL-CONTROLLED RECOVERY`, `This does not resend the operation.`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Integrations shell missing %q", expected)
		}
	}

	rendered.Reset()
	data.IntegrationsReadOnly = true
	if err := pages.ExecuteTemplate(&rendered, "integrations", data); err != nil {
		t.Fatal(err)
	}
	readOnly := rendered.String()
	for _, forbidden := range []string{`id="integrations-new-connection"`, `id="integrations-prepare-execution"`,
		`id="integrations-connection-form"`, `id="integrations-credential-form"`, `id="integrations-resolution-form"`} {
		if strings.Contains(readOnly, forbidden) {
			t.Fatalf("read-only Integrations shell contains mutation control %q", forbidden)
		}
	}
}

func TestIntegrationsClientUsesRoutedContentFreeDualControl(t *testing.T) {
	raw, err := assets.ReadFile("assets/integrations.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{"/integrations`;", `"If-Match"`, `"Idempotency-Key": crypto.randomUUID()`,
		`/resolution-requests`, `/resolutions/${resolutionID}/confirmations`, `evidence_sha256`, `requested_outcome`, `textContent`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Integrations client missing %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "document.cookie", "localStorage", "sessionStorage", "window.prompt", "provider_payload", "secret_key"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Integrations client contains %q", forbidden)
		}
	}
}

func TestIntegrationsStylesArePartOfEmbeddedPrivateShell(t *testing.T) {
	raw, err := assets.ReadFile("assets/integrations.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".integrations-layout", ".integrations-resolution", ".integrations-dialog"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("Integrations styles missing %q", expected)
		}
	}
}
