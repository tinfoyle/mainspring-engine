package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestFinanceTemplateExposesGovernedWorkspaceAndReadOnlyBoundary(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{Title: "Finance", Page: "finance", Selected: &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar"}, FinanceAvailable: true, Script: "/assets/finance.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "finance", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{`src="/assets/finance.js?v=3"`, `href="/app/finance"`, `aria-current="page"`, `id="finance-ledger"`, `id="finance-chart-panel"`, `id="finance-journal-panel"`, `id="finance-reconciliation-panel"`, `data-read-only="false"`, `id="finance-entry-form"`, `id="finance-reconciliation-form"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Finance shell missing %q", expected)
		}
	}

	rendered.Reset()
	data.FinanceReadOnly = true
	if err := pages.ExecuteTemplate(&rendered, "finance", data); err != nil {
		t.Fatal(err)
	}
	readOnly := rendered.String()
	for _, forbidden := range []string{`id="finance-new-ledger"`, `id="finance-new-entry"`, `id="finance-account-form"`, `id="finance-reconciliation-form"`} {
		if strings.Contains(readOnly, forbidden) {
			t.Fatalf("read-only Finance shell contains mutation control %q", forbidden)
		}
	}
}

func TestFinanceClientUsesRoutedVersionedLifecycleBoundaries(t *testing.T) {
	raw, err := assets.ReadFile("assets/finance.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{
		"/finance`;", `"If-Match"`, `"Idempotency-Key": crypto.randomUUID()`, `textContent`,
		`/period-closes`, `/postings`, `/reversals`, `/confirmations`, `difference_minor === 0`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Finance client missing %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "document.cookie", "localStorage", "sessionStorage"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Finance client contains %q", forbidden)
		}
	}
}

func TestFinanceStylesArePartOfEmbeddedPrivateShell(t *testing.T) {
	raw, err := assets.ReadFile("assets/finance.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".finance-layout", ".finance-summary", ".finance-dialog"} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("Finance styles missing %q", expected)
		}
	}
}
