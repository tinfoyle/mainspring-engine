package migrations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAccountExportCoverageMatchesAccountOwnedSchema(t *testing.T) {
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
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	rows, err := pool.Query(ctx, `SELECT table_schema,table_name FROM information_schema.columns WHERE table_schema IN ('public','spyglass') AND column_name='account_id' ORDER BY table_schema,table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	registry, err := accountexport.LaunchRegistry()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			t.Fatal(err)
		}
		seen[schema+"."+table] = true
		if _, covered := registry.Coverage(schema, table); !covered {
			t.Fatalf("Account-owned table %s.%s has no portability disposition", schema, table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// These Account-associated tables do not use a direct account_id column.
	seen["public.accounts"] = true
	seen["public.billing_reconciliation_queue"] = true
	seen["public.account_subscription_lifecycle_events"] = true
	seen["public.account_subscription_lifecycle_notices"] = true
	seen["public.account_subscription_termination_jobs"] = true
	seen["public.affiliate_attributions"] = true
	seen["public.affiliate_enrollments"] = true
	seen["public.affiliate_commission_entries"] = true
	seen["public.affiliate_commission_rules"] = true
	seen["public.affiliate_commission_invoice_payments"] = true
	seen["public.affiliate_commission_invoice_lines"] = true
	seen["public.affiliate_provider_adverse_invoice_lines"] = true
	seen["public.affiliate_credit_reservations"] = true
	seen["public.affiliate_credit_allocations"] = true
	seen["public.affiliate_credit_reservation_events"] = true
	seen["public.affiliate_credit_reversal_adjustments"] = true
	seen["public.affiliate_credit_reversal_adjustment_events"] = true
	seen["public.affiliate_settlement_policies"] = true
	seen["public.affiliate_settlement_policy_current"] = true
	seen["public.affiliate_settlement_policy_events"] = true
	seen["public.privacy_consent_subjects"] = true
	seen["public.privacy_consent_decisions"] = true
	seen["public.analytics_events"] = true
	for _, coverage := range registry.Tables() {
		if !seen[coverage.Schema+"."+coverage.Table] {
			t.Fatalf("portability policy references missing table %s.%s", coverage.Schema, coverage.Table)
		}
	}
	assertGlobalProjectionColumns(t, ctx, pool, registry)
	assertGlobalProjectionRecords(t, ctx, pool)
	assertCellProjectionCohortColumns(t, ctx, pool, registry)
}

func assertCellProjectionCohortColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, registry *accountexport.Registry) {
	t.Helper()
	var projectionTx *pgxpool.Tx
	bySection := postgresadapter.AccountExportCellProjectionTables(projectionTx)
	completed := map[string]bool{"account": true, "agents": true, "attention": true, "baseline": true, "finance": true, "integrations": true, "knowledge": true, "marketing": true, "migration": true, "schedules": true, "work": true}
	seen := make(map[string]bool)
	for section, tables := range bySection {
		if !completed[section] {
			t.Fatalf("cell projection section %s is not a certified cohort", section)
		}
		for _, table := range tables {
			key := table.Schema + "." + table.Table
			if seen[key] {
				t.Fatalf("duplicate cell projection table %s", key)
			}
			seen[key] = true
			coverage, found := registry.Coverage(table.Schema, table.Table)
			if !found || coverage.Disposition != accountexport.Included || coverage.Section != section {
				t.Fatalf("cell projection table %s section=%s coverage=%+v found=%v", key, section, coverage, found)
			}
			assertProjectionColumnUnion(t, ctx, pool, table)
		}
	}
	for _, coverage := range registry.Tables() {
		if coverage.Schema == "spyglass" && coverage.Disposition == accountexport.Included && completed[coverage.Section] && !seen[coverage.Schema+"."+coverage.Table] {
			t.Fatalf("included cell cohort table %s.%s lacks an explicit projection", coverage.Schema, coverage.Table)
		}
	}
}

func assertProjectionColumnUnion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table postgresadapter.AccountExportProjectionTable) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY column_name`, table.Schema, table.Table)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, column)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	reviewed := append(append([]string(nil), table.Columns...), table.OmittedColumns...)
	slices.Sort(reviewed)
	if !slices.Equal(actual, reviewed) {
		t.Fatalf("projection column drift for %s.%s: schema=%v reviewed=%v", table.Schema, table.Table, actual, reviewed)
	}
}

func assertGlobalProjectionColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, registry *accountexport.Registry) {
	t.Helper()
	var projectionTx *pgxpool.Tx
	bySection := postgresadapter.AccountExportGlobalProjectionTables(projectionTx)
	seen := make(map[string]bool)
	for section, tables := range bySection {
		for _, table := range tables {
			key := table.Schema + "." + table.Table
			if seen[key] {
				t.Fatalf("duplicate global projection table %s", key)
			}
			seen[key] = true
			coverage, found := registry.Coverage(table.Schema, table.Table)
			if !found || coverage.Disposition != accountexport.Included || coverage.Section != section {
				t.Fatalf("projection table %s section=%s coverage=%+v found=%v", key, section, coverage, found)
			}
			rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY column_name`, table.Schema, table.Table)
			if err != nil {
				t.Fatal(err)
			}
			var actual []string
			for rows.Next() {
				var column string
				if err := rows.Scan(&column); err != nil {
					t.Fatal(err)
				}
				actual = append(actual, column)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			reviewed := append(append([]string(nil), table.Columns...), table.OmittedColumns...)
			slices.Sort(reviewed)
			if !slices.Equal(actual, reviewed) {
				t.Fatalf("global projection column drift for %s: schema=%v reviewed=%v", key, actual, reviewed)
			}
		}
	}
	for _, coverage := range registry.Tables() {
		if coverage.Schema == "public" && coverage.Disposition == accountexport.Included && !seen[coverage.Schema+"."+coverage.Table] {
			t.Fatalf("included global table %s.%s lacks an explicit projection", coverage.Schema, coverage.Table)
		}
	}
}

func assertGlobalProjectionRecords(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	now := time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("ee100000-0000-4000-8000-000000000001")
	ownerID := ids.UserID("ee200000-0000-4000-8000-000000000001")
	secretToken := []byte("invitation-secret-token-hash")
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at)
		VALUES ($1,'projection-owner@example.com','Projection Owner','active',$3,$3);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
		VALUES ($2,'projection-account','Projection Account','free','active','cell-us-east-01',1,1,1,1,$1,$3);
		INSERT INTO invitations(id,account_id,email,role,state,invited_by_user_id,token_hash,expires_at,created_at)
		VALUES ('ee300000-0000-4000-8000-000000000001',$2,'invitee@example.com','member','pending',$1,$4,$3::timestamptz + interval '1 day',$3)`,
		pgx.QueryExecModeSimpleProtocol, ownerID, accountID, now, secretToken); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	tables := postgresadapter.AccountExportGlobalProjectionTables(tx)["account"]
	source, err := postgresadapter.NewAccountExportSectionSource(accountexport.Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}, accountID, tables)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := source.Open(ctx, accountexport.BuildRequest{AccountID: accountID})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	var records [][]byte
	previousKey := ""
	for {
		record, found, err := cursor.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		var value map[string]any
		if err := json.Unmarshal(record, &value); err != nil {
			t.Fatal(err)
		}
		key, _ := value["key"].(string)
		if key <= previousKey || !bytes.Equal(record, mustCanonicalJSON(t, value)) {
			t.Fatalf("noncanonical/out-of-order record previous=%q record=%s", previousKey, record)
		}
		previousKey = key
		records = append(records, record)
	}
	joined := string(bytes.Join(records, []byte("\n")))
	if len(records) != 2 || !strings.Contains(joined, "Projection Account") || !strings.Contains(joined, "invitee@example.com") || strings.Contains(joined, "token_hash") || strings.Contains(joined, string(secretToken)) {
		t.Fatalf("sanitized records=%s", joined)
	}
}

func mustCanonicalJSON(t *testing.T, value map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
