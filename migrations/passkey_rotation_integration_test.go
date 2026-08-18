package migrations_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresPasskeyEncryptionRotationRetiresOldKey(t *testing.T) {
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
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("ea100000-0000-4000-8000-000000000001")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'rotation@example.com','Rotation Owner','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatal(err)
	}
	sessionService, err := sessions.NewService(postgresadapter.NewSessionRepository(pool), &erasureIDs{values: []string{"ea200000-0000-4000-8000-000000000001"}}, fixedClock{now: now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessionService.Issue(ctx, userID, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	newKey := bytes.Repeat([]byte{0x42}, 32)
	oldCipher, _ := passkeys.NewCipher(oldKey, 1)
	oldRepository, _ := postgresadapter.NewPasskeyRepository(pool, oldCipher)
	if _, err := oldRepository.EnsureUser(ctx, userID, bytes.Repeat([]byte{0x55}, 32), now); err != nil {
		t.Fatal(err)
	}
	credentialID := []byte("rotation-credential")
	if err := oldRepository.CreateCredential(ctx, userID, "Old key credential", webauthn.Credential{ID: credentialID, Authenticator: webauthn.Authenticator{SignCount: 7}}, now, passkeys.MaximumPasskeys); err != nil {
		t.Fatal(err)
	}
	ceremony := passkeys.Ceremony{ID: "ea300000-0000-4000-8000-000000000001", Kind: passkeys.CeremonyRegistration, UserID: userID, SessionID: issued.Session.ID, Data: webauthn.SessionData{Challenge: "rotation-challenge"}, CreatedAt: now, ExpiresAt: now.Add(passkeys.CeremonyTTL)}
	if err := oldRepository.CreateCeremony(ctx, ceremony); err != nil {
		t.Fatal(err)
	}

	keyring, err := passkeys.NewCipherKeyring(map[int][]byte{1: oldKey, 2: newKey}, 2)
	if err != nil {
		t.Fatal(err)
	}
	rotatingRepository, _ := postgresadapter.NewPasskeyRepository(pool, keyring)
	rotation, _ := passkeys.NewRotationService(rotatingRepository, fixedClock{now: now.Add(time.Minute)})
	before, err := rotation.Inspect(ctx, "security@example.com", "inspect passkey rotation fixture", "test")
	if err != nil || before.ActiveVersion != 2 || versionCount(before.CredentialVersions, 1) != 1 || versionCount(before.CeremonyVersions, 1) != 1 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	result, err := rotation.Reencrypt(ctx, 2, "security@example.com", "reencrypt passkey rotation fixture", "test")
	if err != nil || result.Updated != 2 || versionCount(result.CredentialVersions, 2) != 1 || versionCount(result.CeremonyVersions, 2) != 1 {
		t.Fatalf("rotation=%+v err=%v", result, err)
	}

	newOnlyCipher, _ := passkeys.NewCipher(newKey, 2)
	newOnlyRepository, _ := postgresadapter.NewPasskeyRepository(pool, newOnlyCipher)
	loaded, err := newOnlyRepository.User(ctx, userID)
	if err != nil || len(loaded.Credentials) != 1 || !bytes.Equal(loaded.Credentials[0].Credential.ID, credentialID) {
		t.Fatalf("new-key credential=%+v err=%v", loaded.Credentials, err)
	}
	consumed, err := newOnlyRepository.ConsumeCeremony(ctx, ceremony.ID, ceremony.Kind, userID, issued.Session.ID, now.Add(2*time.Minute))
	if err != nil || consumed.Data.Challenge != ceremony.Data.Challenge {
		t.Fatalf("new-key ceremony=%+v err=%v", consumed, err)
	}
	if _, err := oldRepository.User(ctx, userID); err == nil {
		t.Fatal("retired old key decrypted a re-encrypted credential")
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_key_rotation_operator_events`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("rotation audit count=%d err=%v", auditCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE passkey_key_rotation_operator_events SET reason='tampered reason'`); err == nil {
		t.Fatal("passkey rotation operator audit was mutable")
	}
}

func versionCount(values []passkeys.EncryptionVersionCount, version int) uint64 {
	for _, value := range values {
		if value.Version == version {
			return value.Count
		}
	}
	return 0
}
