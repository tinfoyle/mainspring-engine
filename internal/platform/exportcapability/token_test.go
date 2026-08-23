package exportcapability

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestCapabilityRoundTripAndStateBinding(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	key := []byte(strings.Repeat("k", MinimumKeyBytes))
	signer, err := NewSigner("https://app.stage.infiniteocean.net", "1", key, DefaultLifetime, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("https://app.stage.infiniteocean.net", map[string][]byte{"1": key}, MaximumLifetime, time.Second, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	authority := Authority{AccountID: "10000000-0000-4000-8000-000000000001", UserID: "20000000-0000-4000-8000-000000000002", ExportID: "30000000-0000-4000-8000-000000000003", RequestVersion: 4, ArtifactSHA256: strings.Repeat("ab", 32), ArtifactBytes: 4096}
	token, expiresAt, err := signer.Issue(authority, now.Add(time.Minute))
	if err != nil || !expiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("issue: expires=%s err=%v", expiresAt, err)
	}
	claims, err := verifier.Verify(token)
	if err != nil || claims.Authority != authority || claims.KeyID != "1" {
		t.Fatalf("verify: claims=%+v err=%v", claims, err)
	}
	parts := strings.Split(token, ".")
	replacement := "A"
	if strings.HasSuffix(parts[1], replacement) {
		replacement = "B"
	}
	parts[1] = parts[1][:len(parts[1])-1] + replacement
	if _, err := verifier.Verify(strings.Join(parts, ".")); !errors.Is(err, ErrSignature) {
		t.Fatalf("tamper: %v", err)
	}
}

func TestCapabilityRejectsExpiryAndUnknownKey(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	key := []byte(strings.Repeat("k", MinimumKeyBytes))
	signer, _ := NewSigner("https://app.example.test", "old", key, time.Minute, fixedClock{now})
	authority := Authority{AccountID: "10000000-0000-4000-8000-000000000001", UserID: "20000000-0000-4000-8000-000000000002", ExportID: "30000000-0000-4000-8000-000000000003", RequestVersion: 1, ArtifactSHA256: strings.Repeat("01", 32), ArtifactBytes: 1}
	token, _, err := signer.Issue(authority, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	unknown, _ := NewVerifier("https://app.example.test", map[string][]byte{"new": []byte(strings.Repeat("n", MinimumKeyBytes))}, MaximumLifetime, 0, fixedClock{now})
	if _, err := unknown.Verify(token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	expired, _ := NewVerifier("https://app.example.test", map[string][]byte{"old": key}, MaximumLifetime, 0, fixedClock{now.Add(time.Minute)})
	if _, err := expired.Verify(token); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}
