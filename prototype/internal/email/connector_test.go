package email

import (
	"bytes"
	"io"
	"net/mail"
	"strings"
	"testing"
)

func TestExtractEvidencePartsIncludesAttachments(t *testing.T) {
	raw := "Content-Type: multipart/mixed; boundary=test\r\n\r\n" +
		"--test\r\nContent-Type: text/plain\r\n\r\nPlease review the license.\r\n" +
		"--test\r\nContent-Type: text/plain; name=license.txt\r\nContent-Disposition: attachment; filename=license.txt\r\n\r\nLicense number 123\r\n" +
		"--test--\r\n"
	message, err := mail.ReadMessage(bytes.NewBufferString(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, attachments, err := extractEvidenceParts(message.Header, message.Body, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "review the license") || len(attachments) != 1 || attachments[0].Filename != "license.txt" || !strings.Contains(string(attachments[0].Content), "123") {
		t.Fatalf("body=%q attachments=%#v", body, attachments)
	}
}

func TestSettingsRequireEncryptedTransportAndCredentials(t *testing.T) {
	valid := SettingsInput{Name: "Office", EmailAddress: "office@example.com", IMAPHost: "imap.example.com", IMAPPort: 993, IMAPSecurity: SecurityTLS, IMAPUsername: "office@example.com", IMAPPassword: "secret", SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPSecurity: SecurityStartTLS, SMTPUsername: "office@example.com", SMTPPassword: "secret"}
	if _, err := valid.integration(); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	valid.SMTPSecurity = "none"
	if _, err := valid.integration(); err == nil {
		t.Fatal("unencrypted SMTP was accepted")
	}
}

func TestParseRecipientListAndBuildMessage(t *testing.T) {
	recipients, err := ParseRecipientList(`"Sam Example" <sam@example.com>, office@example.com`)
	if err != nil || len(recipients) != 2 {
		t.Fatalf("ParseRecipientList() = %#v, %v", recipients, err)
	}
	raw := string(buildRFC5322(Integration{EmailAddress: "dispatch@example.com", DisplayName: "Example Trades"}, OutgoingMessage{To: recipients, Subject: "Schedule update", Body: "Hello\nWorld"}, "<test@example.com>"))
	for _, expected := range []string{`From: "Example Trades" <dispatch@example.com>`, `To: "Sam Example" <sam@example.com>, <office@example.com>`, "Subject: Schedule update", "Hello\r\nWorld"} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("message missing %q:\n%s", expected, raw)
		}
	}
}

func TestExtractReadableMultipartBody(t *testing.T) {
	raw := "Content-Type: multipart/alternative; boundary=demo\r\n\r\n--demo\r\nContent-Type: text/plain\r\n\r\nPlain text wins.\r\n--demo\r\nContent-Type: text/html\r\n\r\n<b>HTML</b>\r\n--demo--\r\n"
	message, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := extractReadableBody(message.Header, io.Reader(message.Body), 0)
	if err != nil || body != "Plain text wins." {
		t.Fatalf("body = %q, %v", body, err)
	}
}
