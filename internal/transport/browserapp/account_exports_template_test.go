package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAccountExportTemplateExposesGovernedActionsWithoutObjectIdentity(t *testing.T) {
	parsed, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("11111111-1111-4111-8111-111111111111")
	choice := accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar", AccountVersion: 7, Role: accounts.RoleOwner}
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	availableAt := now.Add(-time.Minute)
	exports := []accountexport.Status{
		{ID: "22222222-2222-4222-8222-222222222222", AccountID: accountID, State: accountexport.StateAvailable, ArtifactBytes: 4096, Version: 3, RequestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(7 * 24 * time.Hour), AvailableAt: &availableAt},
		{ID: "33333333-3333-4333-8333-333333333333", AccountID: accountID, State: accountexport.StateQueued, Version: 1, RequestedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)},
	}
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "account-exports", pageData{Title: "Account exports", Page: "account-exports", Choices: []accountaccess.Choice{choice}, Selected: &choice, CanManageExports: true, Exports: exports}); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	for _, expected := range []string{`action="/app/account-exports/request"`, `pattern="EXPORT"`, `action="/app/account-exports/download"`, `action="/app/account-exports/cancel"`, `name="version" value="1"`, "Artifact object identity and content digest remain private"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("Account export page does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"artifact_reference", "artifact_sha256", "s3-export-v1"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("Account export page exposed %q", forbidden)
		}
	}
}
