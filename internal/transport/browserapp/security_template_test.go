package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

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
