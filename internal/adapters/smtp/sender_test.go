package smtp

import (
	"context"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
)

func TestNewRequiresExactHTTPSOriginAndCompleteCredentials(t *testing.T) {
	base := Config{Address: "smtp.example.com:465", ServerName: "smtp.example.com", FromAddress: "hello@infiniteocean.net", AppOrigin: "https://app.infiniteocean.net"}
	if _, err := New(base); err != nil {
		t.Fatal(err)
	}
	base.AppOrigin = "http://app.infiniteocean.net"
	if _, err := New(base); err == nil {
		t.Fatal("expected insecure origin rejection")
	}
	base.AppOrigin = "https://app.infiniteocean.net"
	base.Username = "user"
	if _, err := New(base); err == nil {
		t.Fatal("expected incomplete auth rejection")
	}
}

func TestOwnershipTransferContentIsRoleSpecificAndEscaped(t *testing.T) {
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	for _, role := range []accountmembers.OwnershipNoticeRole{accountmembers.OwnershipNoticePreviousOwner, accountmembers.OwnershipNoticeNewOwner} {
		message := accountmembers.OwnershipTransferNotice{Email: "owner@example.com", DisplayName: "Avery <Owner>", AccountName: "Northstar & Co", CounterpartDisplayName: "Morgan <Lee>", RecipientRole: role, OccurredAt: now}
		subject, plain, htmlBody, err := ownershipTransferContent("https://app.infiniteocean.net", message)
		if err != nil || !strings.Contains(subject, "Northstar & Co") || !strings.Contains(plain, "https://app.infiniteocean.net/app#settings") || !strings.Contains(htmlBody, "Northstar &amp; Co") || strings.Contains(htmlBody, "Morgan <Lee>") {
			t.Fatalf("role=%s subject=%q plain=%q html=%q err=%v", role, subject, plain, htmlBody, err)
		}
	}
	if _, _, _, err := ownershipTransferContent("https://app.infiniteocean.net", accountmembers.OwnershipTransferNotice{}); err == nil {
		t.Fatal("invalid ownership recipient role accepted")
	}
}

func TestSendRejectsHeaderInjectionBeforeNetwork(t *testing.T) {
	sender, err := New(Config{Address: "smtp.invalid:465", ServerName: "smtp.invalid", FromAddress: "hello@infiniteocean.net", AppOrigin: "https://app.infiniteocean.net"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.send(context.Background(), "owner@example.com", "Subject\r\nBcc: attacker@example.com", "plain", "html"); err == nil {
		t.Fatal("expected subject injection rejection")
	}
}

func TestMessageBodyContainsMultipartContentWithoutHeaderInjection(t *testing.T) {
	body, err := messageBody(mail.Address{Name: "Infinite Ocean", Address: "hello@infiniteocean.net"}, mail.Address{Address: "owner@example.com"}, "Verify identity", "plain link", "<p>html link</p>")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"multipart/alternative", "spyglass_", "plain link", "<p>html link</p>", "From: \"Infinite Ocean\" <hello@infiniteocean.net>"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("message missing %q", expected)
		}
	}
	if strings.Contains(body, "\n\n") && !strings.Contains(body, "\r\n\r\n") {
		t.Fatal("message must use SMTP CRLF framing")
	}
}
