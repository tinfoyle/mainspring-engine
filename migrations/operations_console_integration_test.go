package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

const (
	operationsStaffID   = ids.UserID("62000000-0000-4000-8000-000000000001")
	operationsTargetID  = ids.UserID("62000000-0000-4000-8000-000000000002")
	operationsAccountID = ids.AccountID("62000000-0000-4000-8000-000000000003")
)

type operationsGenerator struct{ sequence uint64 }

func (generator *operationsGenerator) New() string {
	generator.sequence++
	return "62000000-0000-4000-8000-" + leftPad12(generator.sequence)
}

func leftPad12(value uint64) string {
	raw := "000000000000" + stringUint(value)
	return raw[len(raw)-12:]
}

func stringUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

func TestPostgresOperationsConsoleEnforcesStaffGrantAuditAndLeastPrivilege(t *testing.T) {
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
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}

	operationsNow := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO cells(id,region,state,assigned_accounts,soft_account_limit,created_at,route_origin)
		  VALUES ('ops-cell','us-east','active',1,100,$1,'https://cell.ops.invalid')`, []any{operationsNow}},
		{`INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES
		  ($1,'operator@example.com','Operations Person','active',$3,1,$3),
		  ($2,'customer@example.com','Customer Person','active',$3,1,$3)`, []any{operationsStaffID, operationsTargetID, operationsNow}},
		{`INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,created_by_user_id,created_at,version,last_catalog_reconciled_version)
		  VALUES ($1,'customer-workshop','Customer Workshop','paid','active','ops-cell',1,1,$2,$3,1,2)`, []any{operationsAccountID, operationsTargetID, operationsNow}},
		{`INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at)
		  VALUES ('62000000-0000-4000-8000-000000000004',$1,$2,'owner','active',1,$3)`, []any{operationsAccountID, operationsTargetID, operationsNow}},
		{`INSERT INTO entitlement_snapshots(account_id,version,catalog_version,evaluated_at,source_hash,effective_packages)
		  VALUES ($1,1,2,$2,decode(repeat('11',32),'hex'),'{}')`, []any{operationsAccountID, operationsNow}},
		{`INSERT INTO billing_profiles(account_id,stripe_customer_id,billing_email,version,created_at,updated_at)
		  VALUES ($1,'cus_ops_contract','billing@example.com',1,$2,$2)`, []any{operationsAccountID, operationsNow}},
		{`INSERT INTO subscriptions(id,account_id,provider,provider_subscription_id,state,offer_code,offer_version,current_period_start,current_period_end,
		  provider_object_version,last_synced_at,created_at,updated_at,provider_customer_id,provider_mode)
		  VALUES ('62000000-0000-4000-8000-000000000005',$1,'stripe','sub_ops_contract','active','team-monthly-v2',2,$2::timestamptz,$2::timestamptz+interval '30 days',
		  '1',$2,$2,$2,'cus_ops_contract','test')`, []any{operationsAccountID, operationsNow}},
	}
	for index, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed statement %d: %v", index, err)
		}
	}
	for index, role := range []operations.StaffRole{operations.RoleSupport, operations.RoleAnalytics} {
		if _, err := pool.Exec(ctx, `SELECT spyglass_operations_assign_staff_role($1,$2,$3,$4,$5,$6,'local')`,
			"62000000-0000-4000-8000-00000000001"+string(rune('0'+index)),
			"62000000-0000-4000-8000-00000000002"+string(rune('0'+index)),
			operationsStaffID, role, "bootstrap@example.com", "Provision reviewed local Operations Console staff role."); err != nil {
			t.Fatal(err)
		}
	}
	const (
		queryRole    = "spyglass_operations_console_contract"
		identityRole = "spyglass_operations_identity_contract"
	)
	if _, err := pool.Exec(ctx, `CREATE ROLE `+queryRole+` NOLOGIN NOBYPASSRLS;
		CREATE ROLE `+identityRole+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA public TO `+queryRole+`,`+identityRole+`;
		GRANT EXECUTE ON FUNCTION spyglass_operations_current_staff(uuid),
		  spyglass_operations_lookup(uuid,uuid,text,text,text,text,text),
		  spyglass_operations_record_session_event(uuid,uuid,uuid,text,text,timestamptz),
		  spyglass_operations_create_support_grant(uuid,uuid,uuid,uuid,uuid,text,text,text,timestamptz,timestamptz),
		  spyglass_operations_get_support_grant(uuid,uuid),
		  spyglass_operations_revoke_support_grant(uuid,uuid,uuid,bigint,text,text,text,timestamptz),
		  spyglass_operations_customer_access_history(uuid,uuid,integer),
		  spyglass_operations_account_view(uuid,uuid,uuid,text,text,text,timestamptz),
		  spyglass_operations_analytics_report(uuid,uuid,timestamptz,timestamptz,text,text,integer,text,text,text,timestamptz),
		  spyglass_operations_support_history(uuid,uuid,integer,timestamptz) TO `+queryRole+`;
		GRANT SELECT ON users,operations_staff,operations_staff_role_assignments,operations_access_events TO `+identityRole+`;
		GRANT SELECT,INSERT,UPDATE ON operations_sessions TO `+identityRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+queryRole+`; DROP OWNED BY `+identityRole+`; DROP ROLE `+queryRole+`; DROP ROLE `+identityRole)
	}()
	queryPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+queryRole)
		return err
	})
	defer queryPool.Close()
	identityPool := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+identityRole)
		return err
	})
	defer identityPool.Close()

	repository := postgresadapter.NewOperationsConsoleRepository(queryPool)
	generator := &operationsGenerator{sequence: 100}
	service, err := operationsconsole.New(repository, generator, fixedClock{now: operationsNow}, "local")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := service.Staff(ctx, operationsStaffID)
	if err != nil || !staff.HasRole(operations.RoleSupport) || !staff.HasRole(operations.RoleAnalytics) {
		t.Fatalf("staff=%+v err=%v", staff, err)
	}
	lookup, err := service.Lookup(ctx, operationsStaffID, operations.LookupQuery{
		Kind: operations.LookupEmail, Value: "customer@example.com",
		Audit: operations.AuditReason{Ticket: "SUP-620", Reason: "Locate the exact customer support target."},
	})
	if err != nil || len(lookup) != 1 || lookup[0].AccountID != operationsAccountID || lookup[0].StripeCustomerID != "cus_ops_contract" {
		t.Fatalf("lookup=%+v err=%v", lookup, err)
	}
	grant, err := service.CreateGrant(ctx, operationsconsole.CreateGrantCommand{
		ActorUserID: operationsStaffID, TargetUserID: operationsTargetID, AccountID: operationsAccountID,
		Audit: operations.AuditReason{Ticket: "SUP-620", Reason: "Customer requested help inspecting billing state."},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ViewAccount(ctx, operationsStaffID, grant.ID, operations.AuditReason{Ticket: "SUP-620", Reason: "Inspect the read-only customer support projection."})
	if err != nil {
		t.Fatal(err)
	}
	if view.User.ID != operationsTargetID || view.Account.ID != operationsAccountID || view.Billing.CustomerID != "cus_ops_contract" || view.Billing.SubscriptionID != "sub_ops_contract" || len(view.SupportHistory) != 2 {
		t.Fatalf("unexpected support view: %+v", view)
	}
	if _, err := service.RevokeGrant(ctx, operationsStaffID, grant.ID, grant.Version, operations.AuditReason{Ticket: "SUP-620", Reason: "Customer support inspection is complete."}); err != nil {
		t.Fatal(err)
	}
	history, err := service.CustomerHistory(ctx, operationsTargetID, operationsAccountID, 20)
	if err != nil || len(history) != 3 || history[0].Action != "support_grant_revoked" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE operations_access_events SET reason='tampered audit evidence' WHERE id=$1`, history[0].ID); err == nil {
		t.Fatal("operations access evidence was mutable")
	}

	report, err := service.Analytics(ctx, operationsStaffID, analyticsreport.Query{
		From: operationsNow.Add(-24 * time.Hour), To: operationsNow, Bucket: analyticsreport.BucketDay,
		Dimension: "none", MinimumCohort: 5,
	}, operations.AuditReason{Ticket: "AN-620", Reason: "Review the aggregate local launch funnel."})
	if err != nil || report.Rows == nil {
		t.Fatalf("report=%+v err=%v", report, err)
	}

	sessionRepository := postgresadapter.NewOperationsSessionRepository(identityPool)
	sessionGenerator := &operationsGenerator{sequence: 300}
	sessionService, err := sessions.NewService(sessionRepository, sessionGenerator, fixedClock{now: operationsNow}, 8*time.Hour, 30*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessionService.IssueForClientWithMethod(ctx, operationsStaffID, 1, "Operations browser", sessions.AuthenticationMethodPasskey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordAuthentication(ctx, operationsStaffID, issued.Session.ID); err != nil {
		t.Fatal(err)
	}
	if authenticated, err := sessionService.Authenticate(ctx, issued.Token); err != nil || authenticated.Session.UserID != operationsStaffID {
		t.Fatalf("authenticated=%+v err=%v", authenticated, err)
	}

	var roles []string
	if err := queryPool.QueryRow(ctx, `SELECT roles FROM spyglass_operations_current_staff($1)`, operationsStaffID).Scan(&roles); err != nil || len(roles) != 2 {
		t.Fatalf("roles=%v err=%v", roles, err)
	}
	if _, err := queryPool.Exec(ctx, `SELECT primary_email FROM users LIMIT 1`); err == nil {
		t.Fatal("Operations Console contract role read the raw User table")
	}
	if _, err := identityPool.Exec(ctx, `SELECT display_name FROM accounts LIMIT 1`); err == nil {
		t.Fatal("Operations identity role read a raw Account table")
	}
}

func TestOperationsConsoleDatabaseErrorsRemainClassified(t *testing.T) {
	var databaseError *pgconn.PgError
	if errors.As(operations.ErrGrantDenied, &databaseError) {
		t.Fatal("domain denial unexpectedly exposes a PostgreSQL error")
	}
}
