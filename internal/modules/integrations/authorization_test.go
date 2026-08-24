package integrations

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func authorizationFixture(t *testing.T, now time.Time) AuthorizationSession {
	t.Helper()
	value, err := NewAuthorizationSession(AuthorizationSessionInput{
		ID:                 ids.IntegrationAuthorizationSessionID(integrationTestID(t, "authorization")),
		AccountID:          ids.AccountID(integrationTestID(t, "account")),
		ConnectionID:       ids.IntegrationConnectionID(integrationTestID(t, "drive-connection")),
		ConnectionRevision: ids.IntegrationConnectionRevisionID(integrationTestID(t, "drive-revision")),
		Provider:           GoogleOAuthProvider, Scope: GoogleDriveReadScope,
		ScopeRevisionSHA256: sha256.Sum256([]byte("drive-revision-scope")),
		RedirectURI:         "https://app.infiniteocean.net/api/v1/accounts/authorization/callback",
		StateSHA256:         sha256.Sum256([]byte("one-use-state")), PKCEChallengeSHA256: sha256.Sum256([]byte("pkce-verifier")),
		CreatedBy: integrationActor(t), CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}, accounts.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAuthorizationSessionBindsOneUseStateAndCredentialGeneration(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	pending := authorizationFixture(t, now)
	state := sha256.Sum256([]byte("one-use-state"))
	exchanging, err := pending.BeginExchange(state, 1, now.Add(time.Minute))
	if err != nil || exchanging.Status != AuthorizationExchanging || exchanging.ClaimedAt == nil || exchanging.Version != 2 {
		t.Fatalf("exchanging=%+v err=%v", exchanging, err)
	}
	if _, err := exchanging.BeginExchange(state, 2, now.Add(2*time.Minute)); !errors.Is(err, ErrState) {
		t.Fatalf("replayed state error=%v", err)
	}
	credentialID := ids.IntegrationCredentialID(integrationTestID(t, "drive-credential"))
	completed, err := exchanging.Complete(credentialID, 3, 2, now.Add(2*time.Minute))
	if err != nil || completed.Status != AuthorizationCompleted || completed.CredentialID != credentialID || completed.CredentialGeneration != 3 || completed.Version != 3 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err := pending.BeginExchange(sha256.Sum256([]byte("wrong-state")), 1, now.Add(time.Minute)); !errors.Is(err, ErrState) {
		t.Fatalf("wrong state error=%v", err)
	}
}

func TestAuthorizationSessionExpiresAndFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	pending := authorizationFixture(t, now)
	expired, err := pending.BeginExchange(pending.StateSHA256, 1, pending.ExpiresAt)
	if err != nil || expired.Status != AuthorizationExpired || expired.ErrorCode != "authorization_expired" {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	exchanging, err := pending.BeginExchange(pending.StateSHA256, 1, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := exchanging.Fail("provider_scope_mismatch", 2, now.Add(2*time.Minute))
	if err != nil || failed.Status != AuthorizationFailed || failed.ErrorCode != "provider_scope_mismatch" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestAuthorizationSessionRejectsOpenRedirectAndNonManager(t *testing.T) {
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	base := AuthorizationSessionInput{
		ID: ids.IntegrationAuthorizationSessionID(integrationTestID(t, "authorization-invalid")), AccountID: ids.AccountID(integrationTestID(t, "account")),
		ConnectionID: ids.IntegrationConnectionID(integrationTestID(t, "drive-connection")), ConnectionRevision: ids.IntegrationConnectionRevisionID(integrationTestID(t, "drive-revision")),
		Provider: GoogleOAuthProvider, Scope: GoogleDriveReadScope, ScopeRevisionSHA256: sha256.Sum256([]byte("scope")),
		RedirectURI: "https://app.infiniteocean.net/callback", StateSHA256: sha256.Sum256([]byte("state")), PKCEChallengeSHA256: sha256.Sum256([]byte("pkce")),
		CreatedBy: integrationActor(t), CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	if _, err := NewAuthorizationSession(base, accounts.RoleMember); !errors.Is(err, ErrRole) {
		t.Fatalf("member begin error=%v", err)
	}
	for _, redirect := range []string{"https://user@example.com/callback", "https://example.com/callback?return=https://evil.example", "http://example.com/callback", "https://example.com/a/../callback"} {
		base.RedirectURI = redirect
		if _, err := NewAuthorizationSession(base, accounts.RoleOwner); !errors.Is(err, ErrInvalid) {
			t.Errorf("redirect %q error=%v", redirect, err)
		}
	}
	base.RedirectURI = "http://127.0.0.1:8080/callback"
	if _, err := NewAuthorizationSession(base, accounts.RoleOwner); err != nil {
		t.Fatalf("local callback error=%v", err)
	}
}
