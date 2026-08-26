package smtp

import (
	"context"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
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
	base.Username = ""
	base.RootCAFile = "/definitely/missing/spyglass-smtp-ca.crt"
	if _, err := New(base); err == nil {
		t.Fatal("expected missing custom root CA rejection")
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

func TestVerificationLinkCarriesOfferAndSameOriginReturnTarget(t *testing.T) {
	returnTo := "/app/checkout?offer=team-monthly-v1&ref=IO-PARTNER1"
	link := verificationLink("https://app.infiniteocean.net", registration.VerificationMessage{Token: "verification+token", OfferCode: "team-monthly-v1", ReturnTo: returnTo})
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "app.infiniteocean.net" || parsed.Path != "/verify" || parsed.Query().Get("token") != "verification+token" || parsed.Query().Get("offer") != "team-monthly-v1" || parsed.Query().Get("return_to") != returnTo {
		t.Fatalf("verification link = %q, parsed=%+v err=%v", link, parsed, err)
	}
}

func TestContactChangeContentIsActionSpecificEscapedAndDoesNotLeakToken(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	token := "mailbox-token+with/specials"
	tests := []struct {
		action       contactchange.Action
		expect       string
		expectToken  bool
		expectLogout bool
	}{
		{action: contactchange.ActionVerifyNew, expect: "/contact-change/verify?token=mailbox-token%2Bwith%2Fspecials", expectToken: true},
		{action: contactchange.ActionRequested, expect: "/app/security", expectLogout: false},
		{action: contactchange.ActionCompleted, expect: "Every existing session was signed out", expectLogout: true},
	}
	for _, test := range tests {
		message := contactchange.Message{Action: test.action, Email: "new@example.com", DisplayName: "Avery <Owner>", OldEmail: "old&contact@example.com", NewEmail: "new<contact@example.com", Token: token, ExpiresAt: now.Add(30 * time.Minute), OccurredAt: now}
		subject, plain, htmlBody, err := contactChangeContent("https://app.infiniteocean.net", message)
		if err != nil || subject == "" || !strings.Contains(plain, test.expect) || !strings.Contains(htmlBody, "Avery &lt;Owner&gt;") || strings.Contains(htmlBody, "new<contact@example.com") {
			t.Fatalf("action=%s subject=%q plain=%q html=%q err=%v", test.action, subject, plain, htmlBody, err)
		}
		encodedToken := "mailbox-token%2Bwith%2Fspecials"
		containsToken := strings.Contains(plain, encodedToken) && strings.Contains(htmlBody, encodedToken)
		if containsToken != test.expectToken || (!test.expectToken && (strings.Contains(plain, token) || strings.Contains(htmlBody, token))) {
			t.Fatalf("action=%s token handling is unsafe: plain=%q html=%q", test.action, plain, htmlBody)
		}
		if test.expectLogout && !strings.Contains(plain, "signed out") {
			t.Fatalf("action=%s omitted session revocation notice: %q", test.action, plain)
		}
	}
	if _, _, _, err := contactChangeContent("https://app.infiniteocean.net", contactchange.Message{}); err == nil {
		t.Fatal("invalid contact-change action accepted")
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
