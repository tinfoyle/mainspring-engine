package migrations_test

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type adminClock struct{ now time.Time }

func (c *adminClock) Now() time.Time { return c.now }
func TestPostgresAdminAuthenticatorEnrollmentReplayRecoveryAndRevocation(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}
	user := ids.UserID("71000000-0000-4000-8000-000000000001")
	clock := &adminClock{time.Now().UTC().Truncate(time.Second)}
	_, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES($1,'admin-fixture@example.test','Test Admin','active',$2,1,$2)`, user, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	identifier := "https://accounts.google.com\x1fadmin-fixture"
	_, err = pool.Exec(ctx, `INSERT INTO authentication_identities(user_id,provider,identifier,created_at,updated_at) VALUES($1,'oidc',$2,$3,$3)`, user, identifier, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `SELECT spyglass_operations_assign_staff_role($1,$2,$3,'operations_administrator','test-owner','Approve isolated authentication test.','local')`, ids.RandomGenerator{}.New(), ids.RandomGenerator{}.New(), user)
	if err != nil {
		t.Fatal(err)
	}
	const role = "spyglass_operations_auth_test"
	_, err = pool.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
 GRANT USAGE ON SCHEMA public TO `+role+`;
 GRANT SELECT (user_id,provider,identifier) ON authentication_identities TO `+role+`;
 GRANT SELECT ON users,operations_staff,operations_staff_role_assignments,operations_access_events TO `+role+`;
 GRANT SELECT,INSERT,UPDATE ON operations_authenticators,operations_sessions TO `+role+`;
 GRANT SELECT,INSERT,UPDATE,DELETE ON operations_login_challenges TO `+role+`;
 GRANT INSERT ON operations_authentication_events TO `+role)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE `+role) }()
	restricted := openPool(t, ctx, databaseURL, func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, "SET ROLE "+role); return e })
	defer restricted.Close()
	if _, e := restricted.Exec(ctx, "SELECT secret_hash FROM authentication_identities"); e == nil {
		t.Fatal("admin identity role can read customer password hashes")
	}
	repo := postgres.NewOperationsAuthenticationRepository(restricted)
	sessionsService, err := sessions.NewService(postgres.NewOperationsSessionRepository(restricted), ids.RandomGenerator{}, clock, 8*time.Hour, 30*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := operationsauth.NewCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := operationsauth.New(repo, cipher, sessionsService, clock, "https://ops.example.test")
	if err != nil {
		t.Fatal(err)
	}
	begin := func() string {
		t.Helper()
		token, e := auth.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = auth.Status(ctx, token); !errors.Is(e, operationsauth.ErrDenied) {
			t.Fatal("unverified challenge exposed status", e)
		}
		ticket, e := operationsauth.SealTicket(cipher, operationsauth.Ticket{Identifier: identifier, State: token, Audience: "https://ops.example.test", ExpiresAt: clock.now.Add(time.Minute)})
		if e != nil {
			t.Fatal(e)
		}
		if e = auth.Google(ctx, token, ticket); e != nil {
			t.Fatal(e)
		}
		if e = auth.Google(ctx, token, ticket); !errors.Is(e, operationsauth.ErrDenied) {
			t.Fatal("Google handoff replay accepted", e)
		}
		return token
	}
	token := begin()
	if _, e := auth.Verify(ctx, token, "000000", false); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("unenrolled login accepted", e)
	}
	setup, e := auth.Setup(ctx, token)
	if e != nil {
		t.Fatal(e)
	}
	again, e := auth.Setup(ctx, token)
	if e != nil || again.Secret != setup.Secret {
		t.Fatal("refresh rotated pending secret", e)
	}
	secret, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	if e != nil {
		t.Fatal(e)
	}
	var encoded string
	if e = pool.QueryRow(ctx, `SELECT pending::text FROM operations_login_challenges LIMIT 1`).Scan(&encoded); e != nil || strings.Contains(encoded, setup.Secret) {
		t.Fatal("secret persisted in plaintext", e)
	}
	enrolled, e := auth.Verify(ctx, token, operationsauth.Code(secret, clock.now.Unix()/30), false)
	if e != nil || enrolled.Issued == nil || len(enrolled.RecoveryCodes) != 8 {
		t.Fatal("enrollment failed", e)
	}
	if enrolled.Issued.Session.AuthenticationMethod != sessions.AuthenticationMethodGoogleTOTP {
		t.Fatal("incorrect assurance")
	}
	if _, e = auth.Verify(ctx, token, operationsauth.Code(secret, clock.now.Unix()/30), false); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("challenge replay accepted", e)
	}
	token = begin()
	if _, e = auth.Setup(ctx, token); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("Google alone replaced authenticator", e)
	}
	if _, e = auth.Verify(ctx, token, operationsauth.Code(secret, clock.now.Unix()/30), false); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("TOTP replay accepted", e)
	}
	for i := 0; i < 4; i++ {
		token = begin()
		if _, e = auth.Verify(ctx, token, "bad-code", false); !errors.Is(e, operationsauth.ErrDenied) {
			t.Fatal(e)
		}
	}
	token = begin()
	if _, e = auth.Verify(ctx, token, "bad-code", false); !errors.Is(e, operationsauth.ErrLimited) {
		t.Fatal("per-user budget bypassed", e)
	}
	clock.now = clock.now.Add(16 * time.Minute)
	token1, token2 := begin(), begin()
	code := operationsauth.Code(secret, clock.now.Unix()/30)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for _, value := range []string{token1, token2} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			result, e := auth.Verify(ctx, value, code, false)
			if e == nil && result.Issued != nil {
				mu.Lock()
				success++
				mu.Unlock()
			} else if !errors.Is(e, operationsauth.ErrDenied) {
				t.Error(e)
			}
		}(value)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("concurrent code successes=%d", success)
	}
	token = begin()
	recoveryCode := enrolled.RecoveryCodes[0]
	recovered, e := auth.Verify(ctx, token, recoveryCode, true)
	if e != nil || !recovered.RecoveryOnly || recovered.Issued != nil {
		t.Fatal("recovery bypassed authenticator", e)
	}
	if _, e = sessionsService.Authenticate(ctx, enrolled.Issued.Token); e == nil {
		t.Fatal("recovery left old session active")
	}
	other := begin()
	if _, e = auth.Verify(ctx, other, operationsauth.Code(secret, clock.now.Unix()/30+1), false); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("old authenticator accepted after recovery", e)
	}

	if _, e = auth.Verify(ctx, other, recoveryCode, true); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("recovery replay accepted", e)
	}
	setup, e = auth.Setup(ctx, token)
	if e != nil {
		t.Fatal(e)
	}
	replacement, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	replaced, e := auth.Verify(ctx, token, operationsauth.Code(replacement, clock.now.Unix()/30), false)
	if e != nil || len(replaced.RecoveryCodes) != 8 {
		t.Fatal("replacement failed", e)
	}
	if _, e = auth.Status(ctx, other); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("old challenge survived replacement", e)
	}
	clock.now = clock.now.Add(30 * time.Second)
	if e = auth.Reauthenticate(ctx, user, replaced.Issued.Session.ID, operationsauth.Code(replacement, clock.now.Unix()/30)); e != nil {
		t.Fatal(e)
	}
	if e = auth.Reauthenticate(ctx, user, replaced.Issued.Session.ID, operationsauth.Code(replacement, clock.now.Unix()/30)); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("reauth replay accepted", e)
	}
	token = begin()
	_, e = pool.Exec(ctx, `SELECT spyglass_operations_revoke_staff_role($1,$2,'operations_administrator','test-owner','End isolated authentication test.','local')`, ids.RandomGenerator{}.New(), user)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = auth.Status(ctx, token); !errors.Is(e, operationsauth.ErrDenied) {
		t.Fatal("revoked staff challenge accepted", e)
	}
	if _, e = sessionsService.Authenticate(ctx, replaced.Issued.Token); e == nil {
		t.Fatal("revoked staff session accepted")
	}
	if _, e = pool.Exec(ctx, `DELETE FROM operations_authentication_events`); e == nil {
		t.Fatal("authentication audit was mutable")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM operations_authentication_events WHERE action='recovery_used'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("recovery audit missing", e)
	}
}
