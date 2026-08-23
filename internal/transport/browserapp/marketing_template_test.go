package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestMarketingTemplateExposesGovernedWorkspaceAndReadOnlyBoundary(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{
		Title:              "Marketing",
		Page:               "marketing",
		Selected:           &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar"},
		MarketingAvailable: true,
		Script:             "/assets/marketing.js",
	}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "marketing", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{
		`src="/assets/marketing.js?v=3"`, `href="/app/marketing"`, `aria-current="page"`,
		`id="marketing-app"`, `id="marketing-campaign-list"`, `id="marketing-campaign-detail"`,
		`id="marketing-asset-list"`, `id="marketing-release-list"`, `data-read-only="false"`,
		`id="marketing-campaign-form"`, `id="marketing-asset-form"`, `id="marketing-release-form"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Marketing shell missing %q", expected)
		}
	}

	rendered.Reset()
	data.MarketingReadOnly = true
	if err := pages.ExecuteTemplate(&rendered, "marketing", data); err != nil {
		t.Fatal(err)
	}
	readOnly := rendered.String()
	for _, forbidden := range []string{
		`id="marketing-new-campaign"`, `id="marketing-new-asset"`, `id="marketing-new-release"`,
		`id="marketing-campaign-form"`, `id="marketing-asset-form"`, `id="marketing-release-form"`,
	} {
		if strings.Contains(readOnly, forbidden) {
			t.Fatalf("read-only Marketing shell contains mutation control %q", forbidden)
		}
	}
}

func TestMarketingClientUsesRoutedVersionedLifecycleBoundaries(t *testing.T) {
	raw, err := assets.ReadFile("assets/marketing.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{
		"/marketing`;", `"If-Match"`, `"Idempotency-Key": crypto.randomUUID()`, `textContent`,
		`/asset-revisions`, `/releases`, `"submissions"`, `"approvals"`, `"cancellations"`,
		`"activations"`, `"pauses"`, `"completions"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Marketing client missing %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "document.cookie", "localStorage", "sessionStorage"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Marketing client contains %q", forbidden)
		}
	}
}

func TestMarketingStylesArePartOfEmbeddedPrivateShell(t *testing.T) {
	raw, err := assets.ReadFile("assets/marketing.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".marketing-layout", ".marketing-columns", ".marketing-dialog"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("Marketing styles missing %q", expected)
		}
	}
}
