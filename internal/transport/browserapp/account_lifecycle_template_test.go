package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAccountLifecycleTemplatesExposeOwnerClosureAndGlobalRecovery(t *testing.T) {
	parsed, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("11111111-1111-4111-8111-111111111111")
	choice := accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar", AccountVersion: 7, Role: accounts.RoleOwner}
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "app", pageData{Title: "Spyglass", Choices: []accountaccess.Choice{choice}, Selected: &choice, CanCloseAccount: true}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`action="/app/account-closures/request"`, `name="account_version" value="7"`, `pattern="CLOSE"`, "7-day cooling-off period", "does not synchronously erase data"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("owner Account page does not contain %q", expected)
		}
	}

	now := time.Date(2026, 8, 18, 17, 0, 0, 0, time.UTC)
	output.Reset()
	if err := parsed.ExecuteTemplate(&output, "closures", pageData{Title: "Account lifecycle", Closures: []accountlifecycle.Status{{RequestID: "request", AccountID: accountID, AccountName: "Northstar", AccountState: accounts.AccountClosing, AccountVersion: 8, State: accountlifecycle.StateCoolingOff, Reason: "Business concluded", RequestedAt: now, ExecuteAfter: now.Add(7 * 24 * time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`action="/app/account-closures/cancel"`, `name="account_version" value="8"`, `pattern="RESTORE"`, "identity-wide surface", "Physical data erasure"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("global lifecycle page does not contain %q", expected)
		}
	}
}
