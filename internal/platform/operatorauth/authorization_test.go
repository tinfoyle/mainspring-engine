package operatorauth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/operatorauth"
)

func TestAuthorizationBindsPhishingResistantProofToExactOperation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	request := operatorauth.Request{
		VerifyKeys: map[string][]byte{"operator-2026-08": publicKey}, Issuer: "https://operators.infiniteocean.net",
		Actor: "platform-admin@example.com", Action: "catalog-admin:publish", Environment: "production",
		Reason: "Publish reviewed catalog 42", Scope: "catalog-version=42", Now: now,
	}
	token := sign(t, privateKey, "operator-2026-08", map[string]any{
		"version": 1, "issuer": request.Issuer, "authorization_id": "10000000-0000-4000-8000-000000000001",
		"actor": request.Actor, "action": request.Action, "environment": request.Environment,
		"reason_sha256": operatorauth.Digest(request.Reason), "scope_sha256": operatorauth.Digest(request.Scope),
		"assurance": operatorauth.Assurance, "mode": operatorauth.ModeStandard,
		"issued_at": now.Add(-time.Minute).Unix(), "expires_at": now.Add(4 * time.Minute).Unix(),
	})
	request.Token = token
	evidence, err := operatorauth.Verify(request)
	if err != nil || evidence.AuthorizationID == "" || evidence.Mode != operatorauth.ModeStandard || evidence.KeyID != "operator-2026-08" {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	for name, mutate := range map[string]func(*operatorauth.Request){
		"actor":       func(value *operatorauth.Request) { value.Actor = "other@example.com" },
		"action":      func(value *operatorauth.Request) { value.Action = "catalog-admin:retire" },
		"environment": func(value *operatorauth.Request) { value.Environment = "staging" },
		"reason":      func(value *operatorauth.Request) { value.Reason = "Different reason" },
		"scope":       func(value *operatorauth.Request) { value.Scope = "catalog-version=43" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			if _, err := operatorauth.Verify(changed); !errors.Is(err, operatorauth.ErrInvalid) {
				t.Fatalf("changed request error=%v", err)
			}
		})
	}
}

func TestBreakGlassRequiresExplicitEnablementIncidentAndTwoIndependentApprovers(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	request := operatorauth.Request{
		VerifyKeys: map[string][]byte{"break-glass-1": publicKey}, Issuer: "https://operators.infiniteocean.net",
		Actor: "responder@example.com", Action: "account-erasure-admin:restore-replay", Environment: "production",
		Reason: "Restore incident IO-4821", Scope: "request=100;cell=cell-a", Now: now,
	}
	claims := map[string]any{
		"version": 1, "issuer": request.Issuer, "authorization_id": "20000000-0000-4000-8000-000000000002",
		"actor": request.Actor, "action": request.Action, "environment": request.Environment,
		"reason_sha256": operatorauth.Digest(request.Reason), "scope_sha256": operatorauth.Digest(request.Scope),
		"assurance": operatorauth.Assurance, "mode": operatorauth.ModeBreakGlass, "incident_id": "IO-4821",
		"approvers": []string{"security-lead@example.com", "operations-lead@example.com"},
		"issued_at": now.Add(-time.Minute).Unix(), "expires_at": now.Add(4 * time.Minute).Unix(),
	}
	request.Token = sign(t, privateKey, "break-glass-1", claims)
	if _, err := operatorauth.Verify(request); !errors.Is(err, operatorauth.ErrInvalid) {
		t.Fatalf("disabled break glass error=%v", err)
	}
	request.AllowBreakGlass = true
	if evidence, err := operatorauth.Verify(request); err != nil || evidence.IncidentID != "IO-4821" || len(evidence.Approvers) != 2 {
		t.Fatalf("break-glass evidence=%+v err=%v", evidence, err)
	}
	claims["approvers"] = []string{request.Actor, "security-lead@example.com"}
	request.Token = sign(t, privateKey, "break-glass-1", claims)
	if _, err := operatorauth.Verify(request); !errors.Is(err, operatorauth.ErrInvalid) {
		t.Fatalf("self-approved break glass error=%v", err)
	}
}

func TestAuthorizationRejectsTamperingUnknownFieldsAndUnsafeLifetime(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	request := operatorauth.Request{VerifyKeys: map[string][]byte{"k1": publicKey}, Issuer: "issuer", Actor: "actor", Action: "mode:action", Environment: "production", Reason: "reason", Scope: "scope", Now: now}
	base := map[string]any{
		"version": 1, "issuer": "issuer", "authorization_id": "30000000-0000-4000-8000-000000000003", "actor": "actor",
		"action": "mode:action", "environment": "production", "reason_sha256": operatorauth.Digest("reason"),
		"scope_sha256": operatorauth.Digest("scope"), "assurance": operatorauth.Assurance, "mode": operatorauth.ModeStandard,
		"issued_at": now.Unix(), "expires_at": now.Add(5 * time.Minute).Unix(),
	}
	base["unexpected"] = true
	request.Token = sign(t, privateKey, "k1", base)
	if _, err := operatorauth.Verify(request); !errors.Is(err, operatorauth.ErrInvalid) {
		t.Fatalf("unknown field error=%v", err)
	}
	delete(base, "unexpected")
	base["expires_at"] = now.Add(operatorauth.MaximumLifetime + time.Second).Unix()
	request.Token = sign(t, privateKey, "k1", base)
	if _, err := operatorauth.Verify(request); !errors.Is(err, operatorauth.ErrInvalid) {
		t.Fatalf("unsafe lifetime error=%v", err)
	}
	base["expires_at"] = now.Add(5 * time.Minute).Unix()
	request.Token = sign(t, privateKey, "k1", base)
	parts := strings.Split(request.Token, ".")
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	signature[0] ^= 1
	request.Token = parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(signature)
	if _, err := operatorauth.Verify(request); !errors.Is(err, operatorauth.ErrInvalid) {
		t.Fatalf("tampered signature error=%v", err)
	}
}

func sign(t *testing.T, privateKey ed25519.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()
	headerBytes, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": operatorauth.Type, "kid": keyID})
	claimsBytes, _ := json.Marshal(claims)
	header := base64.RawURLEncoding.EncodeToString(headerBytes)
	payload := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signature := ed25519.Sign(privateKey, []byte(header+"."+payload))
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(signature)
}
