package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/application/routecanary"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	routertransport "github.com/tinfoyle/spyglass-engine/internal/transport/approuter"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestTwoCellPostgresRoutingIsolationAndMoveFailureContracts(t *testing.T) {
	adminURL := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	globalURL, cleanupGlobal := createDatabase(t, ctx, adminURL)
	defer cleanupGlobal()
	cellAURL, cleanupCellA := createDatabase(t, ctx, adminURL)
	defer cleanupCellA()
	cellBURL, cleanupCellB := createDatabase(t, ctx, adminURL)
	defer cleanupCellB()

	global := openPool(t, ctx, globalURL, nil)
	defer global.Close()
	cellAOwner := openPool(t, ctx, cellAURL, nil)
	defer cellAOwner.Close()
	cellBOwner := openPool(t, ctx, cellBURL, nil)
	defer cellBOwner.Close()
	if _, err := migrations.Apply(ctx, global, migrations.Global); err != nil {
		t.Fatalf("migrate global control database: %v", err)
	}
	for name, pool := range map[string]*pgxpool.Pool{"cell A": cellAOwner, "cell B": cellBOwner} {
		if _, err := migrations.Apply(ctx, pool, migrations.Cell); err != nil {
			t.Fatalf("migrate %s database: %v", name, err)
		}
	}

	const (
		cellAID    = ids.CellID("cell-us-east-01")
		cellBID    = ids.CellID("cell-us-west-01")
		userID     = ids.UserID("91000000-0000-4000-8000-000000000001")
		accountAID = ids.AccountID("92000000-0000-4000-8000-000000000002")
		accountBID = ids.AccountID("93000000-0000-4000-8000-000000000003")
	)
	clock := &routingTestClock{now: time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cellAHandler, closeCellA := newRoutingCell(t, ctx, cellAOwner, cellAURL, cellAID, accountAID, key, clock, logger)
	defer closeCellA()
	cellBHandler, closeCellB := newRoutingCell(t, ctx, cellBOwner, cellBURL, cellBID, accountBID, key, clock, logger)
	defer closeCellB()
	transport := newRoutingCellTransport(map[string]http.Handler{"cell-a.test": cellAHandler, "cell-b.test": cellBHandler})

	seedTwoCellGlobalControl(t, ctx, global, clock.Now(), userID, accountAID, accountBID, cellAID, cellBID)
	sessionService, err := sessions.NewService(postgresadapter.NewSessionRepository(global), fixedIDGenerator{"96000000-0000-4000-8000-000000000006"}, clock, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessionService.Issue(ctx, userID, 1)
	if err != nil {
		t.Fatalf("issue routed test session: %v", err)
	}
	authorizer, err := access.NewAuthorizer(postgresadapter.NewAccessRepository(global))
	if err != nil {
		t.Fatal(err)
	}
	directory, err := accountdirectory.NewCache(postgresadapter.NewAccountDirectoryRepository(global), clock, accountdirectory.Config{TTL: 30 * time.Second, Capacity: 10, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := routecontext.NewSigner("spyglass-app-router", "current", key, 20*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	router, err := routertransport.New(sessionService, authorizer, directory, signer, ids.RandomGenerator{}, routertransport.Config{SessionCookieName: "spyglass_session", TrustedOrigins: []string{"https://app.test"}, Transport: transport}, logger)
	if err != nil {
		t.Fatal(err)
	}

	responseA := routeAccountContext(router.Handler(), issued.Token, accountAID)
	assertRoutedCell(t, responseA, accountAID, cellAID, 1)
	responseB := routeAccountContext(router.Handler(), issued.Token, accountBID)
	assertRoutedCell(t, responseB, accountBID, cellBID, 1)
	if transport.Hits("cell-a.test") != 1 || transport.Hits("cell-b.test") != 1 {
		t.Fatalf("initial cell hits: A=%d B=%d", transport.Hits("cell-a.test"), transport.Hits("cell-b.test"))
	}
	assertPhysicalCellPlacement(t, ctx, cellAOwner, accountAID, accountBID)
	assertPhysicalCellPlacement(t, ctx, cellBOwner, accountBID, accountAID)

	tokenA := transport.LastToken("cell-a.test")
	attack := directCellRequest(cellBHandler, accountAID, tokenA)
	if attack.Code != http.StatusUnauthorized || !strings.Contains(attack.Body.String(), `"code":"invalid_route_context"`) {
		t.Fatalf("wrong-cell token attack status=%d body=%s", attack.Code, attack.Body.String())
	}
	attack = directCellRequest(cellAHandler, accountBID, tokenA)
	if attack.Code != http.StatusUnauthorized || !strings.Contains(attack.Body.String(), `"code":"invalid_route_context"`) {
		t.Fatalf("cross-Account path attack status=%d body=%s", attack.Code, attack.Body.String())
	}
	if countReceipts(t, ctx, cellAOwner) != 1 || countReceipts(t, ctx, cellBOwner) != 1 {
		t.Fatalf("rejected attacks persisted replay receipts: A=%d B=%d", countReceipts(t, ctx, cellAOwner), countReceipts(t, ctx, cellBOwner))
	}
	canary, err := routecanary.Probe(ctx, routecanary.Config{
		Origin: "https://cell-a.test", CellID: cellAID, AccountID: accountAID,
		PlacementGeneration: 1, EntitlementVersion: 1, Issuer: "spyglass-app-router",
		KeyID: "current", SigningKey: key, Timeout: 2 * time.Second,
		Transport: transport, Clock: clock, IDs: fixedIDGenerator{"97000000-0000-4000-8000-000000000007"},
	})
	if err != nil || canary.CellID != cellAID || canary.KeyID != "current" || countReceipts(t, ctx, cellAOwner) != 2 {
		t.Fatalf("real cell route canary=%+v receipts=%d err=%v", canary, countReceipts(t, ctx, cellAOwner), err)
	}
	var canaryActorKind, canaryActorID string
	if err := cellAOwner.QueryRow(ctx, `SELECT actor_kind,actor_id FROM spyglass.route_context_receipts WHERE account_id=$1 AND request_id=$2`, accountAID, "97000000-0000-4000-8000-000000000007").Scan(&canaryActorKind, &canaryActorID); err != nil || canaryActorKind != "workload" || canaryActorID != routecanary.ActorID {
		t.Fatalf("persisted route canary actor kind=%q id=%q err=%v", canaryActorKind, canaryActorID, err)
	}

	moveAccountBetweenCells(t, ctx, global, cellAOwner, cellBOwner, accountAID, cellBID, clock.Now())
	moved := routeAccountContext(router.Handler(), issued.Token, accountAID)
	assertRoutedCell(t, moved, accountAID, cellBID, 2)
	if transport.Hits("cell-a.test") != 2 || transport.Hits("cell-b.test") != 2 {
		t.Fatalf("moved Account was sent to the wrong cell: A=%d B=%d", transport.Hits("cell-a.test"), transport.Hits("cell-b.test"))
	}
	stale := directCellRequest(cellAHandler, accountAID, tokenA)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"stale_route"`) {
		t.Fatalf("pre-move token status=%d body=%s", stale.Code, stale.Body.String())
	}

	transport.SetFailed("cell-b.test", true)
	cellAHits, cellBHits := transport.Hits("cell-a.test"), transport.Hits("cell-b.test")
	unavailable := routeAccountContext(router.Handler(), issued.Token, accountAID)
	if unavailable.Code != http.StatusBadGateway || !strings.Contains(unavailable.Body.String(), `"code":"cell_unavailable"`) {
		t.Fatalf("assigned-cell failure status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	if transport.Hits("cell-a.test") != cellAHits || transport.Hits("cell-b.test") != cellBHits+2 {
		t.Fatalf("router guessed a fallback cell: before A=%d B=%d after A=%d B=%d", cellAHits, cellBHits, transport.Hits("cell-a.test"), transport.Hits("cell-b.test"))
	}
	if stats := router.TransportStats(); stats.RetryAttempts != 1 || stats.RetryRecovered != 0 || stats.RequestsFailed != 1 {
		t.Fatalf("assigned-cell failure stats=%+v", stats)
	}
	transport.SetFailed("cell-b.test", false)
	assertRoutedCell(t, routeAccountContext(router.Handler(), issued.Token, accountAID), accountAID, cellBID, 2)

	if _, err := global.Exec(ctx, `UPDATE cells SET state='disabled' WHERE id=$1`, cellBID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * time.Second)
	cellAHits, cellBHits = transport.Hits("cell-a.test"), transport.Hits("cell-b.test")
	unroutable := routeAccountContext(router.Handler(), issued.Token, accountAID)
	if unroutable.Code != http.StatusServiceUnavailable || !strings.Contains(unroutable.Body.String(), `"code":"routing_unavailable"`) {
		t.Fatalf("disabled-cell refresh status=%d body=%s", unroutable.Code, unroutable.Body.String())
	}
	if transport.Hits("cell-a.test") != cellAHits || transport.Hits("cell-b.test") != cellBHits {
		t.Fatal("router contacted a cell after refreshed placement became unroutable")
	}
	if _, err := global.Exec(ctx, `UPDATE cells SET state='active' WHERE id=$1`, cellBID); err != nil {
		t.Fatal(err)
	}
	assertRoutedCell(t, routeAccountContext(router.Handler(), issued.Token, accountAID), accountAID, cellBID, 2)
}

func testPostgresURL(t *testing.T) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("SPYGLASS_POSTGRES_TEST_URL"))
	if value == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	return value
}

func newRoutingCell(t *testing.T, ctx context.Context, owner *pgxpool.Pool, databaseURL string, cellID ids.CellID, accountID ids.AccountID, key []byte, clock routecontext.Clock, logger *slog.Logger) (http.Handler, func()) {
	t.Helper()
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, accountID, clock.Now()); err != nil {
		t.Fatalf("seed %s Account namespace: %v", cellID, err)
	}
	role := "spyglass_two_cell_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT ON spyglass.account_namespaces TO `+role+`;
		GRANT SELECT,INSERT ON spyglass.route_context_receipts TO `+role); err != nil {
		t.Fatalf("create %s serving role: %v", cellID, err)
	}
	serving := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	cell, err := database.NewCellPool(serving)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := postgresadapter.NewRouteContextReceiptRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := routecontext.NewVerifier("spyglass-app-router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	if err != nil {
		t.Fatal(err)
	}
	acceptor, err := routecontext.NewAcceptor(verifier, receipts, clock)
	if err != nil {
		t.Fatal(err)
	}
	server, err := cellapi.New(acceptor, logger, cellapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), func() {
		serving.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}
}

func seedTwoCellGlobalControl(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time, userID ids.UserID, accountAID, accountBID ids.AccountID, cellAID, cellBID ids.CellID) {
	t.Helper()
	packages := []byte(`[{"code":"work","version":1,"mode":"enabled","limits":{"active_items":100},"limit_policies":{"active_items":{"kind":"capacity","combine":"maximum"}},"sources":["subscription"]}]`)
	hash := sha256.Sum256(packages)
	var catalogVersion uint64
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM catalog_publications WHERE state='published'`).Scan(&catalogVersion); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, cell := range []struct {
		id, region, origin string
	}{{string(cellAID), "us-east", "http://cell-a.test"}, {string(cellBID), "us-west", "http://cell-b.test"}} {
		if _, err := tx.Exec(ctx, `INSERT INTO cells (id,region,state,assigned_accounts,soft_account_limit,created_at,route_origin) VALUES ($1,$2,'active',1,1000,$3,$4)`, cell.id, cell.region, now, cell.origin); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users (id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'two-cell@example.com','Two Cell Operator','active',$2,1,$2)`, userID, now); err != nil {
		t.Fatal(err)
	}
	for index, account := range []struct {
		id     ids.AccountID
		slug   string
		name   string
		cell   ids.CellID
		region string
	}{{accountAID, "atlantic-operations", "Atlantic Operations", cellAID, "us-east"}, {accountBID, "pacific-operations", "Pacific Operations", cellBID, "us-west"}} {
		if _, err := tx.Exec(ctx, `INSERT INTO accounts (id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,created_by_user_id,created_at) VALUES ($1,$2,$3,'paid','active',$4,1,1,$5,$6,$7)`, account.id, account.slug, account.name, account.cell, catalogVersion, userID, now); err != nil {
			t.Fatal(err)
		}
		membershipID := ids.MembershipID([]string{"94000000-0000-4000-8000-000000000004", "95000000-0000-4000-8000-000000000005"}[index])
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (id,account_id,user_id,role,state,version,created_at) VALUES ($1,$2,$3,'owner','active',1,$4)`, membershipID, account.id, userID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO entitlement_snapshots (account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($1,1,$2,$3,$4,$5)`, account.id, catalogVersion, now, hash[:], packages); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_directory (account_id,cell_id,placement_generation,state,data_region,updated_at) VALUES ($1,$2,1,'active',$3,$4)`, account.id, account.cell, account.region, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func routeAccountContext(handler http.Handler, token string, accountID ids.AccountID) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "https://app.test/api/v1/accounts/"+string(accountID)+"/context", nil)
	request.AddCookie(&http.Cookie{Name: "spyglass_session", Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertRoutedCell(t *testing.T, response *httptest.ResponseRecorder, accountID ids.AccountID, cellID ids.CellID, generation uint64) {
	t.Helper()
	var payload struct {
		AccountID           ids.AccountID `json:"account_id"`
		CellID              ids.CellID    `json:"cell_id"`
		PlacementGeneration uint64        `json:"placement_generation"`
	}
	if response.Code != http.StatusOK {
		t.Fatalf("routed context status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.AccountID != accountID || payload.CellID != cellID || payload.PlacementGeneration != generation {
		t.Fatalf("routed context=%+v err=%v", payload, err)
	}
}

func directCellRequest(handler http.Handler, accountID ids.AccountID, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://cell.test/api/v1/accounts/"+string(accountID)+"/context", nil)
	request.Header.Set(cellapi.RouteContextHeader, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertPhysicalCellPlacement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, expected, forbidden ids.AccountID) {
	t.Helper()
	var expectedCount, forbiddenCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces WHERE account_id=$1`, expected).Scan(&expectedCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.account_namespaces WHERE account_id=$1`, forbidden).Scan(&forbiddenCount); err != nil {
		t.Fatal(err)
	}
	if expectedCount != 1 || forbiddenCount != 0 {
		t.Fatalf("physical cell placement expected=%d forbidden=%d", expectedCount, forbiddenCount)
	}
}

func countReceipts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.route_context_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func moveAccountBetweenCells(t *testing.T, ctx context.Context, global, cellA, cellB *pgxpool.Pool, accountID ids.AccountID, destination ids.CellID, now time.Time) {
	t.Helper()
	tx, err := global.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE accounts SET cell_id=$2,placement_generation=2 WHERE id=$1`, accountID, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE account_directory SET cell_id=$2,placement_generation=2,state='active',data_region='us-west',updated_at=$3 WHERE account_id=$1`, accountID, destination, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cellA.Exec(ctx, `UPDATE spyglass.account_namespaces SET placement_generation=2,state='moving' WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := cellB.Exec(ctx, `INSERT INTO spyglass.account_namespaces (account_id,placement_generation,state,created_at) VALUES ($1,2,'active',$2)`, accountID, now); err != nil {
		t.Fatal(err)
	}
}

type routingTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *routingTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *routingTestClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type routingCellTransport struct {
	mu       sync.Mutex
	handlers map[string]http.Handler
	failed   map[string]bool
	hits     map[string]int
	tokens   map[string][]string
}

func newRoutingCellTransport(handlers map[string]http.Handler) *routingCellTransport {
	return &routingCellTransport{handlers: handlers, failed: make(map[string]bool), hits: make(map[string]int), tokens: make(map[string][]string)}
}

func (transport *routingCellTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	host := request.URL.Host
	transport.mu.Lock()
	handler := transport.handlers[host]
	failed := transport.failed[host]
	transport.hits[host]++
	transport.tokens[host] = append(transport.tokens[host], request.Header.Get(cellapi.RouteContextHeader))
	transport.mu.Unlock()
	if handler == nil {
		return nil, errors.New("unregistered cell destination")
	}
	if failed {
		return nil, errors.New("assigned cell is unavailable")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Result(), nil
}

func (transport *routingCellTransport) SetFailed(host string, failed bool) {
	transport.mu.Lock()
	transport.failed[host] = failed
	transport.mu.Unlock()
}

func (transport *routingCellTransport) Hits(host string) int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.hits[host]
}

func (transport *routingCellTransport) LastToken(host string) string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	values := transport.tokens[host]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
