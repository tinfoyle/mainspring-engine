package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresTrafficAuthorityAuditAndRevocation(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,'traffic@example.com','Traffic Reviewer','active',now(),1,now())`, operationsStaffID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT spyglass_operations_assign_staff_role('69000000-0000-4000-8000-000000000001','69000000-0000-4000-8000-000000000002',$1,'analytics','local-test','Test traffic authority separation.','local')`, operationsStaffID); err != nil {
		t.Fatal(err)
	}
	const role = "spyglass_traffic_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS; GRANT USAGE ON SCHEMA public TO `+role+`; GRANT EXECUTE ON FUNCTION spyglass_operations_authorize_traffic_report(uuid,uuid,timestamptz,timestamptz,text,text,text,timestamptz) TO `+role); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE `+role) }()
	restricted := openPool(t, ctx, databaseURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer restricted.Close()
	authorizer := postgresadapter.NewOperationsTrafficAuthorizer(restricted, "local")
	now := time.Now().UTC()
	query := trafficreport.Query{From: now.Add(-time.Hour), To: now}
	audit := operations.AuditReason{Ticket: "OPS-690", Reason: "Review current traffic and collection status."}
	if err := authorizer.AuthorizeTrafficRead(ctx, operationsStaffID, query, audit); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("analytics authority allowed: %v", err)
	}
	if _, err := restricted.Exec(ctx, `SELECT * FROM users`); err == nil {
		t.Fatal("traffic role can read customer table")
	}
	if _, err := pool.Exec(ctx, `SELECT spyglass_operations_assign_staff_role('69000000-0000-4000-8000-000000000003','69000000-0000-4000-8000-000000000004',$1,'operations_administrator','local-test','Test authorized traffic review.','local')`, operationsStaffID); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.AuthorizeTrafficRead(ctx, operationsStaffID, query, audit); err != nil {
		t.Fatal(err)
	}
	var count int
	var details string
	if err := pool.QueryRow(ctx, `SELECT count(*),min(details::text) FROM operations_access_events WHERE action='traffic_report_requested'`).Scan(&count, &details); err != nil || count != 1 {
		t.Fatalf("audit count=%d details=%s err=%v", count, details, err)
	}
	if _, err := restricted.Exec(ctx, `DELETE FROM operations_access_events`); err == nil {
		t.Fatal("traffic reader can erase audit")
	}
	if _, err := pool.Exec(ctx, `UPDATE operations_staff_role_assignments SET revoked_at=now() WHERE staff_user_id=$1 AND role='operations_administrator'`, operationsStaffID); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.AuthorizeTrafficRead(ctx, operationsStaffID, query, audit); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("revoked administrator allowed: %v", err)
	}
	var publicExecute bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) a WHERE p.proname='spyglass_operations_authorize_traffic_report' AND a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(&publicExecute); err != nil || publicExecute {
		t.Fatalf("public execute=%v err=%v", publicExecute, err)
	}
}
