package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestMCPOAuthCodeRotationAudienceAndIdentityInvalidation(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 22, 23, 0, 0, 0, time.UTC)
	userID := ids.UserID("11000000-0000-4000-8000-000000000001")
	sessionID := ids.SessionID("12000000-0000-4000-8000-000000000001")
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at)
		VALUES ($1,'oauth@example.test','OAuth User','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions(id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,expires_at,client_label,authentication_method,reauthentication_method)
		VALUES ($2,$1,decode(repeat('11',32),'hex'),1,$3::timestamptz,$3::timestamptz,$3::timestamptz,$3::timestamptz,$3::timestamptz+interval '1 hour','integration','password','password')`, userID, sessionID, now); err != nil {
		t.Fatal(err)
	}

	const (
		issuer      = "https://app.infiniteocean.net"
		resource    = "https://mcp.infiniteocean.net"
		clientID    = "https://client.example/oauth/metadata.json"
		redirectURI = "http://127.0.0.1:8765/callback"
		verifier    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	)
	verifierDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
	repository := postgresadapter.NewMCPAuthRepository(pool)
	service, err := mcpauth.New(repository, ids.RandomGenerator{}, mcpauth.RandomSecrets{}, fixedClock{now: now}, issuer, resource)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := mcpauth.New(postgresadapter.NewMCPAuthRepository(pool), ids.RandomGenerator{}, mcpauth.RandomSecrets{}, fixedClock{now: now}, issuer, resource)
	if err != nil {
		t.Fatal(err)
	}

	issue := func(state string) mcpauth.TokenSet {
		t.Helper()
		pending, beginErr := service.Begin(ctx, mcpauth.AuthorizationCommand{
			UserID: userID, SessionID: sessionID,
			Client:      mcpauth.Client{ID: clientID, Name: "Integration Client", RedirectURIs: []string{redirectURI}},
			RedirectURI: redirectURI, Resource: resource, Scope: mcpauth.ScopeMCP,
			State: state, CodeChallenge: challenge,
		})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		decision, decideErr := service.Decide(ctx, mcpauth.AuthorizationDecision{PendingID: pending.ID, UserID: userID, SessionID: sessionID, Approve: true})
		if decideErr != nil || !decision.Approved || decision.Code == "" {
			t.Fatalf("decision = %+v, %v", decision, decideErr)
		}
		tokens, exchangeErr := service.ExchangeCode(ctx, decision.Code, clientID, redirectURI, resource, verifier)
		if exchangeErr != nil {
			t.Fatal(exchangeErr)
		}
		if _, replayErr := service.ExchangeCode(ctx, decision.Code, clientID, redirectURI, resource, verifier); !errors.Is(replayErr, mcpauth.ErrConsumed) {
			t.Fatalf("authorization-code replay = %v", replayErr)
		}
		return tokens
	}

	initial := issue("first")
	if actor, authErr := replica.Authenticate(ctx, initial.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); authErr != nil || actor.UserID != userID {
		t.Fatalf("access authentication = %+v, %v", actor, authErr)
	}
	if _, authErr := service.Authenticate(ctx, initial.AccessToken, mcpauth.TokenRequirement{Audience: "https://other.example", Scope: mcpauth.ScopeMCP}); !errors.Is(authErr, mcpauth.ErrAccessDenied) {
		t.Fatalf("wrong audience = %v", authErr)
	}

	rotated, err := replica.Refresh(ctx, initial.RefreshToken, clientID, resource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, rotated.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Refresh(ctx, initial.RefreshToken, clientID, resource); !errors.Is(err, mcpauth.ErrRefreshReuse) {
		t.Fatalf("refresh replay = %v", err)
	}
	if _, err = service.Authenticate(ctx, rotated.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("access after family replay = %v", err)
	}
	revoked := issue("revocation")
	if err = service.Revoke(ctx, revoked.AccessToken, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, revoked.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("access after revocation = %v", err)
	}
	if _, err = service.Refresh(ctx, revoked.RefreshToken, clientID, resource); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("refresh after related-token revocation = %v", err)
	}

	managed := issue("grant-management")
	grants, err := replica.Grants(ctx, userID)
	if err != nil || len(grants) != 1 || grants[0].ClientID != clientID || grants[0].ClientName != "Integration Client" {
		t.Fatalf("active grants = %+v, %v", grants, err)
	}
	grantRevoked, err := service.RevokeGrant(ctx, userID, grants[0].ID)
	if err != nil || !grantRevoked {
		t.Fatalf("grant revocation = %t, %v", grantRevoked, err)
	}
	if _, err = replica.Authenticate(ctx, managed.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("access after grant revocation = %v", err)
	}
	if _, err = service.Refresh(ctx, managed.RefreshToken, clientID, resource); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("refresh after grant revocation = %v", err)
	}
	if grants, err = service.Grants(ctx, userID); err != nil || len(grants) != 0 {
		t.Fatalf("active grants after revocation = %+v, %v", grants, err)
	}

	identityBound := issue("identity-version")
	if _, err = pool.Exec(ctx, `UPDATE users SET security_version=security_version+1 WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(ctx, identityBound.AccessToken, mcpauth.TokenRequirement{Audience: resource, Scope: mcpauth.ScopeMCP}); !errors.Is(err, mcpauth.ErrAccessDenied) {
		t.Fatalf("access after identity version change = %v", err)
	}

	var malformedHashes int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM mcp_oauth_codes WHERE octet_length(code_hash)<>32) +
		  (SELECT count(*) FROM mcp_oauth_access_tokens WHERE octet_length(token_hash)<>32) +
		  (SELECT count(*) FROM mcp_oauth_refresh_tokens WHERE octet_length(token_hash)<>32)`).Scan(&malformedHashes); err != nil || malformedHashes != 0 {
		t.Fatalf("persisted credential digests = %d, %v", malformedHashes, err)
	}
}
