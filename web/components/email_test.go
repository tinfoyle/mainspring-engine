package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBaselineEmailSetupExposesCredentialFieldsAndReturnPath(t *testing.T) {
	var output bytes.Buffer
	integration := &EmailIntegrationView{
		Name: "Office inbox", EmailAddress: "office@example.test", DisplayName: "Office",
		IMAPServer: "imap.example.test:993", SMTPServer: "smtp.example.test:587", Status: "active",
	}
	if err := EmailPage("Demo", UserView{}, integration, nil, "csrf", "", "", "key", false, true).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}

	html := output.String()
	for _, expected := range []string{
		"Optional integration", "Connect a mailbox to the documented baseline", `name="return_to" value="baseline"`,
		`name="imap_host"`, `name="imap_password"`, `name="smtp_host"`, `name="smtp_password"`,
		"Use this mailbox", "Replace with a different mailbox", "Saved usernames and passwords are never copied",
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("baseline mailbox setup missing %q: %s", expected, html)
		}
	}
	if strings.Contains(html, "Send through SMTP") {
		t.Fatalf("baseline mailbox setup rendered the general inbox composer: %s", html)
	}
	for _, prefilled := range []string{`value="office@example.test"`, `value="imap.example.test"`, `value="smtp.example.test"`} {
		if strings.Contains(html, prefilled) {
			t.Fatalf("baseline mailbox replacement form exposed saved setting %q: %s", prefilled, html)
		}
	}
}
