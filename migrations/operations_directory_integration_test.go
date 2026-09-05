package migrations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresDirectoryPaginationAuditAndLeastPrivilege(t *testing.T) {
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at) VALUES($1,'directory-admin@example.com','Directory Admin','active',now(),now()-interval '1 day')`, operationsStaffID)
	for i := 1; i <= 6; i++ {
		state := "active"
		if i == 6 {
			state = "pending_verification"
		}
		exec(`INSERT INTO users(id,primary_email,display_name,state,created_at) VALUES($1,$2,$3,$4,'2026-09-05T12:00:00Z')`,
			fmt.Sprintf("71000000-0000-4000-8000-%012d", i), fmt.Sprintf("directory%d@example.com", i), fmt.Sprintf("Person %d", i), state)
	}
	exec(`INSERT INTO cells(id,region,state,assigned_accounts,soft_account_limit,created_at,route_origin) VALUES('directory-cell','us-east','active',0,100,now(),'https://directory.invalid')`)
	for i := 1; i <= 3; i++ {
		state := "active"
		if i == 3 {
			state = "closed"
		}
		exec(`INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,created_by_user_id,created_at) VALUES($1,$2,$3,'free',$4,'directory-cell',$5,'2026-09-05T12:00:00Z')`, fmt.Sprintf("71000000-0000-4000-8001-%012d", i), fmt.Sprintf("directory-team-%d", i), fmt.Sprintf("Team %d", i), state, operationsStaffID)
	}
	exec(`INSERT INTO memberships(id,account_id,user_id,role,state,created_at) VALUES
 ('71000000-0000-4000-8002-000000000001','71000000-0000-4000-8001-000000000001','71000000-0000-4000-8000-000000000001','owner','active',now()),
 ('71000000-0000-4000-8002-000000000002','71000000-0000-4000-8001-000000000001','71000000-0000-4000-8000-000000000002','member','removed',now())`)
	grant := func(role string) {
		exec(`SELECT spyglass_operations_assign_staff_role($1,$2,$3,$4,'local-test','Test directory role boundaries.','local')`, ids.RandomGenerator{}.New(), ids.RandomGenerator{}.New(), operationsStaffID, role)
	}
	grant("support")
	const role = "spyglass_directory_contract"
	exec(`CREATE ROLE ` + role + ` NOLOGIN NOBYPASSRLS; GRANT USAGE ON SCHEMA public TO ` + role + `; GRANT EXECUTE ON FUNCTION spyglass_operations_directory(uuid,uuid,text,integer,integer,text,text,text) TO ` + role)
	defer func() { _, _ = pool.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE `+role) }()
	restricted := openPool(t, ctx, databaseURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer restricted.Close()
	repo := postgresadapter.NewOperationsConsoleRepository(restricted)
	staff := operations.Staff{UserID: operationsStaffID, State: operations.StaffActive, Roles: []operations.StaffRole{operations.RoleAdministrator}}
	query := operations.DirectoryQuery{Kind: "users", Page: 1, PageSize: 2, Audit: operations.AuditReason{Ticket: "OPS-710", Reason: "Review paginated registrations."}}
	call := func(q operations.DirectoryQuery, event ids.OperationsAuditEventID) (operations.DirectoryPage, error) {
		return repo.Directory(ctx, staff, q, event, "local")
	}
	event := func() ids.OperationsAuditEventID { return ids.OperationsAuditEventID(ids.RandomGenerator{}.New()) }
	if _, err := call(query, event()); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("support allowed by database: %v", err)
	}
	grant("operations_administrator")
	seen := map[ids.UserID]bool{}
	for page := 1; page <= 4; page++ {
		query.Page = page
		result, err := call(query, event())
		if err != nil || result.Total != 7 || len(result.Users) > 2 || len(result.Teams) != 0 {
			t.Fatalf("user page %+v: %v", result, err)
		}
		for _, u := range result.Users {
			if seen[u.ID] {
				t.Fatal("duplicate across tied timestamps")
			}
			seen[u.ID] = true
			if u.ID == "71000000-0000-4000-8000-000000000001" && u.TeamCount != 1 {
				t.Fatal("active team count")
			}
			if u.ID == "71000000-0000-4000-8000-000000000002" && u.TeamCount != 0 {
				t.Fatal("removed membership counted")
			}
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "security_version") || strings.Contains(string(raw), "secret_hash") {
			t.Fatal("private fields included")
		}
	}
	if len(seen) != 7 || !seen["71000000-0000-4000-8000-000000000006"] {
		t.Fatal("users without teams or pending verification were omitted")
	}
	query.Kind = "teams"
	query.Page = 1
	first, err := call(query, event())
	if err != nil || first.Total != 3 || len(first.Teams) != 2 || first.Teams[0].ID != "71000000-0000-4000-8001-000000000003" || first.Teams[0].State != "closed" {
		t.Fatalf("team first page: %+v %v", first, err)
	}
	query.Page = 2
	last, err := call(query, event())
	if err != nil || len(last.Teams) != 1 || last.Teams[0].MemberCount != 1 {
		t.Fatalf("team last page: %+v %v", last, err)
	}
	query.Page = 3
	empty, err := call(query, event())
	if err != nil || empty.Total != 3 || len(empty.Teams) != 0 || empty.Users == nil || empty.Teams == nil {
		t.Fatalf("empty page: %+v %v", empty, err)
	}
	query.Page = 1
	query.PageSize = 101
	if _, err := call(query, event()); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("oversize page allowed: %v", err)
	}
	query.PageSize = 2
	duplicate := event()
	if _, err := call(query, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := call(query, duplicate); err == nil {
		t.Fatal("returned directory despite failed audit insert")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM operations_access_events WHERE action='directory_viewed'`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("audit count=%d %v", count, err)
	}
	for _, table := range []string{"users", "accounts", "memberships", "operations_access_events"} {
		if _, err := restricted.Exec(ctx, "SELECT * FROM "+table); err == nil {
			t.Fatal("runtime can directly read", table)
		}
	}
	var publicExecute bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) a WHERE p.proname='spyglass_operations_directory' AND a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(&publicExecute); err != nil || publicExecute {
		t.Fatal("directory is publicly executable")
	}
	exec(`UPDATE operations_staff_role_assignments SET revoked_at=now() WHERE staff_user_id=$1 AND role='operations_administrator'`, operationsStaffID)
	if _, err := call(query, event()); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("revoked administrator allowed: %v", err)
	}
}
