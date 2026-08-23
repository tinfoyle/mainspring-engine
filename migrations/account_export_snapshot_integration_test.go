package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type snapshotSourceFactory struct {
	global pgx.Tx
	cell   pgx.Tx
}

func (factory *snapshotSourceFactory) Sources(_ context.Context, global, cell pgx.Tx, _ accountexport.Work) ([]accountexport.SectionSource, []accountexport.ObjectSource, error) {
	factory.global, factory.cell = global, cell
	return nil, nil, nil
}

func TestAccountExportSnapshotCoordinatorFencesGlobalAndCellSnapshots(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	global := openPool(t, ctx, databaseURL, nil)
	defer global.Close()
	cell := openPool(t, ctx, databaseURL, nil)
	defer cell.Close()
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		if _, err := migrations.Apply(ctx, global, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	accountID := ids.AccountID("ed100000-0000-4000-8000-000000000001")
	ownerID := ids.UserID("ed200000-0000-4000-8000-000000000001")
	now := time.Now().UTC()
	if _, err := global.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at)
		VALUES ($1,'snapshot-owner@example.com','Snapshot Owner','active',$3,$3);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
		VALUES ($2,'snapshot-account','Snapshot Account','free','active','cell-us-east-01',3,1,1,7,$1,$3);
		INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at)
		VALUES ($2,'cell-us-east-01',3,'active','us-east',$3);
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($2,3,'active',$3)`, pgx.QueryExecModeSimpleProtocol, ownerID, accountID, now); err != nil {
		t.Fatal(err)
	}
	factory := &snapshotSourceFactory{}
	coordinator, err := postgresadapter.NewAccountExportSnapshotCoordinator(global, cell, "cell-us-east-01", factory)
	if err != nil {
		t.Fatal(err)
	}
	work := accountexport.Work{ID: "ed300000-0000-4000-8000-000000000001", AccountID: accountID, RequestedBy: ownerID,
		CellID: "cell-us-east-01", PlacementGeneration: 3, AccountVersion: 7, Version: 2,
		LeaseID: "ed400000-0000-4000-8000-000000000001", RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	called := 0
	err = coordinator.WithSnapshot(ctx, work, func(snapshot accountexport.Snapshot, _ []accountexport.SectionSource, _ []accountexport.ObjectSource) error {
		called++
		if snapshot.CellID != work.CellID || snapshot.PlacementGeneration != 3 || snapshot.AccountVersion != 7 || snapshot.GlobalAt.Before(now) || snapshot.CellAt.Before(now) {
			t.Fatalf("snapshot=%+v", snapshot)
		}
		var accountContext, globalIsolation, cellIsolation, globalReadOnly, cellReadOnly string
		if err := factory.cell.QueryRow(ctx, `SELECT current_setting('spyglass.account_id')`).Scan(&accountContext); err != nil {
			t.Fatal(err)
		}
		if err := factory.global.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&globalIsolation); err != nil {
			t.Fatal(err)
		}
		if err := factory.cell.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&cellIsolation); err != nil {
			t.Fatal(err)
		}
		if err := factory.global.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&globalReadOnly); err != nil {
			t.Fatal(err)
		}
		if err := factory.cell.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&cellReadOnly); err != nil {
			t.Fatal(err)
		}
		if accountContext != string(accountID) || globalIsolation != "repeatable read" || cellIsolation != "repeatable read" || globalReadOnly != "on" || cellReadOnly != "on" {
			t.Fatalf("context=%q isolation=%q/%q read_only=%q/%q", accountContext, globalIsolation, cellIsolation, globalReadOnly, cellReadOnly)
		}
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("called=%d err=%v", called, err)
	}
	if _, err := global.Exec(ctx, `UPDATE account_directory SET placement_generation=4 WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	err = coordinator.WithSnapshot(ctx, work, func(accountexport.Snapshot, []accountexport.SectionSource, []accountexport.ObjectSource) error {
		return nil
	})
	var failure accountexport.BuildFailure
	if !errors.As(err, &failure) || failure.Code() != "snapshot_identity_invalid" || !failure.Permanent() {
		t.Fatalf("drift failure=%v", err)
	}
}
