package migrations_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresRegistrationCatalogAndCheckoutContracts(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	published, err := postgresadapter.NewCatalogRepository(pool).Published(ctx)
	if err != nil {
		t.Fatalf("load published catalog: %v", err)
	}
	if published.Version != 2 || len(published.Plans) != 3 {
		t.Fatalf("unexpected published catalog: version=%d plans=%d", published.Version, len(published.Plans))
	}

	adminNow := time.Now().UTC().Truncate(time.Millisecond)
	adminService, err := catalogadmin.NewService(postgresadapter.NewCatalogAdminRepository(pool), ids.RandomGenerator{}, fixedClock{now: adminNow})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := adminService.CreateDraft(ctx, catalog.Default(adminNow), "catalog-author@example.com", "prepare reviewed package and offer publication")
	if err != nil || draft.Version != 3 || draft.State != catalogadmin.StateDraft {
		t.Fatalf("create catalog draft = %+v, %v", draft, err)
	}
	for _, mapping := range []struct{ offer, price string }{{"team-monthly-v1", "price_catalog_team_test"}, {"operating-monthly-v1", "price_catalog_operating_test"}} {
		if err := adminService.MapStripePrice(ctx, draft.Version, mapping.offer, "test", mapping.price, "catalog-author@example.com", "attach reviewed Stripe test price mapping"); err != nil {
			t.Fatalf("map catalog offer %s: %v", mapping.offer, err)
		}
	}
	if _, err := adminService.RequestReview(ctx, draft.Version, "catalog-author@example.com", "request independent commercial catalog review"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminService.Approve(ctx, draft.Version, "catalog-author@example.com", "attempt self approval must be rejected"); !errors.Is(err, catalogadmin.ErrReviewSeparation) {
		t.Fatalf("self-review result = %v", err)
	}
	if _, err := adminService.Approve(ctx, draft.Version, "catalog-reviewer@example.com", "approve validated package and offer publication"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminService.Publish(ctx, draft.Version, adminNow, "catalog-publisher@example.com", "publish independently reviewed catalog version"); err != nil {
		t.Fatal(err)
	}
	current, err := postgresadapter.NewCatalogRepository(pool).Published(ctx)
	if err != nil || current.Version != draft.Version {
		t.Fatalf("new publication current = %d, %v", current.Version, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE catalog_publications SET content=jsonb_set(content,'{version}','99'::jsonb) WHERE version=$1`, draft.Version); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("catalog content mutation result = %v", err)
	}
	if _, err := adminService.Retire(ctx, draft.Version, "catalog-publisher@example.com", "retire current catalog to verify safe fallback"); err != nil {
		t.Fatal(err)
	}
	fallback, err := postgresadapter.NewCatalogRepository(pool).Published(ctx)
	if err != nil || fallback.Version != 2 {
		t.Fatalf("retired publication fallback = %d, %v", fallback.Version, err)
	}
	if _, err := adminService.Publish(ctx, draft.Version, adminNow, "catalog-publisher@example.com", "republish prior reviewed version as rollback recovery"); err != nil {
		t.Fatal(err)
	}
	rolledForward, err := postgresadapter.NewCatalogRepository(pool).Published(ctx)
	if err != nil || rolledForward.Version != draft.Version {
		t.Fatalf("republished catalog = %d, %v", rolledForward.Version, err)
	}
	var catalogAuditEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM catalog_operator_events WHERE catalog_version=$1`, draft.Version).Scan(&catalogAuditEvents); err != nil || catalogAuditEvents != 8 {
		t.Fatalf("catalog audit events = %d, %v", catalogAuditEvents, err)
	}
	incomplete, err := adminService.CreateDraft(ctx, catalog.Default(adminNow), "catalog-author@example.com", "verify paid offers require provider mappings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminService.RequestReview(ctx, incomplete.Version, "catalog-author@example.com", "request review without required price mappings"); !errors.Is(err, catalogadmin.ErrOfferMapping) {
		t.Fatalf("incomplete offer mapping result = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE catalog_publications SET state='published',published_at=$2,published_by='bypass' WHERE version=$1`, incomplete.Version, adminNow); err == nil || !strings.Contains(err.Error(), "invalid catalog state transition") {
		t.Fatalf("direct catalog state bypass result = %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM catalog_publications WHERE version=$1`, incomplete.Version); err == nil || !strings.Contains(err.Error(), "cannot be deleted") {
		t.Fatalf("catalog deletion result = %v", err)
	}
	const concurrentDrafts = 4
	versions := make(chan uint64, concurrentDrafts)
	errorsChannel := make(chan error, concurrentDrafts)
	var catalogGroup sync.WaitGroup
	for index := 0; index < concurrentDrafts; index++ {
		catalogGroup.Add(1)
		go func(index int) {
			defer catalogGroup.Done()
			content := catalog.Default(adminNow)
			content.Plans[0].Description = fmt.Sprintf("Concurrent draft %d", index)
			created, err := adminService.CreateDraft(ctx, content, "catalog-author@example.com", "verify serialized catalog version allocation")
			if err != nil {
				errorsChannel <- err
				return
			}
			versions <- created.Version
		}(index)
	}
	catalogGroup.Wait()
	close(versions)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatal(err)
	}
	uniqueVersions := map[uint64]bool{}
	for version := range versions {
		uniqueVersions[version] = true
	}
	if len(uniqueVersions) != concurrentDrafts {
		t.Fatalf("concurrent catalog versions = %#v", uniqueVersions)
	}

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	networkLimiter := postgresadapter.NewNetworkRateLimiter(pool)
	actor := [32]byte{9}
	policy := abuse.Policy{Limit: 2, Window: 15 * time.Minute}
	for attempt, expected := range []bool{true, true, false, false} {
		allowed, err := networkLimiter.Consume(ctx, abuse.ScopeLogin, actor, now, policy)
		if err != nil || allowed != expected {
			t.Fatalf("network limiter attempt %d: allowed=%v want=%v err=%v", attempt+1, allowed, expected, err)
		}
	}
	allowedAfterWindow, err := networkLimiter.Consume(ctx, abuse.ScopeLogin, actor, now.Add(16*time.Minute), policy)
	if err != nil || !allowedAfterWindow {
		t.Fatalf("network limiter did not reset after window: allowed=%v err=%v", allowedAfterWindow, err)
	}
	notificationQueue := postgresadapter.NewNotificationOutbox(pool)
	notificationCipher, err := notifications.NewCipher(bytes.Repeat([]byte{0x51}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	queuedSender, err := notifications.NewQueuedSender(notificationQueue, notificationCipher, ids.RandomGenerator{}, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	queuedRecovery := recovery.Message{Email: "owner@example.com", DisplayName: "Owner", Token: "outbox-secret-token", ExpiresAt: now.Add(time.Hour)}
	if err := queuedSender.SendRecovery(ctx, queuedRecovery); err != nil {
		t.Fatalf("enqueue encrypted notification: %v", err)
	}
	var plaintextEmail, plaintextToken bool
	if err := pool.QueryRow(ctx, `
		SELECT position(convert_to($1,'UTF8') in ciphertext)>0,position(convert_to($2,'UTF8') in ciphertext)>0
		FROM identity_notification_outbox LIMIT 1`, queuedRecovery.Email, queuedRecovery.Token).Scan(&plaintextEmail, &plaintextToken); err != nil {
		t.Fatal(err)
	}
	if plaintextEmail || plaintextToken {
		t.Fatal("notification outbox contains plaintext identity credentials")
	}
	notificationDelivery := &captureNotifications{}
	notificationProcessor, err := notifications.NewProcessor(notificationQueue, notificationCipher, notificationDelivery, fixedClock{now: now}, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := notificationProcessor.ProcessOne(ctx); err != nil || !worked || notificationDelivery.recovery.Token != queuedRecovery.Token {
		t.Fatalf("process encrypted notification: worked=%v delivery=%+v err=%v", worked, notificationDelivery.recovery, err)
	}
	var notificationState string
	if err := pool.QueryRow(ctx, `SELECT processing_state FROM identity_notification_outbox LIMIT 1`).Scan(&notificationState); err != nil || notificationState != "delivered" {
		t.Fatalf("notification state = %q, %v", notificationState, err)
	}

	repository := postgresadapter.NewRegistrationRepository(pool)
	sender := &captureVerification{}
	service := registration.NewService(repository, sender, repository, func() catalog.PublishedCatalog { return published }, ids.RandomGenerator{}, fixedClock{now: now}, staticPasswordHasher{})
	if _, err := service.Begin(ctx, registration.BeginCommand{Email: "owner@example.com", DisplayName: "Owner", AccountName: "Northstar Labs", Region: "us-east"}); err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	if sender.message.Token == "" {
		t.Fatal("registration did not emit a verification token")
	}
	provisioned, err := service.Complete(ctx, registration.CompleteCommand{Token: sender.message.Token, Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("complete registration: %v", err)
	}
	if provisioned.Account.Type != "free" || len(provisioned.Snapshot.Packages) != 1 || string(provisioned.Snapshot.Packages[0].Code) != "knowledge" {
		t.Fatalf("unexpected free account projection: type=%s packages=%v", provisioned.Account.Type, provisioned.Snapshot.Packages)
	}
	if _, err := service.Begin(ctx, registration.BeginCommand{Email: " OWNER@example.com ", DisplayName: "Owner Again", AccountName: "Other Labs", Region: "us-east"}); !errors.Is(err, registration.ErrEmailExists) {
		t.Fatalf("duplicate registration error = %v, want ErrEmailExists", err)
	}

	sessionRepository := postgresadapter.NewSessionRepository(pool)
	sessionService, err := sessions.NewService(sessionRepository, ids.RandomGenerator{}, fixedClock{now: now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessionService.IssueForClient(ctx, provisioned.User.ID, provisioned.User.SecurityVersion, "PostgreSQL contract browser")
	if err != nil {
		t.Fatalf("issue persistent session: %v", err)
	}
	active, err := sessionService.Active(ctx, provisioned.User.ID, issued.Session.ID)
	if err != nil || len(active) != 1 || !active[0].Current || active[0].ClientLabel != "PostgreSQL contract browser" {
		t.Fatalf("persistent active sessions = %+v, %v", active, err)
	}
	if revoked, err := sessionService.RevokeOwned(ctx, ids.UserID("30000000-0000-4000-8000-000000000003"), issued.Session.ID); err != nil || revoked {
		t.Fatalf("cross-user persistent revoke = %v, %v", revoked, err)
	}
	if err := sessionService.MarkReauthenticated(ctx, provisioned.User.ID, issued.Session.ID); err != nil {
		t.Fatalf("mark persistent session reauthenticated: %v", err)
	}
	var securityEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_security_events WHERE user_id=$1`, provisioned.User.ID).Scan(&securityEvents); err != nil {
		t.Fatal(err)
	}
	if securityEvents != 2 {
		t.Fatalf("security event count = %d, want session creation and reauthentication", securityEvents)
	}

	recoverySender := &captureRecovery{}
	authenticationRepository := postgresadapter.NewAuthenticationRepository(pool)
	networkGuard, err := abuse.NewGuard(postgresadapter.NewNetworkRateLimiter(pool))
	if err != nil {
		t.Fatal(err)
	}
	recoveryService, err := recovery.NewService(postgresadapter.NewRecoveryRepository(pool), recoverySender, authenticationRepository, networkGuard, authn.Passwords{}, ids.RandomGenerator{}, fixedClock{now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	unknownRecovery, err := recoveryService.Begin(ctx, recovery.BeginCommand{Email: "missing@example.com", NetworkActor: [32]byte{1}})
	if err != nil || unknownRecovery.Delivered {
		t.Fatalf("unknown persistent recovery = %+v, %v", unknownRecovery, err)
	}
	startedRecovery, err := recoveryService.Begin(ctx, recovery.BeginCommand{Email: " OWNER@example.com ", NetworkActor: [32]byte{1}})
	if err != nil || !startedRecovery.Delivered || recoverySender.message.Token == "" {
		t.Fatalf("begin persistent recovery = %+v, message=%+v, %v", startedRecovery, recoverySender.message, err)
	}
	loginLimitKey := sha256.Sum256([]byte("owner@example.com"))
	if err := authenticationRepository.Failure(ctx, loginLimitKey, now, 1, 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := recoveryService.Complete(ctx, recovery.CompleteCommand{Token: recoverySender.message.Token, Password: "replacement password material"}); err != nil {
		t.Fatalf("complete persistent recovery: %v", err)
	}
	if blocked, err := authenticationRepository.Blocked(ctx, loginLimitKey, now.Add(time.Minute)); err != nil || blocked {
		t.Fatalf("login limiter after recovery = blocked:%v err:%v", blocked, err)
	}
	if _, err := sessionService.Authenticate(ctx, issued.Token); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatalf("pre-recovery session result = %v, want invalid session", err)
	}
	localIdentity, err := authenticationRepository.LocalIdentityForUser(ctx, provisioned.User.ID)
	passwords := authn.Passwords{}
	if err != nil || !passwords.Verify(localIdentity.PasswordHash, "replacement password material") || passwords.Verify(localIdentity.PasswordHash, "correct horse battery staple") {
		t.Fatalf("recovered credential was not replaced: %v", err)
	}
	if err := recoveryService.Complete(ctx, recovery.CompleteCommand{Token: recoverySender.message.Token, Password: "another replacement password"}); !errors.Is(err, recovery.ErrInvalidChallenge) {
		t.Fatalf("reused recovery token result = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_security_events WHERE user_id=$1`, provisioned.User.ID).Scan(&securityEvents); err != nil {
		t.Fatal(err)
	}
	if securityEvents != 3 {
		t.Fatalf("security event count after recovery = %d, want three", securityEvents)
	}
	securityHistory, err := sessionService.SecurityEvents(ctx, provisioned.User.ID, 10)
	if err != nil || len(securityHistory) != 3 || securityHistory[0].Type != sessions.EventCredentialRecovered {
		t.Fatalf("persistent security history = %+v, %v", securityHistory, err)
	}
	otherSecurityHistory, err := sessionService.SecurityEvents(ctx, ids.UserID("30000000-0000-4000-8000-000000000003"), 10)
	if err != nil || len(otherSecurityHistory) != 0 {
		t.Fatalf("cross-user security history = %+v, %v", otherSecurityHistory, err)
	}

	commercial := postgresadapter.NewCommercialAccessRepository(pool)
	price, err := commercial.ProviderPrice(ctx, draft.Version, "team-monthly-v1", "stripe", "test")
	if err != nil || price != "price_catalog_team_test" {
		t.Fatalf("provider price = %q, %v", price, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE offer_provider_prices SET provider_price_id='price_tampered' WHERE catalog_version=$1 AND offer_code='team-monthly-v1'`, draft.Version); err == nil || !strings.Contains(err.Error(), "only while the catalog is draft") {
		t.Fatalf("published price mapping mutation result = %v", err)
	}

	requests := []string{ids.RandomGenerator{}.New(), ids.RandomGenerator{}.New()}
	type reservationResult struct {
		request string
		value   bool
		err     error
	}
	results := make(chan reservationResult, len(requests))
	var group sync.WaitGroup
	for _, requestID := range requests {
		group.Add(1)
		go func(requestID string) {
			defer group.Done()
			reservation, err := commercial.BeginCheckout(ctx, provisioned.Account.ID, "team-monthly-v1", "test", requestID, now)
			results <- reservationResult{request: requestID, value: reservation.Proceed, err: err}
		}(requestID)
	}
	group.Wait()
	close(results)
	proceedingRequest := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("reserve checkout: %v", result.err)
		}
		if result.value {
			if proceedingRequest != "" {
				t.Fatal("parallel checkout requests both received permission to proceed")
			}
			proceedingRequest = result.request
		}
	}
	if proceedingRequest == "" {
		t.Fatal("parallel checkout requests produced no winner")
	}
	hosted := billing.HostedSession{ID: "cs_test_contract", URL: "https://checkout.stripe.test/session", ExpiresAt: now.Add(20 * time.Minute)}
	if err := commercial.CompleteCheckout(ctx, provisioned.Account.ID, proceedingRequest, hosted, now); err != nil {
		t.Fatalf("complete checkout reservation: %v", err)
	}
	resumed, err := commercial.BeginCheckout(ctx, provisioned.Account.ID, "team-monthly-v1", "test", ids.RandomGenerator{}.New(), now)
	if err != nil || resumed.Resume == nil || resumed.Resume.ID != hosted.ID {
		t.Fatalf("resume checkout = %+v, %v", resumed, err)
	}
}

func TestPostgresMigrationsAndAccountIsolation(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()

	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		result, err := migrations.Apply(ctx, owner, target)
		if err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
		if len(result.Applied) == 0 {
			t.Fatalf("expected first %s migration run to apply files", target)
		}
		result, err = migrations.Apply(ctx, owner, target)
		if err != nil {
			t.Fatalf("reapply %s migrations: %v", target, err)
		}
		if len(result.Applied) != 0 {
			t.Fatalf("expected idempotent %s migration run, applied %v", target, result.Applied)
		}
	}

	var ledgerCount, catalogCount, cellCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass_schema_migrations`).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM catalog_publications WHERE state='published'`).Scan(&catalogCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM cells WHERE state='active'`).Scan(&cellCount); err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 11 || catalogCount != 1 || cellCount != 1 {
		t.Fatalf("unexpected migrated state: ledger=%d published_catalogs=%d active_cells=%d", ledgerCount, catalogCount, cellCount)
	}

	testAccountIsolation(t, ctx, owner, databaseURL)
	if _, err := owner.Exec(ctx, `UPDATE spyglass_schema_migrations SET checksum='\\x00'::bytea WHERE target='global' AND version=1`); err != nil {
		t.Fatalf("tamper migration ledger: %v", err)
	}
	if _, err := migrations.Apply(ctx, owner, migrations.Global); err == nil || !strings.Contains(err.Error(), "migrations are immutable") {
		t.Fatalf("tampered migration result = %v, want immutable-migration error", err)
	}
}

func testAccountIsolation(t *testing.T, ctx context.Context, owner *pgxpool.Pool, databaseURL string) {
	t.Helper()
	accountA := ids.AccountID("10000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("20000000-0000-4000-8000-000000000002")
	if _, err := owner.Exec(ctx, `
		INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',statement_timestamp()),($2,1,'active',statement_timestamp())`, accountA, accountB); err != nil {
		t.Fatalf("seed account namespaces: %v", err)
	}

	role := "spyglass_serving_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN`); err != nil {
		t.Fatalf("create serving role: %v", err)
	}
	defer func() { _, _ = owner.Exec(context.Background(), `DROP ROLE IF EXISTS `+role) }()
	if _, err := owner.Exec(ctx, `GRANT USAGE ON SCHEMA spyglass TO `+role+`; GRANT SELECT,INSERT ON ALL TABLES IN SCHEMA spyglass TO `+role); err != nil {
		t.Fatalf("grant serving role: %v", err)
	}

	serving := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer serving.Close()

	var visible int
	if err := serving.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces`).Scan(&visible); err != nil {
		t.Fatalf("query without account context: %v", err)
	}
	if visible != 0 {
		t.Fatalf("serving role saw %d rows without account context", visible)
	}

	cellPool, err := database.NewCellPool(serving)
	if err != nil {
		t.Fatal(err)
	}
	err = cellPool.WithAccountTx(ctx, accountA, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces`).Scan(&visible); err != nil {
			return err
		}
		if visible != 1 {
			return fmt.Errorf("account A saw %d namespace rows", visible)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces WHERE account_id=$1`, accountB).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			return fmt.Errorf("account A read account B by guessed identifier")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("account-scoped transaction: %v", err)
	}
	err = cellPool.WithAccountTx(ctx, accountA, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO spyglass.account_audit_events
			(account_id,id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
			VALUES ($1,'30000000-0000-4000-8000-000000000003','test','user','test','test','{}',statement_timestamp())`, accountB)
		return err
	})
	if !isRowSecurityViolation(err) {
		t.Fatalf("cross-account insert error = %v, want row-security violation", err)
	}

	if err := serving.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces`).Scan(&visible); err != nil {
		t.Fatalf("query after account transaction: %v", err)
	}
	if visible != 0 {
		t.Fatalf("transaction-local account context leaked; visible rows=%d", visible)
	}
}

func createDatabase(t *testing.T, ctx context.Context, adminURL string) (string, func()) {
	t.Helper()
	name := "spyglass_test_" + randomSuffix(t)
	admin := openPool(t, ctx, adminURL, nil)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatalf("create test database: %v", err)
	}
	databaseURL := withDatabase(t, adminURL, name)
	return databaseURL, func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, `DROP DATABASE IF EXISTS `+name); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		admin.Close()
	}
}

func openPool(t *testing.T, ctx context.Context, databaseURL string, afterConnect func(context.Context, *pgx.Conn) error) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	config.MaxConns = 4
	config.AfterConnect = afterConnect
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	return pool
}

func withDatabase(t *testing.T, rawURL, database string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var value [6]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value[:])
}

func isRowSecurityViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "42501"
}

type captureVerification struct {
	message registration.VerificationMessage
}

func (sender *captureVerification) SendVerification(_ context.Context, message registration.VerificationMessage) error {
	sender.message = message
	return nil
}

type captureRecovery struct{ message recovery.Message }

func (sender *captureRecovery) SendRecovery(_ context.Context, message recovery.Message) error {
	sender.message = message
	return nil
}

type captureNotifications struct {
	verification registration.VerificationMessage
	invitation   invitations.Message
	recovery     recovery.Message
}

func (sender *captureNotifications) SendVerification(_ context.Context, message registration.VerificationMessage) error {
	sender.verification = message
	return nil
}
func (sender *captureNotifications) SendInvitation(_ context.Context, message invitations.Message) error {
	sender.invitation = message
	return nil
}
func (sender *captureNotifications) SendRecovery(_ context.Context, message recovery.Message) error {
	sender.recovery = message
	return nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type staticPasswordHasher struct{}

func (staticPasswordHasher) Hash(string) (string, error) { return "$argon2id$integration-test", nil }
