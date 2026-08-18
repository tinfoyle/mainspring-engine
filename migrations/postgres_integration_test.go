package migrations_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/admissionhttp"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/entitlementrollout"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
	"github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	admissiontransport "github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
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
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
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

	// Anchor publication time to the database clock after seed migrations. A
	// host clock value in the past can sort behind the seeded publication, while
	// a future value is not yet visible to Published().
	var adminNow time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&adminNow); err != nil {
		t.Fatal(err)
	}
	adminNow = adminNow.UTC()
	adminService, err := catalogadmin.NewService(postgresadapter.NewCatalogAdminRepository(pool), ids.RandomGenerator{}, fixedClock{now: adminNow})
	if err != nil {
		t.Fatal(err)
	}
	nextCatalog := catalog.Default(adminNow)
	for index := range nextCatalog.Packages {
		if nextCatalog.Packages[index].Code == catalog.PackageKnowledge {
			nextCatalog.Packages[index].Version = 2
			nextCatalog.Packages[index].DefaultLimits["documents"] = 50
		}
	}
	for index := range nextCatalog.Plans {
		if nextCatalog.Plans[index].Code == "free" {
			nextCatalog.Plans[index].Packages[catalog.PackageWork] = catalog.ModeEnabled
		}
	}
	draft, err := adminService.CreateDraft(ctx, nextCatalog, "catalog-author@example.com", "prepare reviewed package and offer publication")
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
	attributedInvitation := invitations.Message{AccountID: provisioned.Account.ID, Email: "member@example.com", AccountName: provisioned.Account.DisplayName, Token: "account-attributed-invitation", Role: accounts.RoleMember, ExpiresAt: now.Add(time.Hour)}
	if err := queuedSender.SendInvitation(ctx, attributedInvitation); err != nil {
		t.Fatalf("enqueue Account-attributed notification: %v", err)
	}
	var notificationAccountID string
	if err := pool.QueryRow(ctx, `SELECT account_id::text FROM identity_notification_outbox WHERE processing_state='queued' AND kind='invitation' ORDER BY created_at DESC LIMIT 1`).Scan(&notificationAccountID); err != nil || notificationAccountID != string(provisioned.Account.ID) {
		t.Fatalf("notification Account attribution = %q, %v", notificationAccountID, err)
	}
	billingPayload := []byte(`{"id":"evt_erasure_attribution","data":{"object":{"metadata":{"spyglass_account_id":"` + string(provisioned.Account.ID) + `"}}}}`)
	billingHash := sha256.Sum256(billingPayload)
	billingEntry := billing.InboxEntry{ProviderEventID: "evt_erasure_attribution", AccountID: provisioned.Account.ID, EventType: "customer.subscription.updated", ProviderCreatedAt: now, Mode: "test", PayloadHash: billingHash, SignatureVerifiedAt: now, ProcessingState: "accepted", CreatedAt: now}
	accepted, err := postgresadapter.NewBillingInbox(pool).Accept(ctx, billingEntry, billingPayload)
	if err != nil || !accepted {
		t.Fatalf("accept Account-attributed billing event: accepted=%v err=%v", accepted, err)
	}
	var billingAccountID string
	if err := pool.QueryRow(ctx, `SELECT account_id::text FROM billing_event_inbox WHERE provider_event_id=$1`, billingEntry.ProviderEventID).Scan(&billingAccountID); err != nil || billingAccountID != string(provisioned.Account.ID) {
		t.Fatalf("billing event Account attribution = %q, %v", billingAccountID, err)
	}
	missingAccountID := ids.AccountID(ids.RandomGenerator{}.New())
	suppressedPayload := []byte(`{"id":"evt_post_erasure_retry","protected":"must-not-persist"}`)
	suppressedHash := sha256.Sum256(suppressedPayload)
	suppressedEntry := billing.InboxEntry{ProviderEventID: "evt_post_erasure_retry", AccountID: missingAccountID, EventType: "customer.subscription.updated", ProviderCreatedAt: now, Mode: "test", PayloadHash: suppressedHash, SignatureVerifiedAt: now, ProcessingState: "accepted", CreatedAt: now}
	accepted, err = postgresadapter.NewBillingInbox(pool).Accept(ctx, suppressedEntry, suppressedPayload)
	if err != nil || accepted {
		t.Fatalf("post-erasure billing retry suppression: accepted=%v err=%v", accepted, err)
	}
	var suppressedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_event_inbox WHERE provider_event_id=$1`, suppressedEntry.ProviderEventID).Scan(&suppressedCount); err != nil || suppressedCount != 0 {
		t.Fatalf("post-erasure billing retry persisted: count=%d err=%v", suppressedCount, err)
	}
	directoryEntry, err := postgresadapter.NewAccountDirectoryRepository(pool).Lookup(ctx, provisioned.Account.ID)
	if err != nil || directoryEntry.CellID != provisioned.Account.CellID || directoryEntry.PlacementGeneration != provisioned.Account.PlacementGeneration || directoryEntry.RouteOrigin != "http://app-api.spyglass-reference.svc.cluster.local" {
		t.Fatalf("unexpected Account directory route: entry=%+v err=%v", directoryEntry, err)
	}
	legacyGrantID := ids.RandomGenerator{}.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_grants
		(id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,priority,reason,created_at)
		VALUES ($1,$2,'marketing',1,'enabled','subscription','sub_legacy_contract','{}'::jsonb,$3,50,'legacy purchased package',$3)`, legacyGrantID, provisioned.Account.ID, adminNow); err != nil {
		t.Fatalf("seed independent subscription grant: %v", err)
	}
	rolloutRepository := postgresadapter.NewEntitlementRolloutRepository(pool)
	rolloutProcessor, err := entitlementrollout.NewProcessor(rolloutRepository, ids.RandomGenerator{}, fixedClock{now: time.Now().UTC()}, 2*time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	processEntitlementTarget(t, ctx, pool, rolloutProcessor, provisioned.Account.ID, draft.Version)
	var entitlementVersion, reconciledCatalog uint64
	if err := pool.QueryRow(ctx, `SELECT entitlement_version,last_catalog_reconciled_version FROM accounts WHERE id=$1`, provisioned.Account.ID).Scan(&entitlementVersion, &reconciledCatalog); err != nil {
		t.Fatal(err)
	}
	if entitlementVersion != 2 || reconciledCatalog != draft.Version {
		t.Fatalf("catalog rollout account versions: entitlement=%d catalog=%d", entitlementVersion, reconciledCatalog)
	}
	var freeKnowledgeVersion uint64
	var freeKnowledgeRaw []byte
	if err := pool.QueryRow(ctx, `SELECT package_version,limits FROM entitlement_grants WHERE account_id=$1 AND source='free_plan' AND package_code='knowledge'`, provisioned.Account.ID).Scan(&freeKnowledgeVersion, &freeKnowledgeRaw); err != nil {
		t.Fatal(err)
	}
	var freeKnowledgeLimits map[string]int64
	if err := json.Unmarshal(freeKnowledgeRaw, &freeKnowledgeLimits); err != nil || freeKnowledgeVersion != 2 || freeKnowledgeLimits["documents"] != 50 {
		t.Fatalf("rolled out free Knowledge grant: version=%d limits=%s err=%v", freeKnowledgeVersion, freeKnowledgeRaw, err)
	}
	var legacyGrantCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_grants WHERE id=$1 AND source='subscription'`, legacyGrantID).Scan(&legacyGrantCount); err != nil || legacyGrantCount != 1 {
		t.Fatalf("independent subscription grant count=%d err=%v", legacyGrantCount, err)
	}
	var effectivePackagesRaw []byte
	if err := pool.QueryRow(ctx, `SELECT effective_packages FROM entitlement_snapshots WHERE account_id=$1 ORDER BY version DESC LIMIT 1`, provisioned.Account.ID).Scan(&effectivePackagesRaw); err != nil {
		t.Fatal(err)
	}
	var effectivePackages []entitlements.PackageAccess
	if err := json.Unmarshal(effectivePackagesRaw, &effectivePackages); err != nil {
		t.Fatal(err)
	}
	present := map[catalog.PackageCode]bool{}
	for _, item := range effectivePackages {
		present[item.Code] = true
	}
	for _, code := range []catalog.PackageCode{catalog.PackageKnowledge, catalog.PackageWork, catalog.PackageMarketing} {
		if !present[code] {
			t.Fatalf("rolled out snapshot missing %s: %+v", code, effectivePackages)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET last_catalog_reconciled_version=2 WHERE id=$1`, provisioned.Account.ID); err != nil {
		t.Fatal(err)
	}
	processEntitlementTarget(t, ctx, pool, rolloutProcessor, provisioned.Account.ID, draft.Version)
	var unchangedVersion uint64
	if err := pool.QueryRow(ctx, `SELECT entitlement_version FROM accounts WHERE id=$1`, provisioned.Account.ID).Scan(&unchangedVersion); err != nil || unchangedVersion != entitlementVersion {
		t.Fatalf("unchanged rollout advanced snapshot: version=%d err=%v", unchangedVersion, err)
	}
	laterCatalog := catalog.Default(adminNow)
	laterDraft, err := adminService.CreateDraft(ctx, laterCatalog, "catalog-author@example.com", "prepare a second reviewed entitlement configuration")
	if err != nil {
		t.Fatal(err)
	}
	for _, mapping := range []struct{ offer, price string }{{"team-monthly-v1", "price_catalog_later_team"}, {"operating-monthly-v1", "price_catalog_later_operating"}} {
		if err := adminService.MapStripePrice(ctx, laterDraft.Version, mapping.offer, "test", mapping.price, "catalog-author@example.com", "attach second-version Stripe test mapping"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := adminService.RequestReview(ctx, laterDraft.Version, "catalog-author@example.com", "request review of second entitlement configuration"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminService.Approve(ctx, laterDraft.Version, "catalog-reviewer@example.com", "approve second entitlement configuration"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminService.Publish(ctx, laterDraft.Version, adminNow, "catalog-publisher@example.com", "publish second entitlement configuration"); err != nil {
		t.Fatal(err)
	}
	processEntitlementTarget(t, ctx, pool, rolloutProcessor, provisioned.Account.ID, laterDraft.Version)
	if _, err := adminService.Retire(ctx, draft.Version, "catalog-publisher@example.com", "retire prior version before rollback verification"); err != nil {
		t.Fatal(err)
	}
	var rollbackNow time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&rollbackNow); err != nil {
		t.Fatal(err)
	}
	rollbackNow = rollbackNow.UTC()
	rollbackService, _ := catalogadmin.NewService(postgresadapter.NewCatalogAdminRepository(pool), ids.RandomGenerator{}, fixedClock{now: rollbackNow})
	if _, err := rollbackService.Publish(ctx, draft.Version, rollbackNow, "catalog-publisher@example.com", "republish reviewed lower version for rollback"); err != nil {
		t.Fatal(err)
	}
	rollbackProcessor, _ := entitlementrollout.NewProcessor(rolloutRepository, ids.RandomGenerator{}, fixedClock{now: rollbackNow}, 2*time.Minute, 2)
	processEntitlementTarget(t, ctx, pool, rollbackProcessor, provisioned.Account.ID, draft.Version)
	var rollbackEntitlementVersion, rollbackCatalogVersion uint64
	if err := pool.QueryRow(ctx, `SELECT entitlement_version,last_catalog_reconciled_version FROM accounts WHERE id=$1`, provisioned.Account.ID).Scan(&rollbackEntitlementVersion, &rollbackCatalogVersion); err != nil {
		t.Fatal(err)
	}
	if rollbackEntitlementVersion != 4 || rollbackCatalogVersion != draft.Version {
		t.Fatalf("rollback account versions: entitlement=%d catalog=%d", rollbackEntitlementVersion, rollbackCatalogVersion)
	}
	var restoredWork, preservedSubscription int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_grants WHERE account_id=$1 AND source='free_plan' AND package_code='work'`, provisioned.Account.ID).Scan(&restoredWork); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_grants WHERE id=$1 AND source='subscription'`, legacyGrantID).Scan(&preservedSubscription); err != nil {
		t.Fatal(err)
	}
	if restoredWork != 1 || preservedSubscription != 1 {
		t.Fatalf("rollback grants: restored_work=%d preserved_subscription=%d", restoredWork, preservedSubscription)
	}
	rollbackCurrent, err := postgresadapter.NewCatalogRepository(pool).Published(ctx)
	if err != nil || rollbackCurrent.Version != draft.Version {
		t.Fatalf("rollback current Catalog = %d, %v", rollbackCurrent.Version, err)
	}
	usageRepository := postgresadapter.NewUsageAdmissionRepository(pool)
	usageAuthorizer, err := access.NewAuthorizer(postgresadapter.NewAccessRepository(pool))
	if err != nil {
		t.Fatal(err)
	}
	usageNow := rollbackNow.Add(time.Minute)
	usageService, err := usageadmission.NewService(usageAuthorizer, usageRepository, ids.RandomGenerator{}, fixedClock{now: usageNow})
	if err != nil {
		t.Fatal(err)
	}
	firstUsageKey := "71000000-0000-4000-8000-000000000001"
	usageActor := access.Actor{UserID: provisioned.User.ID}
	firstUsage, err := usageService.Reserve(ctx, usageadmission.ReserveCommand{Actor: usageActor, AccountID: provisioned.Account.ID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 60, RequestID: firstUsageKey})
	if err != nil || firstUsage.Current != 60 || firstUsage.Maximum != 100 || firstUsage.EntitlementVersion != rollbackEntitlementVersion {
		t.Fatalf("first usage admission = %+v, %v", firstUsage, err)
	}
	repeatedUsage, err := usageService.Reserve(ctx, usageadmission.ReserveCommand{Actor: usageActor, AccountID: provisioned.Account.ID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 60, RequestID: firstUsageKey})
	if err != nil || repeatedUsage.ID != firstUsage.ID || repeatedUsage.Current != 60 {
		t.Fatalf("idempotent usage admission = %+v, %v", repeatedUsage, err)
	}
	if _, err := usageService.Reserve(ctx, usageadmission.ReserveCommand{Actor: usageActor, AccountID: provisioned.Account.ID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 59, RequestID: firstUsageKey}); !errors.Is(err, usageadmission.ErrReservationConflict) {
		t.Fatalf("conflicting usage idempotency key = %v", err)
	}
	usageResults := make(chan error, 2)
	for index, requestID := range []string{"71000000-0000-4000-8000-000000000002", "71000000-0000-4000-8000-000000000003"} {
		go func(index int, requestID string) {
			_, reserveErr := usageService.Reserve(ctx, usageadmission.ReserveCommand{Actor: usageActor, AccountID: provisioned.Account.ID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 30, RequestID: requestID})
			if reserveErr == nil {
				usageResults <- nil
				return
			}
			if !access.IsDenied(reserveErr, access.DenialLimitExceeded) {
				usageResults <- fmt.Errorf("concurrent usage %d: %w", index, reserveErr)
				return
			}
			usageResults <- reserveErr
		}(index, requestID)
	}
	var admittedCount, deniedCount int
	for range 2 {
		result := <-usageResults
		if result == nil {
			admittedCount++
		} else if access.IsDenied(result, access.DenialLimitExceeded) {
			deniedCount++
		} else {
			t.Fatal(result)
		}
	}
	if admittedCount != 1 || deniedCount != 1 {
		t.Fatalf("concurrent usage admission: admitted=%d denied=%d", admittedCount, deniedCount)
	}
	if _, err := usageService.Release(ctx, usageadmission.ReleaseCommand{Actor: usageActor, AccountID: provisioned.Account.ID, RequestID: firstUsageKey}); err != nil {
		t.Fatalf("release usage: %v", err)
	}
	releasedAgain, err := usageService.Release(ctx, usageadmission.ReleaseCommand{Actor: usageActor, AccountID: provisioned.Account.ID, RequestID: firstUsageKey})
	if err != nil || releasedAgain.State != usageadmission.ReservationReleased || releasedAgain.Current != 30 {
		t.Fatalf("idempotent usage release = %+v, %v", releasedAgain, err)
	}
	expiringKey := "71000000-0000-4000-8000-000000000004"
	expiresAt := usageNow.Add(time.Second)
	if _, err := usageRepository.Reserve(ctx, usageadmission.PersistCommand{ID: ids.RandomGenerator{}.New(), AccountID: provisioned.Account.ID, RequestID: expiringKey, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 40, Maximum: 100, ExpectedEntitlementVersion: rollbackEntitlementVersion, ExpiresAt: &expiresAt, Now: usageNow}); err != nil {
		t.Fatalf("reserve expiring usage: %v", err)
	}
	afterExpiry, err := usageRepository.Reserve(ctx, usageadmission.PersistCommand{ID: ids.RandomGenerator{}.New(), AccountID: provisioned.Account.ID, RequestID: "71000000-0000-4000-8000-000000000005", PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 40, Maximum: 100, ExpectedEntitlementVersion: rollbackEntitlementVersion, Now: usageNow.Add(2 * time.Second)})
	if err != nil || afterExpiry.Current != 70 {
		t.Fatalf("usage expiry reclamation = %+v, %v", afterExpiry, err)
	}
	var expiredState string
	if err := pool.QueryRow(ctx, `SELECT state FROM entitlement_usage_reservations WHERE account_id=$1 AND request_id=$2`, provisioned.Account.ID, expiringKey).Scan(&expiredState); err != nil || expiredState != "expired" {
		t.Fatalf("expired usage state = %q, %v", expiredState, err)
	}
	_, err = usageRepository.Reserve(ctx, usageadmission.PersistCommand{ID: ids.RandomGenerator{}.New(), AccountID: provisioned.Account.ID, RequestID: "71000000-0000-4000-8000-000000000006", PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, Maximum: 100, ExpectedEntitlementVersion: rollbackEntitlementVersion + 1, Now: usageNow})
	if !errors.Is(err, usageadmission.ErrEntitlementChanged) {
		t.Fatalf("stale entitlement usage admission = %v", err)
	}
	testRoutedWorkCommandAdmission(t, ctx, pool, databaseURL, provisioned.User.ID, provisioned.Account.ID, provisioned.Account.CellID, rollbackEntitlementVersion, usageNow.Add(3*time.Second))
	testWorkReleaseReconciliation(t, ctx, pool, databaseURL, provisioned.Account.ID, rollbackEntitlementVersion, usageNow.Add(10*time.Second))
	legacyTeam, ok := published.Plan("team")
	if !ok {
		t.Fatal("seeded legacy Catalog has no Team plan")
	}
	legacyOffer := catalog.Offer{}
	for _, offer := range published.Offers {
		if offer.Code == "team-monthly-v1" {
			legacyOffer = offer
			break
		}
	}
	legacyPackages := make(map[catalog.PackageCode]catalog.FeaturePackage, len(published.Packages))
	for _, definition := range published.Packages {
		legacyPackages[definition.Code] = definition
	}
	billingSyncedAt := usageNow.Add(3 * time.Second)
	if err := postgresadapter.NewBillingProjectionRepository(pool).ApplyProjection(ctx, billing.Projection{
		Subscription: billing.ProviderSubscription{ID: "sub_catalog_version_contract", Mode: "test", CustomerID: "cus_catalog_version_contract", State: "active", CurrentPeriodStart: billingSyncedAt.Add(-time.Hour), CurrentPeriodEnd: billingSyncedAt.Add(30 * 24 * time.Hour), ObjectVersion: "2026-08-18T10:00:03Z"},
		Mapping:      billing.MappedOffer{AccountID: provisioned.Account.ID, CatalogVersion: published.Version, Offer: legacyOffer, Plan: legacyTeam, Packages: legacyPackages, Catalog: published},
		Grants:       []entitlements.Grant{{ID: ids.GrantID(ids.RandomGenerator{}.New()), AccountID: provisioned.Account.ID, PackageCode: catalog.PackageWork, PackageVersion: 1, Mode: catalog.ModeEnabled, Source: entitlements.SourceSubscription, SourceReference: "sub_catalog_version_contract", Limits: map[catalog.LimitCode]int64{"active_items": 100}, StartsAt: billingSyncedAt.Add(-time.Hour), Priority: 50, Reason: "legacy Team subscription contract"}},
		SyncedAt:     billingSyncedAt,
	}); err != nil {
		t.Fatalf("cross-Catalog billing projection: %v", err)
	}
	var billingSnapshotCatalog uint64
	if err := pool.QueryRow(ctx, `SELECT catalog_version FROM entitlement_snapshots WHERE account_id=$1 ORDER BY version DESC LIMIT 1`, provisioned.Account.ID).Scan(&billingSnapshotCatalog); err != nil || billingSnapshotCatalog != draft.Version {
		t.Fatalf("billing snapshot Catalog = %d, %v; want current %d rather than mapped %d", billingSnapshotCatalog, err, draft.Version, published.Version)
	}
	leaseRolloutID := ids.RandomGenerator{}.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_catalog_rollouts (id,target_catalog_version,source,state,effective_at,seeded_count,created_at,seeded_at)
		VALUES ($1,$2,'drift_repair','processing',$3,1,$3,$3)`, leaseRolloutID, draft.Version, rollbackNow); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_recompute_queue (rollout_id,account_id,processing_state,created_at) VALUES ($1,$2,'pending',$3)`, leaseRolloutID, provisioned.Account.ID, rollbackNow); err != nil {
		t.Fatal(err)
	}
	firstClaim, claimed, err := rolloutRepository.Claim(ctx, rollbackNow, time.Second)
	if err != nil || !claimed || firstClaim.AttemptCount != 1 {
		t.Fatalf("first entitlement claim = %+v claimed=%v err=%v", firstClaim, claimed, err)
	}
	secondClaim, claimed, err := rolloutRepository.Claim(ctx, rollbackNow.Add(2*time.Second), time.Minute)
	if err != nil || !claimed || secondClaim.AttemptCount != 2 {
		t.Fatalf("reclaimed entitlement = %+v claimed=%v err=%v", secondClaim, claimed, err)
	}
	if err := rolloutRepository.MarkFailed(ctx, firstClaim, rollbackNow.Add(2*time.Second), rollbackNow.Add(time.Minute), "stale_attempt", false); err == nil {
		t.Fatal("stale entitlement claimant was allowed to acknowledge newer work")
	}
	if err := rolloutRepository.MarkFailed(ctx, secondClaim, rollbackNow.Add(2*time.Second), rollbackNow.Add(time.Minute), "terminal_test", true); err != nil {
		t.Fatalf("current entitlement claimant could not acknowledge work: %v", err)
	}
	var terminalRolloutState string
	var terminalFailureCount int
	if err := pool.QueryRow(ctx, `SELECT state,failed_count FROM entitlement_catalog_rollouts WHERE id=$1`, leaseRolloutID).Scan(&terminalRolloutState, &terminalFailureCount); err != nil || terminalRolloutState != "failed" || terminalFailureCount != 1 {
		t.Fatalf("terminal entitlement rollout: state=%s failures=%d err=%v", terminalRolloutState, terminalFailureCount, err)
	}
	if _, err := service.Begin(ctx, registration.BeginCommand{Email: " OWNER@example.com ", DisplayName: "Owner Again", AccountName: "Other Labs", Region: "us-east"}); !errors.Is(err, registration.ErrEmailExists) {
		t.Fatalf("duplicate registration error = %v, want ErrEmailExists", err)
	}

	sessionRepository := postgresadapter.NewSessionRepository(pool)
	sessionService, err := sessions.NewService(sessionRepository, ids.RandomGenerator{}, fixedClock{now: now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessionService.IssueForClientWithMethod(ctx, provisioned.User.ID, provisioned.User.SecurityVersion, "PostgreSQL contract browser", sessions.AuthenticationMethodPasskey)
	if err != nil {
		t.Fatalf("issue persistent session: %v", err)
	}
	active, err := sessionService.Active(ctx, provisioned.User.ID, issued.Session.ID)
	if err != nil || len(active) != 1 || !active[0].Current || active[0].ClientLabel != "PostgreSQL contract browser" || active[0].AuthenticationMethod != sessions.AuthenticationMethodPasskey || active[0].AuthenticationAssurance != sessions.AssuranceUserVerifiedCryptographic {
		t.Fatalf("persistent active sessions = %+v, %v", active, err)
	}
	if revoked, err := sessionService.RevokeOwned(ctx, ids.UserID("30000000-0000-4000-8000-000000000003"), issued.Session.ID); err != nil || revoked {
		t.Fatalf("cross-user persistent revoke = %v, %v", revoked, err)
	}
	if err := sessionService.MarkReauthenticated(ctx, provisioned.User.ID, issued.Session.ID); err != nil {
		t.Fatalf("mark persistent session reauthenticated: %v", err)
	}
	refreshedSession, err := sessionService.Authenticate(ctx, issued.Token)
	if err != nil || refreshedSession.Session.AuthenticationMethod != sessions.AuthenticationMethodPasskey || refreshedSession.Session.ReauthenticationMethod != sessions.AuthenticationMethodPassword {
		t.Fatalf("persistent session assurance after password step-up = %+v, %v", refreshedSession.Session, err)
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
	passkeyCipher, err := passkeys.NewCipher(bytes.Repeat([]byte{0x72}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	passkeyRepository, err := postgresadapter.NewPasskeyRepository(pool, passkeyCipher)
	if err != nil {
		t.Fatal(err)
	}
	passkeyUser, err := passkeyRepository.EnsureUser(ctx, provisioned.User.ID, bytes.Repeat([]byte{0x31}, 32), now)
	if err != nil || passkeyUser.Identity.ID != provisioned.User.ID {
		t.Fatalf("ensure persistent passkey user = %+v, %v", passkeyUser, err)
	}
	storedCredential := webauthn.Credential{ID: []byte("persistent-credential-id"), PublicKey: []byte("encrypted-credential-material"), Flags: webauthn.CredentialFlags{UserPresent: true, UserVerified: true}, Authenticator: webauthn.Authenticator{SignCount: 4}}
	if err := passkeyRepository.CreateCredential(ctx, provisioned.User.ID, "PostgreSQL passkey", storedCredential, now, passkeys.MaximumPasskeys); err != nil {
		t.Fatalf("create persistent passkey: %v", err)
	}
	var credentialPlaintext bool
	if err := pool.QueryRow(ctx, `SELECT position($1::bytea in encrypted_credential)>0 FROM passkey_credentials WHERE credential_id=$2`, storedCredential.PublicKey, storedCredential.ID).Scan(&credentialPlaintext); err != nil || credentialPlaintext {
		t.Fatalf("persistent passkey plaintext exposed=%v err=%v", credentialPlaintext, err)
	}
	credentials, err := passkeyRepository.ListCredentials(ctx, provisioned.User.ID)
	if err != nil || len(credentials) != 1 || !bytes.Equal(credentials[0].Credential.PublicKey, storedCredential.PublicKey) {
		t.Fatalf("persistent passkey round trip = %+v, %v", credentials, err)
	}
	if updated, err := passkeyRepository.UpdateCredential(ctx, provisioned.User.ID, storedCredential.ID, 3, storedCredential, passkeys.EventAuthenticated, now.Add(time.Second)); err != nil || updated {
		t.Fatalf("stale passkey counter update = %v, %v", updated, err)
	}
	storedCredential.Authenticator.SignCount = 5
	if updated, err := passkeyRepository.UpdateCredential(ctx, provisioned.User.ID, storedCredential.ID, 4, storedCredential, passkeys.EventAuthenticated, now.Add(time.Second)); err != nil || !updated {
		t.Fatalf("current passkey counter update = %v, %v", updated, err)
	}
	ceremonyID := ids.RandomGenerator{}.New()
	ceremony := passkeys.Ceremony{ID: ceremonyID, Kind: passkeys.CeremonyLogin, Data: webauthn.SessionData{Challenge: "server-only-persistent-challenge"}, CreatedAt: now, ExpiresAt: now.Add(passkeys.CeremonyTTL)}
	if err := passkeyRepository.CreateCeremony(ctx, ceremony); err != nil {
		t.Fatalf("create persistent passkey ceremony: %v", err)
	}
	var ceremonyPlaintext bool
	if err := pool.QueryRow(ctx, `SELECT position(convert_to($1,'UTF8') in encrypted_session_data)>0 FROM passkey_ceremonies WHERE id=$2`, ceremony.Data.Challenge, ceremonyID).Scan(&ceremonyPlaintext); err != nil || ceremonyPlaintext {
		t.Fatalf("persistent ceremony plaintext exposed=%v err=%v", ceremonyPlaintext, err)
	}
	consumed, err := passkeyRepository.ConsumeCeremony(ctx, ceremonyID, passkeys.CeremonyLogin, "", "", now.Add(time.Second))
	if err != nil || consumed.Data.Challenge != ceremony.Data.Challenge {
		t.Fatalf("consume persistent passkey ceremony = %+v, %v", consumed, err)
	}
	if _, err := passkeyRepository.ConsumeCeremony(ctx, ceremonyID, passkeys.CeremonyLogin, "", "", now.Add(time.Second)); !errors.Is(err, passkeys.ErrInvalidCeremony) {
		t.Fatalf("persistent ceremony replay = %v", err)
	}
	if other, err := passkeyRepository.ListCredentials(ctx, ids.UserID("30000000-0000-4000-8000-000000000003")); err != nil || len(other) != 0 {
		t.Fatalf("cross-user passkey list = %+v, %v", other, err)
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

	var ledgerCount, catalogCount, cellCount, routedCellCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM public.spyglass_schema_migrations`).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM catalog_publications WHERE state='published'`).Scan(&catalogCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM cells WHERE state='active'`).Scan(&cellCount); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM cells WHERE route_origin='http://app-api.spyglass-reference.svc.cluster.local'`).Scan(&routedCellCount); err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 32 || catalogCount != 1 || cellCount != 1 || routedCellCount != 1 {
		t.Fatalf("unexpected migrated state: ledger=%d published_catalogs=%d active_cells=%d routed_cells=%d", ledgerCount, catalogCount, cellCount, routedCellCount)
	}
	testAccountIsolation(t, ctx, owner, databaseURL)
	if _, err := owner.Exec(ctx, `UPDATE public.spyglass_schema_migrations SET checksum='\\x00'::bytea WHERE target='global' AND version=1`); err != nil {
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
	if _, err := owner.Exec(ctx, `GRANT USAGE ON SCHEMA spyglass TO `+role+`; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA spyglass TO `+role); err != nil {
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

	testWorkIsolationAndConcurrency(t, ctx, serving, cellPool, accountA, accountB)
	testRouteContextReceipts(t, ctx, owner, databaseURL, cellPool, accountA)
}

func testRouteContextReceipts(t *testing.T, ctx context.Context, owner *pgxpool.Pool, databaseURL string, cellPool *database.CellPool, accountID ids.AccountID) {
	t.Helper()
	repository, err := postgresadapter.NewRouteContextReceiptRepository(cellPool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)
	binding, err := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+string(accountID)+"/work-items", []byte(`{"title":"Bound"}`))
	if err != nil {
		t.Fatal(err)
	}
	claims := routecontext.Claims{Issuer: "router", Audience: routecontext.Audience("cell-us-east-01"), IssuedAt: now.Unix(), ExpiresAt: now.Add(20 * time.Second).Unix(), Authority: routecontext.Authority{RequestID: "60000000-0000-4000-8000-000000000006", AccountID: accountID, ActorKind: "user", ActorID: "40000000-0000-4000-8000-000000000004", Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1}, Binding: binding}
	if err := repository.Consume(ctx, claims, now); err != nil {
		t.Fatalf("consume route context: %v", err)
	}
	if err := repository.Consume(ctx, claims, now); !errors.Is(err, routecontext.ErrReplay) {
		t.Fatalf("replay error = %v", err)
	}
	stale := claims
	stale.Authority.RequestID = "70000000-0000-4000-8000-000000000007"
	stale.Authority.PlacementGeneration = 2
	if err := repository.Consume(ctx, stale, now); !errors.Is(err, routecontext.ErrPlacement) {
		t.Fatalf("stale placement error = %v", err)
	}
	if err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='draining' WHERE account_id=$1`, accountID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	drainingWrite := claims
	drainingWrite.Authority.RequestID = "80000000-0000-4000-8000-000000000008"
	if err := repository.Consume(ctx, drainingWrite, now); !errors.Is(err, routecontext.ErrUnavailable) {
		t.Fatalf("draining write error = %v", err)
	}
	drainingRead := drainingWrite
	drainingRead.Authority.RequestID = "90000000-0000-4000-8000-000000000009"
	drainingRead.Binding, _ = routecontext.Bind(http.MethodGet, "/api/v1/accounts/"+string(accountID)+"/context", nil)
	if err := repository.Consume(ctx, drainingRead, now); err != nil {
		t.Fatalf("draining read: %v", err)
	}
	if err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE spyglass.account_namespaces SET state='active' WHERE account_id=$1`, accountID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	workerRole := "spyglass_route_retention_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+workerRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA spyglass TO `+workerRole+`;
		GRANT SELECT,UPDATE,DELETE ON spyglass.route_context_receipt_cleanup_queue TO `+workerRole+`;
		GRANT SELECT,UPDATE,DELETE ON spyglass.route_context_receipts TO `+workerRole); err != nil {
		t.Fatalf("create route receipt worker role: %v", err)
	}
	workerPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+workerRole)
		return err
	})
	defer func() {
		workerPool.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+workerRole+`; DROP ROLE IF EXISTS `+workerRole)
	}()
	var crossAccountVisible int
	if err := workerPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.route_context_receipts`).Scan(&crossAccountVisible); err != nil || crossAccountVisible != 0 {
		t.Fatalf("route receipt worker bypassed Account RLS: count=%d err=%v", crossAccountVisible, err)
	}
	if err := workerPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&crossAccountVisible); err == nil {
		t.Fatal("route receipt worker read customer Work")
	}
	if err := workerPool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&crossAccountVisible); err == nil {
		t.Fatal("route receipt worker read global Users")
	}
	workerCell, err := database.NewCellPool(workerPool)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := postgresadapter.NewRouteReceiptCleanupRepository(workerPool, workerCell, fixedIDGenerator{"a0000000-0000-4000-8000-00000000000a"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupNow := now.Add(2 * time.Minute)
	job, found, err := cleanup.Claim(ctx, cleanupNow, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim route receipt cleanup found=%t err=%v", found, err)
	}
	concurrent := claims
	concurrent.Authority.RequestID = "b0000000-0000-4000-8000-00000000000b"
	concurrent.IssuedAt = cleanupNow.Unix()
	concurrent.ExpiresAt = cleanupNow.Add(20 * time.Second).Unix()
	if err := repository.Consume(ctx, concurrent, cleanupNow); err != nil {
		t.Fatalf("consume receipt during cleanup lease: %v", err)
	}
	pruned, err := cleanup.Prune(ctx, job, cleanupNow, time.Minute, 100)
	if err != nil || pruned != 2 {
		t.Fatalf("concurrent route receipt cleanup pruned=%d err=%v", pruned, err)
	}
	processor, err := routeretention.NewProcessor(cleanup, fixedClock{now: cleanupNow}, time.Minute, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(ctx)
	if err != nil || !result.Worked || result.Pruned != 0 {
		t.Fatalf("rescheduled route receipt inspection result=%+v err=%v", result, err)
	}
	stats, err := processor.Stats(ctx)
	if err != nil || stats.Scheduled != 1 || stats.Ready != 0 {
		t.Fatalf("route receipt cleanup stats after concurrent insert=%+v err=%v", stats, err)
	}
	processor, err = routeretention.NewProcessor(cleanup, fixedClock{now: cleanupNow.Add(2 * time.Minute)}, time.Minute, time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	result, err = processor.ProcessOne(ctx)
	if err != nil || !result.Worked || result.Pruned != 1 {
		t.Fatalf("final route receipt cleanup result=%+v err=%v", result, err)
	}
	stats, err = processor.Stats(ctx)
	if err != nil || stats.Scheduled != 0 {
		t.Fatalf("final route receipt cleanup stats=%+v err=%v", stats, err)
	}
	var remaining int
	if err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.route_context_receipts WHERE account_id=$1`, accountID).Scan(&remaining)
	}); err != nil || remaining != 0 {
		t.Fatalf("remaining route receipts=%d err=%v", remaining, err)
	}
}

func testWorkIsolationAndConcurrency(t *testing.T, ctx context.Context, rawPool *pgxpool.Pool, cellPool *database.CellPool, accountA, accountB ids.AccountID) {
	t.Helper()
	repository, err := postgresadapter.NewWorkRepository(cellPool, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	actor := workdomain.Actor{Kind: workdomain.ActorUser, ID: "40000000-0000-4000-8000-000000000004"}
	itemID := ids.WorkItemID("50000000-0000-4000-8000-000000000005")
	draft, err := workdomain.NewDraft(workdomain.Draft{
		ID: itemID, AccountID: accountA, Kind: workdomain.KindTicket, Title: "Reconcile month-end close",
		Priority: workdomain.PriorityUrgent, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(itemID),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.Create(ctx, draft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor, Reason: "integration contract", CorrelationID: "work-isolation-contract", At: now})
	if err != nil {
		t.Fatalf("create account A work item: %v", err)
	}
	if created.Number != 1 || created.Version != 1 || created.State != workdomain.StateOpen {
		t.Fatalf("created work item = %+v", created)
	}

	if _, err := repository.Get(ctx, accountB, itemID); !errors.Is(err, workapp.ErrNotFound) {
		t.Fatalf("account B guessed account A work ID: %v", err)
	}
	page, err := repository.List(ctx, accountA, workapp.ListQuery{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list account A work = %+v, %v", page, err)
	}
	other, err := repository.List(ctx, accountB, workapp.ListQuery{Limit: 10})
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("list account B work = %+v, %v", other, err)
	}
	summary, err := repository.Summary(ctx, accountA)
	if err != nil || summary.Active != 1 || summary.Urgent != 1 {
		t.Fatalf("account A summary = %+v, %v", summary, err)
	}

	started, err := created.Transition(workdomain.TransitionCommand{To: workdomain.StateInProgress, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: created.Version, At: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	started, err = repository.Update(ctx, started, created.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "start the close", CorrelationID: "work-transition-contract", At: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("start work item: %v", err)
	}
	stale, err := created.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityUser, UserID: ids.UserID(actor.ID)}, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: created.Version, At: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(ctx, stale, created.Version, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor, Reason: "stale writer", CorrelationID: "work-concurrency-contract", At: now.Add(2 * time.Minute)}); !errors.Is(err, workapp.ErrConflict) {
		t.Fatalf("stale work update error = %v", err)
	}
	loaded, err := repository.Get(ctx, accountA, itemID)
	if err != nil || loaded.Version != started.Version || loaded.State != workdomain.StateInProgress {
		t.Fatalf("load winning work state = %+v, %v", loaded, err)
	}

	done, err := started.Transition(workdomain.TransitionCommand{To: workdomain.StateDone, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: started.Version, At: now.Add(3 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	done, err = repository.Update(ctx, done, started.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "finish before reconciliation", CorrelationID: "work-release-queue-contract", At: now.Add(3 * time.Minute)})
	if err != nil {
		t.Fatalf("complete work item: %v", err)
	}
	firstQueue, err := postgresadapter.NewWorkReleaseQueueRepository(rawPool, cellPool, fixedIDGenerator{"60000000-0000-4000-8000-000000000006"})
	if err != nil {
		t.Fatal(err)
	}
	first, found, err := firstQueue.Claim(ctx, now.Add(4*time.Minute), time.Minute)
	if err != nil || !found || first.Attempt != 1 {
		t.Fatalf("first Work release claim = %+v found=%v err=%v", first, found, err)
	}
	if _, found, err := firstQueue.Claim(ctx, now.Add(4*time.Minute), time.Minute); err != nil || found {
		t.Fatalf("active lease was concurrently claimable: found=%v err=%v", found, err)
	}
	secondQueue, _ := postgresadapter.NewWorkReleaseQueueRepository(rawPool, cellPool, fixedIDGenerator{"70000000-0000-4000-8000-000000000007"})
	second, found, err := secondQueue.Claim(ctx, now.Add(6*time.Minute), time.Minute)
	if err != nil || !found || second.Attempt != 2 || second.LeaseID == first.LeaseID {
		t.Fatalf("reclaimed Work release = %+v found=%v err=%v", second, found, err)
	}

	reopened, err := done.Transition(workdomain.TransitionCommand{To: workdomain.StateOpen, Role: accounts.RoleOwner, Actor: actor, Reason: "new evidence requires reopening", ExpectedVersion: done.Version, At: now.Add(7 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	newReservation := "80000000-0000-4000-8000-000000000008"
	reopened, err = reopened.WithReopenedCapacity(newReservation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(ctx, reopened, done.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "new evidence requires reopening", CorrelationID: "work-reopen-contract", At: now.Add(7 * time.Minute)}); err != nil {
		t.Fatalf("reopen work item: %v", err)
	}
	if err := firstQueue.Complete(ctx, first, now.Add(8*time.Minute)); !errors.Is(err, workreconciliation.ErrLeaseLost) {
		t.Fatalf("stale release lease completion = %v", err)
	}
	if err := secondQueue.Complete(ctx, second, now.Add(8*time.Minute)); err != nil {
		t.Fatalf("complete reclaimed Work release: %v", err)
	}
	loaded, err = repository.Get(ctx, accountA, itemID)
	if err != nil || loaded.State != workdomain.StateOpen || loaded.CapacityReservationID != newReservation || loaded.CapacityReleasedAt != nil {
		t.Fatalf("old release corrupted reopened Work capacity: item=%+v err=%v", loaded, err)
	}
	recanceled, err := loaded.Transition(workdomain.TransitionCommand{To: workdomain.StateCanceled, Role: accounts.RoleOwner, Actor: actor, Reason: "close reopened work", ExpectedVersion: loaded.Version, At: now.Add(9 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(ctx, recanceled, loaded.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "close reopened work", CorrelationID: "work-recancel-contract", At: now.Add(9 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCapacityReleased(ctx, accountA, itemID, newReservation, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("synchronous release checkpoint: %v", err)
	}
	loaded, err = repository.Get(ctx, accountA, itemID)
	if err != nil || loaded.CapacityReleasedAt == nil {
		t.Fatalf("synchronous capacity checkpoint=%+v err=%v", loaded, err)
	}
	stats, err := secondQueue.Stats(ctx, now.Add(8*time.Minute))
	if err != nil || stats.Pending != 0 || stats.Processing != 0 || stats.DeadLetter != 0 {
		t.Fatalf("release queue stats=%+v err=%v", stats, err)
	}
}

func testRoutedWorkCommandAdmission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, databaseURL string, userID ids.UserID, accountID ids.AccountID, cellID ids.CellID, entitlementVersion uint64, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2) ON CONFLICT (account_id) DO NOTHING`, accountID, now); err != nil {
		t.Fatalf("seed command Account namespace: %v", err)
	}
	cellRole := "spyglass_command_cell_" + randomSuffix(t)
	globalRole := "spyglass_admission_global_" + randomSuffix(t)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+cellRole+` NOLOGIN; CREATE ROLE `+globalRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA spyglass TO `+cellRole+`;
		GRANT SELECT ON spyglass.account_namespaces,spyglass.work_items TO `+cellRole+`;
		GRANT SELECT,INSERT,UPDATE ON spyglass.work_item_number_counters,spyglass.work_items,spyglass.work_item_events,spyglass.work_capacity_release_queue TO `+cellRole+`;
		GRANT SELECT ON accounts,memberships,entitlement_snapshots TO `+globalRole+`;
		GRANT SELECT,INSERT,UPDATE ON entitlement_usage_counters,entitlement_usage_reservations TO `+globalRole+`;
		GRANT EXECUTE ON FUNCTION spyglass_lock_account_entitlement_version(uuid) TO `+globalRole); err != nil {
		t.Fatalf("create routed Work command roles: %v", err)
	}
	cellPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+cellRole)
		return err
	})
	globalPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+globalRole)
		return err
	})
	defer func() {
		cellPool.Close()
		globalPool.Close()
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+cellRole+`; DROP OWNED BY `+globalRole+`; DROP ROLE IF EXISTS `+cellRole+`; DROP ROLE IF EXISTS `+globalRole)
	}()
	var forbidden int
	if err := cellPool.QueryRow(ctx, `SELECT count(*) FROM memberships`).Scan(&forbidden); err == nil {
		t.Fatal("cell command credential read global Memberships")
	}
	if err := globalPool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&forbidden); err == nil {
		t.Fatal("admission credential read global Users")
	}
	if err := globalPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&forbidden); err == nil {
		t.Fatal("admission credential read cell Work")
	}

	globalAuthorizer, _ := access.NewAuthorizer(postgresadapter.NewAccessRepository(globalPool))
	usageService, _ := usageadmission.NewService(globalAuthorizer, postgresadapter.NewUsageAdmissionRepository(globalPool), ids.RandomGenerator{}, fixedClock{now: now})
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := routecontext.NewSigner("spyglass-app-router", "current", key, 20*time.Second, fixedClock{now: now})
	verifier, _ := routecontext.NewVerifier("spyglass-app-router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, fixedClock{now: now})
	admissionServer, err := admissiontransport.New(usageService, map[ids.CellID]admissiontransport.Verifier{cellID: verifier}, slog.New(slog.NewTextHandler(io.Discard, nil)), admissiontransport.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(admissionServer.Handler())
	defer httpServer.Close()
	capacityClient, _ := admissionhttp.New(httpServer.URL, true, nil)
	cell, _ := database.NewCellPool(cellPool)
	workRepository, _ := postgresadapter.NewWorkRepository(cell, ids.RandomGenerator{})
	workService, _ := workapp.NewService(routeaccess.NewAuthorizer(), capacityClient, workRepository, fixedClock{now: now})
	packageAccess := &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "enabled", Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]routecontext.LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}
	proofContext := func(operationID, routeID, body string) context.Context {
		request, _ := http.NewRequest(http.MethodPost, "https://cell.test/api/v1/accounts/"+string(accountID)+"/work-items", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", operationID)
		binding, _ := routecontext.BindRequest(request, []byte(body))
		authority := routecontext.Authority{RequestID: routeID, OperationID: operationID, AccountID: accountID, ActorKind: "user", ActorID: string(userID), Role: "owner", CellID: cellID, PlacementGeneration: 1, EntitlementVersion: entitlementVersion, PackageAccess: packageAccess}
		token, err := signer.Issue(routecontext.Audience(cellID), authority, binding)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := verifier.Verify(token, binding)
		if err != nil {
			t.Fatal(err)
		}
		requestContext := routecontext.WithClaims(ctx, claims)
		return routecontext.WithProof(requestContext, routecontext.Proof{Token: token, Binding: binding})
	}
	operationID := "81000000-0000-4000-8000-000000000001"
	actor := access.Actor{UserID: userID}
	created, err := workService.Create(proofContext(operationID, "81000000-0000-4000-8000-000000000003", `{"title":"Admitted Work"}`), workapp.CreateCommand{Actor: actor, AccountID: accountID, RequestID: operationID, Kind: workdomain.KindTicket, Title: "Admitted Work", Priority: workdomain.PriorityHigh, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: string(userID)}}, CorrelationID: operationID})
	if err != nil || created.ID != ids.WorkItemID(operationID) || created.CapacityReservationID != operationID {
		t.Fatalf("routed admitted Work=%+v err=%v", created, err)
	}
	var reservationState string
	if err := pool.QueryRow(ctx, `SELECT state FROM entitlement_usage_reservations WHERE account_id=$1 AND request_id=$2`, accountID, operationID).Scan(&reservationState); err != nil || reservationState != "active" {
		t.Fatalf("admitted reservation state=%q err=%v", reservationState, err)
	}

	rejectedID := "81000000-0000-4000-8000-000000000002"
	_, err = workService.Create(proofContext(rejectedID, "81000000-0000-4000-8000-000000000004", `{"title":"Rejected Work"}`), workapp.CreateCommand{Actor: actor, AccountID: accountID, RequestID: rejectedID, ParentID: "82000000-0000-4000-8000-000000000005", Kind: workdomain.KindTodo, Title: "Rejected Work", Priority: workdomain.PriorityNormal, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: string(userID)}}, CorrelationID: rejectedID})
	if !errors.Is(err, workapp.ErrConstraint) {
		t.Fatalf("definitive cell rejection=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM entitlement_usage_reservations WHERE account_id=$1 AND request_id=$2`, accountID, rejectedID).Scan(&reservationState); err != nil || reservationState != "released" {
		t.Fatalf("compensated reservation state=%q err=%v", reservationState, err)
	}
}

func testWorkReleaseReconciliation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, databaseURL string, accountID ids.AccountID, entitlementVersion uint64, now time.Time) {
	t.Helper()
	reservationID := "72000000-0000-4000-8000-000000000001"
	usage := postgresadapter.NewUsageAdmissionRepository(pool)
	if _, err := usage.Reserve(ctx, usageadmission.PersistCommand{ID: ids.RandomGenerator{}.New(), AccountID: accountID, RequestID: reservationID, PackageCode: catalog.PackageWork, LimitCode: workapp.ActiveItems, Amount: 1, Maximum: 100, ExpectedEntitlementVersion: entitlementVersion, Now: now}); err != nil {
		t.Fatalf("reserve Work reconciliation capacity: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2) ON CONFLICT (account_id) DO NOTHING`, accountID, now); err != nil {
		t.Fatalf("seed reconciled Account namespace: %v", err)
	}
	cell, _ := database.NewCellPool(pool)
	workRepository, _ := postgresadapter.NewWorkRepository(cell, ids.RandomGenerator{})
	actor := workdomain.Actor{Kind: workdomain.ActorUser, ID: "73000000-0000-4000-8000-000000000003"}
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(reservationID), AccountID: accountID, Kind: workdomain.KindTodo, Title: "Verify capacity reconciliation", Priority: workdomain.PriorityNormal, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: reservationID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := workRepository.Create(ctx, draft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor, CorrelationID: "release-reconciliation", At: now})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := item.Transition(workdomain.TransitionCommand{To: workdomain.StateCanceled, Role: accounts.RoleOwner, Actor: actor, Reason: "no longer required", ExpectedVersion: item.Version, At: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workRepository.Update(ctx, canceled, item.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "no longer required", CorrelationID: "release-reconciliation", At: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	cellRole := "spyglass_work_cell_" + randomSuffix(t)
	globalRole := "spyglass_work_global_" + randomSuffix(t)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+cellRole+` NOLOGIN; CREATE ROLE `+globalRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA spyglass TO `+cellRole+`;
		GRANT SELECT,UPDATE,DELETE ON spyglass.work_capacity_release_queue TO `+cellRole+`;
		GRANT SELECT,UPDATE ON spyglass.work_items TO `+cellRole+`;
		GRANT SELECT ON spyglass.account_namespaces TO `+cellRole+`;
		GRANT SELECT ON accounts TO `+globalRole+`;
		GRANT SELECT,UPDATE ON entitlement_usage_counters,entitlement_usage_reservations TO `+globalRole); err != nil {
		t.Fatalf("create Work reconciler roles: %v", err)
	}
	cellWorkerPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+cellRole)
		return err
	})
	globalWorkerPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+globalRole)
		return err
	})
	defer func() {
		cellWorkerPool.Close()
		globalWorkerPool.Close()
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+cellRole+`; DROP OWNED BY `+globalRole+`; DROP ROLE IF EXISTS `+cellRole+`; DROP ROLE IF EXISTS `+globalRole)
	}()
	var crossScopeCount int
	if err := cellWorkerPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&crossScopeCount); err != nil || crossScopeCount != 0 {
		t.Fatalf("cell reconciler bypassed Account RLS: count=%d err=%v", crossScopeCount, err)
	}
	if err := cellWorkerPool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&crossScopeCount); err == nil {
		t.Fatal("cell reconciler read global Users")
	}
	if err := globalWorkerPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_capacity_release_queue`).Scan(&crossScopeCount); err == nil {
		t.Fatal("global release credential read cell reconciliation outbox")
	}
	workerCell, _ := database.NewCellPool(cellWorkerPool)
	queue, _ := postgresadapter.NewWorkReleaseQueueRepository(cellWorkerPool, workerCell, fixedIDGenerator{"74000000-0000-4000-8000-000000000004"})
	processor, err := workreconciliation.NewProcessor(queue, postgresadapter.NewUsageReleaseRepository(globalWorkerPool), fixedClock{now: now.Add(2 * time.Minute)}, time.Minute, 12)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := processor.ProcessOne(ctx); err != nil || !worked {
		t.Fatalf("process Work release: worked=%v err=%v", worked, err)
	}
	reservation, err := postgresadapter.NewUsageReleaseRepository(pool).Release(ctx, accountID, reservationID, now.Add(3*time.Minute))
	if err != nil || reservation.State != usageadmission.ReservationReleased {
		t.Fatalf("idempotent reconciled capacity=%+v err=%v", reservation, err)
	}
	loaded, err := workRepository.Get(ctx, accountID, item.ID)
	if err != nil || loaded.CapacityReleasedAt == nil {
		t.Fatalf("reconciled Work checkpoint=%+v err=%v", loaded, err)
	}
	var queueState string
	if err := pool.QueryRow(ctx, `SELECT processing_state FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID).Scan(&queueState); err != nil || queueState != "completed" {
		t.Fatalf("Work release queue state=%q err=%v", queueState, err)
	}

	operatorNow := now.Add(4 * time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET
		processing_state='dead_letter',attempt_count=12,next_attempt_at=NULL,lease_id=NULL,lease_expires_at=NULL,
		last_attempt_at=$4,last_error_code='global_release_unavailable',completed_at=NULL
		WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID, operatorNow); err != nil {
		t.Fatalf("seed Work release dead letter: %v", err)
	}
	operatorRole := "spyglass_work_operator_" + randomSuffix(t)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+operatorRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public,spyglass TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_inspect_work_capacity_release_dead_letters(uuid,text,text,text,integer) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_requeue_work_capacity_release_dead_letter(uuid,uuid,uuid,uuid,text,text,text) TO `+operatorRole); err != nil {
		t.Fatalf("create Work release operator role: %v", err)
	}
	operatorPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+operatorRole)
		return err
	})
	defer func() {
		operatorPool.Close()
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+operatorRole+`; DROP ROLE IF EXISTS `+operatorRole)
	}()
	if err := operatorPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_capacity_release_queue`).Scan(&crossScopeCount); err == nil {
		t.Fatal("Work release operator directly read the technical queue")
	}
	if err := operatorPool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items`).Scan(&crossScopeCount); err == nil {
		t.Fatal("Work release operator read customer Work")
	}
	if _, err := operatorPool.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET processing_state='pending'`); err == nil {
		t.Fatal("Work release operator directly mutated the technical queue")
	}
	operatorRepository := postgresadapter.NewWorkReleaseAdminRepository(operatorPool)
	operatorService, _ := workreleaseadmin.NewService(operatorRepository, fixedIDGenerator{"75000000-0000-4000-8000-000000000005"})
	inspection, err := operatorService.Inspect(ctx, 10, "release-operator@example.com", "verify global database recovery", "integration")
	if err != nil || inspection.AuditBatchID != "75000000-0000-4000-8000-000000000005" || len(inspection.DeadLetters) != 1 || inspection.DeadLetters[0].AttemptCount != 12 || inspection.DeadLetters[0].LastErrorCode != "global_release_unavailable" {
		t.Fatalf("inspect Work release dead letters=%+v err=%v", inspection, err)
	}
	target := inspection.DeadLetters[0].Target
	operatorService, _ = workreleaseadmin.NewService(operatorRepository, fixedIDGenerator{"76000000-0000-4000-8000-000000000006"})
	requeueResult, err := operatorService.Requeue(ctx, target, "release-operator@example.com", "global database service is healthy", "integration")
	if err != nil || requeueResult.AuditBatchID != "76000000-0000-4000-8000-000000000006" || requeueResult.DeadLetter.AttemptCount != 12 || requeueResult.DeadLetter.NextAttemptAt == nil {
		t.Fatalf("requeue Work release=%+v err=%v", requeueResult, err)
	}
	var attempts int
	var nextAttempt *time.Time
	var lastError *string
	if err := pool.QueryRow(ctx, `SELECT processing_state,attempt_count,next_attempt_at,last_error_code FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID).Scan(&queueState, &attempts, &nextAttempt, &lastError); err != nil || queueState != "pending" || attempts != 0 || nextAttempt == nil || lastError != nil {
		t.Fatalf("requeued state=%q attempts=%d next=%v last_error=%v err=%v", queueState, attempts, nextAttempt, lastError, err)
	}
	var inspectedEvents, requeuedEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='inspected'),count(*) FILTER (WHERE action='requeued') FROM spyglass.work_capacity_release_operator_events WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID).Scan(&inspectedEvents, &requeuedEvents); err != nil || inspectedEvents != 1 || requeuedEvents != 1 {
		t.Fatalf("Work release operator audit inspect=%d requeue=%d err=%v", inspectedEvents, requeuedEvents, err)
	}
	if _, err := operatorService.Requeue(ctx, target, "release-operator@example.com", "retry duplicate operator command", "integration"); !errors.Is(err, workreleaseadmin.ErrStateConflict) {
		t.Fatalf("duplicate Work release requeue=%v", err)
	}
	emptyService, _ := workreleaseadmin.NewService(operatorRepository, fixedIDGenerator{"77000000-0000-4000-8000-000000000007"})
	if empty, err := emptyService.Inspect(ctx, 10, "release-operator@example.com", "verify dead letter queue drained", "integration"); err != nil || len(empty.DeadLetters) != 0 {
		t.Fatalf("empty Work release inspection=%+v err=%v", empty, err)
	}
	var emptyAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_capacity_release_operator_events WHERE batch_id=$1 AND action='inspected' AND account_id IS NULL`, "77000000-0000-4000-8000-000000000007").Scan(&emptyAudit); err != nil || emptyAudit != 1 {
		t.Fatalf("empty Work release inspection audit=%d err=%v", emptyAudit, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM spyglass.work_capacity_release_operator_events WHERE batch_id=$1`, "76000000-0000-4000-8000-000000000006"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("Work release operator audit deletion=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET processing_state='completed',attempt_count=1,next_attempt_at=NULL,lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL,completed_at=$4 WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID, operatorNow); err != nil {
		t.Fatalf("seed completed Work release retention row: %v", err)
	}
	pruned, err := queue.PruneCompleted(ctx, operatorNow.Add(time.Minute), 10)
	if err != nil || pruned != 1 {
		t.Fatalf("prune completed Work releases=%d err=%v", pruned, err)
	}
	var retainedQueueRows, retainedAuditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID).Scan(&retainedQueueRows); err != nil || retainedQueueRows != 0 {
		t.Fatalf("retained completed Work release rows=%d err=%v", retainedQueueRows, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_capacity_release_operator_events WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, accountID, item.ID, reservationID).Scan(&retainedAuditRows); err != nil || retainedAuditRows != 2 {
		t.Fatalf("retained Work release operator audit rows=%d err=%v", retainedAuditRows, err)
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
	ownership    accountmembers.OwnershipTransferNotice
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
func (sender *captureNotifications) SendOwnershipTransfer(_ context.Context, message accountmembers.OwnershipTransferNotice) error {
	sender.ownership = message
	return nil
}

type entitlementProcessor interface {
	ProcessOne(context.Context) (bool, error)
}

func processEntitlementTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, processor entitlementProcessor, accountID ids.AccountID, target uint64) {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		var current uint64
		if err := pool.QueryRow(ctx, `SELECT last_catalog_reconciled_version FROM accounts WHERE id=$1`, accountID).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current == target {
			return
		}
		if _, err := processor.ProcessOne(ctx); err != nil {
			t.Fatalf("process entitlement rollout: %v", err)
		}
	}
	t.Fatalf("account %s did not reconcile to Catalog %d", accountID, target)
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type fixedIDGenerator struct{ value string }

func (generator fixedIDGenerator) New() string { return generator.value }

type staticPasswordHasher struct{}

func (staticPasswordHasher) Hash(string) (string, error) { return "$argon2id$integration-test", nil }
