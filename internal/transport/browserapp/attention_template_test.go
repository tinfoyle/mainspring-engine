package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestYourTurnTemplatePublishesOnePackageAwareDecisionQueue(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	choice := accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio", Role: accounts.RoleOwner}
	data := pageData{
		Title: "Your Turn", Page: "your-turn", Selected: &choice, Choices: []accountaccess.Choice{choice}, Script: "/assets/attention.js", ActorUserID: "20000000-0000-4000-8000-000000000002",
		AttentionAvailable: true, WorkAvailable: true, ApprovalsAvailable: true,
	}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "your-turn", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{
		`src="/assets/attention.js?v=2"`,
		`class="active" aria-current="page" href="/app/your-turn"`,
		`id="attention-app"`,
		`data-account-id="01J00000000000000000000000"`,
		`data-user-id="20000000-0000-4000-8000-000000000002"`,
		`data-work-available="true"`,
		`data-approvals-available="true"`,
		`id="attention-command-status" role="status" aria-live="polite"`,
		`data-attention-filter="information"`,
		`data-attention-filter="review"`,
		`data-attention-filter="approval"`,
		`id="attention-detail" tabindex="-1"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Your Turn shell missing %q: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"payload_sha256", "capacity_reservation", "private reviewer rationale"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Your Turn shell exposed internal content %q", forbidden)
		}
	}
}

func TestYourTurnTemplateOmitsApprovalQueueWithoutApprovalRole(t *testing.T) {
	pages, _ := template.New("pages").Parse(pageTemplates)
	choice := accountaccess.Choice{AccountID: "01J00000000000000000000000", DisplayName: "Northstar Studio", Role: accounts.RoleMember}
	data := pageData{Title: "Your Turn", Selected: &choice, AttentionAvailable: true, WorkAvailable: true, AgentsAvailable: true}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "your-turn", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if strings.Contains(body, `data-attention-filter="approval"`) || !strings.Contains(body, `data-approvals-available="false"`) {
		t.Fatalf("non-approver received approval queue contract: %s", body)
	}
}

func TestYourTurnScriptPreservesDraftConcurrencyAndAccessibleKeyboardContract(t *testing.T) {
	raw, err := assets.ReadFile("assets/attention.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{
		"spyglass.attention.decision.v1.${accountID}.${kind}.${id}",
		"window.sessionStorage.setItem(draftKey",
		"window.sessionStorage.removeItem(draftKey",
		`"If-Match": `,
		`crypto.randomUUID()`,
		`error.code === "attention_version_conflict"`,
		`detail.focus()`,
		`event.key !== "ArrowDown"`,
		`announce(`,
		`requirement: item.requirement`,
		`reviewer_id=${encodeURIComponent(userID)}`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Your Turn client is missing browser contract %q", expected)
		}
	}
	for _, forbidden := range []string{"window.prompt", "localStorage", "innerHTML", "document.cookie"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Your Turn client contains forbidden browser behavior %q", forbidden)
		}
	}
}
