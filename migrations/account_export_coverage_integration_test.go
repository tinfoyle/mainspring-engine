package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
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
	for _, coverage := range registry.Tables() {
		if !seen[coverage.Schema+"."+coverage.Table] {
			t.Fatalf("portability policy references missing table %s.%s", coverage.Schema, coverage.Table)
		}
	}
}
