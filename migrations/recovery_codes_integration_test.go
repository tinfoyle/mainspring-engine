package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type integrationRecoveryCodes struct{ next int }

func (g *integrationRecoveryCodes) Generate() (string, error) {
	g.next++
	return fmt.Sprintf("%032x", g.next), nil
}

type allowPasskeyNetwork struct{}

func (allowPasskeyNetwork) Allow(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return true, nil
}

func TestPostgresRecoveryCodesAreSingleUseSessionBoundAndReplaceLostPasskey(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	for _, target := range []migrations.Target{migrations.Global, migrations.Development} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	now := time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC)
	userID := ids.UserID("fa100000-0000-4000-8000-000000000001")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'factor-recovery@example.com','Factor Recovery Owner','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatal(err)
	}
	postureService, err := securityposture.NewService(postgresadapter.NewSecurityPostureRepository(pool))
	if err != nil {
		t.Fatal(err)
	}
	initialPosture, err := postureService.Status(ctx, userID)
	if err != nil || initialPosture.PasskeyCount != 0 || initialPosture.RecoveryCodesConfigured || initialPosture.OwnerReady {
		t.Fatalf("initial security posture=%+v err=%v", initialPosture, err)
	}
	sessionIDs := &erasureIDs{values: []string{
		"fa200000-0000-4000-8000-000000000001", "fa200000-0000-4000-8000-000000000002",
		"fa200000-0000-4000-8000-000000000003", "fa200000-0000-4000-8000-000000000004",
	}}
	sessionService, _ := sessions.NewService(postgresadapter.NewSessionRepository(pool), sessionIDs, fixedClock{now: now}, 24*time.Hour, time.Hour, 15*time.Minute)
	passwordOne, _ := sessionService.IssueForClientWithMethod(ctx, userID, 1, "password one", sessions.AuthenticationMethodPassword)
	passwordTwo, _ := sessionService.IssueForClientWithMethod(ctx, userID, 1, "password two", sessions.AuthenticationMethodPassword)
	passwordThree, _ := sessionService.IssueForClientWithMethod(ctx, userID, 1, "password three", sessions.AuthenticationMethodPassword)
	passkeySession, _ := sessionService.IssueForClientWithMethod(ctx, userID, 1, "existing passkey", sessions.AuthenticationMethodPasskey)

	repository := postgresadapter.NewRecoveryCodeRepository(pool)
	codes := &integrationRecoveryCodes{}
	recoveryService, _ := recoverycodes.NewService(repository, &erasureIDs{values: []string{
		"fa300000-0000-4000-8000-000000000001", "fa300000-0000-4000-8000-000000000002",
	}}, codes, fixedClock{now: now})
	first, err := recoveryService.Rotate(ctx, passkeySession.Session)
	if err != nil || first.Status.Version != 1 || first.Status.Remaining != recoverycodes.CodeCount {
		t.Fatalf("first rotation=%+v err=%v", first, err)
	}
	codeOnlyPosture, err := postureService.Status(ctx, userID)
	if err != nil || codeOnlyPosture.PasskeyCount != 0 || !codeOnlyPosture.RecoveryCodesConfigured || codeOnlyPosture.RecoveryCodesRemaining != recoverycodes.CodeCount || codeOnlyPosture.OwnerReady {
		t.Fatalf("code-only security posture=%+v err=%v", codeOnlyPosture, err)
	}
	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT encode(code_hash,'hex') FROM user_recovery_codes ORDER BY position LIMIT 1`).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(first.Codes[0], "-", "") == storedHash {
		t.Fatal("recovery code was stored reversibly")
	}
	if err := recoveryService.Consume(ctx, passwordOne.Session, first.Codes[0]); err != nil {
		t.Fatal(err)
	}
	if err := recoveryService.Consume(ctx, passwordOne.Session, first.Codes[0]); !errors.Is(err, recoverycodes.ErrInvalidCode) {
		t.Fatalf("replayed code=%v", err)
	}
	grantedOne, _ := recoveryService.Granted(ctx, passwordOne.Session)
	grantedTwo, _ := recoveryService.Granted(ctx, passwordTwo.Session)
	if !grantedOne || grantedTwo {
		t.Fatalf("session-bound grants one=%v two=%v", grantedOne, grantedTwo)
	}

	second, err := recoveryService.Rotate(ctx, passkeySession.Session)
	if err != nil || second.Status.Version != 2 {
		t.Fatalf("second rotation=%+v err=%v", second, err)
	}
	grantedOne, _ = recoveryService.Granted(ctx, passwordOne.Session)
	if grantedOne {
		t.Fatal("rotating recovery codes retained an old replacement grant")
	}
	passkeyCipher, _ := passkeys.NewCipher(make([]byte, 32), 1)
	passkeyRepository, _ := postgresadapter.NewPasskeyRepository(pool, passkeyCipher)
	if _, err := passkeyRepository.EnsureUser(ctx, userID, make([]byte, 32), now); err != nil {
		t.Fatal(err)
	}
	passkeyService, err := passkeys.NewService(passkeyRepository, sessionService, allowPasskeyNetwork{}, &erasureIDs{values: []string{"fa400000-0000-4000-8000-000000000001", "fa400000-0000-4000-8000-000000000002"}}, fixedClock{now: now}, passkeys.Config{RelyingPartyID: "example.com", Origins: []string{"https://example.com"}, RecoveryPolicy: recoveryService})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := passkeyService.BeginRegistration(ctx, passwordOne.Session); !errors.Is(err, passkeys.ErrRecoveryCodeRequired) {
		t.Fatalf("lost-factor registration without code=%v", err)
	}
	if err := recoveryService.Consume(ctx, passwordOne.Session, second.Codes[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := passkeyService.BeginRegistration(ctx, passwordOne.Session); err != nil {
		t.Fatalf("replacement registration after code grant=%v", err)
	}

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, session := range []sessions.Session{passwordTwo.Session, passwordThree.Session} {
		wait.Add(1)
		go func(value sessions.Session) {
			defer wait.Done()
			results <- recoveryService.Consume(ctx, value, second.Codes[2])
		}(session)
	}
	wait.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, recoverycodes.ErrInvalidCode) {
			rejected++
		} else {
			t.Fatalf("concurrent consumption=%v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent consumption succeeded=%d rejected=%d", succeeded, rejected)
	}
	for index, credentialID := range [][]byte{[]byte("concurrent-passkey-a"), []byte("concurrent-passkey-b")} {
		if err := passkeyRepository.CreateCredential(ctx, userID, fmt.Sprintf("Concurrent key %d", index+1), webauthn.Credential{ID: credentialID, PublicKey: []byte{1}}, now, passkeys.MaximumPasskeys); err != nil {
			t.Fatal(err)
		}
	}
	type deletion struct {
		deleted bool
		err     error
	}
	deletions := make(chan deletion, 2)
	for _, credentialID := range [][]byte{[]byte("concurrent-passkey-a"), []byte("concurrent-passkey-b")} {
		wait.Add(1)
		go func(value []byte) {
			defer wait.Done()
			deleted, err := passkeyRepository.DeleteCredential(ctx, userID, value, false, now)
			deletions <- deletion{deleted: deleted, err: err}
		}(credentialID)
	}
	wait.Wait()
	close(deletions)
	deleted, protected := 0, 0
	for result := range deletions {
		if result.deleted && result.err == nil {
			deleted++
		} else if !result.deleted && errors.Is(result.err, passkeys.ErrRecoveryCodesRequired) {
			protected++
		} else {
			t.Fatalf("concurrent passkey deletion=%+v", result)
		}
	}
	if deleted != 1 || protected != 1 {
		t.Fatalf("concurrent passkey deletion deleted=%d protected=%d", deleted, protected)
	}
	var remainingPasskeys int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_credentials WHERE user_id=$1`, userID).Scan(&remainingPasskeys); err != nil || remainingPasskeys != 1 {
		t.Fatalf("remaining passkeys=%d err=%v", remainingPasskeys, err)
	}
	readyPosture, err := postureService.Status(ctx, userID)
	if err != nil || readyPosture.PasskeyCount != 1 || !readyPosture.RecoveryCodesConfigured || readyPosture.RecoveryCodesRemaining != 8 || !readyPosture.OwnerReady {
		t.Fatalf("ready security posture=%+v err=%v", readyPosture, err)
	}
	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_security_events WHERE user_id=$1 AND event_type IN ('recovery_codes_rotated','recovery_code_consumed')`, userID).Scan(&eventCount); err != nil || eventCount != 5 {
		t.Fatalf("recovery security events=%d err=%v", eventCount, err)
	}
}
