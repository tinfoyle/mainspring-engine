package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestKnowledgeTemplateExposesReviewQueueWithoutAuthoritativeAgentControls(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{Title: "Knowledge", Page: "knowledge", Selected: &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar"}, KnowledgeAvailable: true, Script: "/assets/knowledge.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "knowledge", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{`src="/assets/knowledge.js?v=2"`, `aria-current="page"`, `id="knowledge-claims"`, `id="knowledge-detail"`, `id="knowledge-facts"`, `data-read-only="false"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Knowledge shell missing %q", expected)
		}
	}
	if strings.Contains(body, "accept agent output automatically") {
		t.Fatal("unsafe authority language")
	}
}

func TestKnowledgeClientUsesRoutedVersionedDecisionBoundary(t *testing.T) {
	raw, err := assets.ReadFile("assets/knowledge.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{"/knowledge`;", "state=proposed", `"If-Match": etag`, `"Idempotency-Key": crypto.randomUUID()`, "textContent"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Knowledge client missing %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "document.cookie", "localStorage"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Knowledge client contains %q", forbidden)
		}
	}
}
