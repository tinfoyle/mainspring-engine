package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAccountExportRequestsAreLeaseFencedAndExpireExactly(t *testing.T) {
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
	for _, target := range []migrations.Target{migrations.Global, migrations.Development} {
		if _, err := migrations.Apply(ctx, pool, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}
	now := time.Date(2026, 8, 24, 14, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("e4100000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("e4100000-0000-4000-8000-000000000002")
	ownerID := ids.UserID("e4200000-0000-4000-8000-000000000001")
	memberID := ids.UserID("e4200000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at)
		VALUES ($1,'export-owner@example.com','Export Owner','active',$5,$5),($2,'export-member@example.com','Export Member','active',$5,$5);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
		VALUES ($3,'export-account','Export Account','free','active','cell-us-east-01',1,1,1,1,$1,$5),
		       ($4,'other-export-account','Other Export Account','free','active','cell-us-east-01',1,1,1,1,$1,$5);
		INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at)
		VALUES ($3,'cell-us-east-01',1,'active','us-east',$5),($4,'cell-us-east-01',1,'active','us-east',$5);
		INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at)
		VALUES ('e4300000-0000-4000-8000-000000000001',$3,$1,'owner','active',1,$5),
		       ('e4300000-0000-4000-8000-000000000002',$3,$2,'member','active',1,$5)`,
		pgx.QueryExecModeSimpleProtocol, ownerID, memberID, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	repository := postgresadapter.NewAccountExportRepository(pool)
	create := accountexport.CreateMutation{ID: "e4400000-0000-4000-8000-000000000001", EventID: "e4500000-0000-4000-8000-000000000001",
		AccountID: accountID, RequestedBy: ownerID, CellID: "cell-us-east-01", PlacementGeneration: 1, RequestedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	created, err := repository.Create(ctx, create)
	if err != nil || created.State != accountexport.StateQueued || created.Version != 1 || created.AccountVersion != 1 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	second := create
	second.ID, second.EventID = "e4400000-0000-4000-8000-000000000002", "e4500000-0000-4000-8000-000000000002"
	if _, err := repository.Create(ctx, second); !errors.Is(err, accountexport.ErrStateConflict) {
		t.Fatalf("concurrent active request error=%v", err)
	}
	unauthorized := create
	unauthorized.ID, unauthorized.EventID, unauthorized.RequestedBy = "e4400000-0000-4000-8000-000000000003", "e4500000-0000-4000-8000-000000000003", memberID
	if _, err := repository.Create(ctx, unauthorized); err == nil {
		t.Fatal("ordinary member created complete Account export")
	}

	work, claimed, err := repository.ClaimBuild(ctx, now, 5*time.Minute, "e4600000-0000-4000-8000-000000000001", "e4500000-0000-4000-8000-000000000004")
	if err != nil || !claimed || work.Version != 2 || work.AttemptCount != 1 || work.AccountVersion != 1 {
		t.Fatalf("first claim=%+v claimed=%v err=%v", work, claimed, err)
	}
	if _, claimed, err := repository.ClaimBuild(ctx, now, 5*time.Minute, "e4600000-0000-4000-8000-000000000002", "e4500000-0000-4000-8000-000000000005"); err != nil || claimed {
		t.Fatalf("active lease second claim=%v err=%v", claimed, err)
	}
	requeued, err := repository.RecordFailure(ctx, accountexport.FailureMutation{Work: work, EventID: "e4500000-0000-4000-8000-000000000006",
		ErrorCode: "cell_snapshot_unavailable", NextAttemptAt: now.Add(10 * time.Minute), At: now.Add(time.Minute)})
	if err != nil || requeued.State != accountexport.StateQueued || requeued.Version != 3 {
		t.Fatalf("requeued=%+v err=%v", requeued, err)
	}
	work, claimed, err = repository.ClaimBuild(ctx, now.Add(10*time.Minute), 5*time.Minute, "e4600000-0000-4000-8000-000000000003", "e4500000-0000-4000-8000-000000000007")
	if err != nil || !claimed || work.Version != 4 || work.AttemptCount != 2 {
		t.Fatalf("retry claim=%+v claimed=%v err=%v", work, claimed, err)
	}
	digest := sha256.Sum256([]byte("private deterministic artifact"))
	artifact := accountexport.Artifact{Reference: "account-exports/e410/e440.zip", SHA256: digest, Bytes: 4096}
	completed, err := repository.Complete(ctx, accountexport.CompleteMutation{Work: work, EventID: "e4500000-0000-4000-8000-000000000008",
		Snapshot: accountexport.Snapshot{CellID: work.CellID, PlacementGeneration: work.PlacementGeneration, AccountVersion: work.AccountVersion,
			GlobalAt: now.Add(11 * time.Minute), CellAt: now.Add(12 * time.Minute)}, Artifact: artifact, AvailableAt: now.Add(13 * time.Minute)})
	if err != nil || completed.State != accountexport.StateAvailable || completed.Version != 5 || completed.ArtifactBytes != artifact.Bytes {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err := repository.Get(ctx, otherAccountID, create.ID); !errors.Is(err, accountexport.ErrNotFound) {
		t.Fatalf("cross-Account read error=%v", err)
	}
	if _, claimed, err := repository.ClaimDeletion(ctx, now.Add(24*time.Hour), 5*time.Minute, "e4700000-0000-4000-8000-000000000001", "e4500000-0000-4000-8000-000000000009"); err != nil || claimed {
		t.Fatalf("early expiry claimed=%v err=%v", claimed, err)
	}
	deletion, claimed, err := repository.ClaimDeletion(ctx, create.ExpiresAt, 5*time.Minute, "e4700000-0000-4000-8000-000000000002", "e4500000-0000-4000-8000-000000000010")
	if err != nil || !claimed || deletion.Version != 6 || deletion.Artifact.Reference != artifact.Reference || !bytes.Equal(deletion.Artifact.SHA256[:], artifact.SHA256[:]) {
		t.Fatalf("deletion=%+v claimed=%v err=%v", deletion, claimed, err)
	}
	stale := deletion
	stale.LeaseID = "e4700000-0000-4000-8000-000000000003"
	if _, err := repository.CompleteDeletion(ctx, stale, create.ExpiresAt.Add(time.Minute), "e4500000-0000-4000-8000-000000000011"); !errors.Is(err, accountexport.ErrLeaseConflict) {
		t.Fatalf("stale deletion error=%v", err)
	}
	mismatched := deletion
	mismatched.Artifact.Bytes++
	if _, err := repository.CompleteDeletion(ctx, mismatched, create.ExpiresAt.Add(time.Minute), "e4500000-0000-4000-8000-000000000011"); !errors.Is(err, accountexport.ErrLeaseConflict) {
		t.Fatalf("mismatched artifact deletion error=%v", err)
	}
	deleted, err := repository.CompleteDeletion(ctx, deletion, create.ExpiresAt.Add(time.Minute), "e4500000-0000-4000-8000-000000000012")
	if err != nil || deleted.State != accountexport.StateDeleted || deleted.Version != 7 || deleted.DeletedAt == nil {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	var reference *string
	var storedDigest []byte
	if err := pool.QueryRow(ctx, `SELECT artifact_reference,artifact_sha256 FROM account_export_requests WHERE id=$1`, create.ID).Scan(&reference, &storedDigest); err != nil || reference != nil || !bytes.Equal(storedDigest, digest[:]) {
		t.Fatalf("deleted evidence reference=%v digest=%x err=%v", reference, storedDigest, err)
	}
	var eventTypes []string
	rows, err := pool.Query(ctx, `SELECT event_type FROM account_export_events WHERE request_id=$1 ORDER BY occurred_at,id`, create.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatal(err)
		}
		eventTypes = append(eventTypes, eventType)
	}
	rows.Close()
	if strings.Join(eventTypes, ",") != "requested,build_claimed,build_requeued,build_claimed,available,deletion_claimed,deleted" {
		t.Fatalf("event types=%v", eventTypes)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM account_export_events WHERE request_id=$1`, create.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("event mutation error=%v", err)
	}
	second.RequestedAt, second.ExpiresAt = create.ExpiresAt.Add(2*time.Hour), create.ExpiresAt.Add(3*time.Hour)
	createdAfterExpiry, err := repository.Create(ctx, second)
	if err != nil || createdAfterExpiry.State != accountexport.StateQueued {
		t.Fatalf("new request after deletion=%+v err=%v", createdAfterExpiry, err)
	}
	if _, claimed, err := repository.ClaimBuild(ctx, second.ExpiresAt, 5*time.Minute, "e4600000-0000-4000-8000-000000000004", "e4500000-0000-4000-8000-000000000013"); err != nil || claimed {
		t.Fatalf("expired request claim=%v err=%v", claimed, err)
	}
	expired, err := repository.Get(ctx, accountID, second.ID)
	if err != nil || expired.State != accountexport.StateFailed || expired.ErrorCode != "request_expired" {
		t.Fatalf("expired request=%+v err=%v", expired, err)
	}
	third := unauthorized
	third.RequestedBy, third.RequestedAt, third.ExpiresAt = ownerID, second.ExpiresAt.Add(time.Hour), second.ExpiresAt.Add(25*time.Hour)
	if _, err := repository.Create(ctx, third); err != nil {
		t.Fatalf("create movement-race request: %v", err)
	}
	movementWork, claimed, err := repository.ClaimBuild(ctx, third.RequestedAt, 5*time.Minute, "e4600000-0000-4000-8000-000000000005", "e4500000-0000-4000-8000-000000000014")
	if err != nil || !claimed {
		t.Fatalf("movement-race claim=%+v claimed=%v err=%v", movementWork, claimed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET placement_generation=2,version=version+1 WHERE id=$1;
		UPDATE account_directory SET placement_generation=2,updated_at=$2 WHERE account_id=$1`, pgx.QueryExecModeSimpleProtocol, accountID, third.RequestedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Complete(ctx, accountexport.CompleteMutation{Work: movementWork, EventID: "e4500000-0000-4000-8000-000000000015",
		Snapshot: accountexport.Snapshot{CellID: movementWork.CellID, PlacementGeneration: movementWork.PlacementGeneration, AccountVersion: movementWork.AccountVersion,
			GlobalAt: third.RequestedAt.Add(time.Minute), CellAt: third.RequestedAt.Add(2 * time.Minute)}, Artifact: artifact, AvailableAt: third.RequestedAt.Add(3 * time.Minute)}); !errors.Is(err, accountexport.ErrLeaseConflict) {
		t.Fatalf("completion across movement error=%v", err)
	}
	if _, claimed, err := repository.ClaimBuild(ctx, third.RequestedAt.Add(3*time.Minute), 5*time.Minute, "e4600000-0000-4000-8000-000000000006", "e4500000-0000-4000-8000-000000000016"); err != nil || claimed {
		t.Fatalf("movement drift claim=%v err=%v", claimed, err)
	}
	drifted, err := repository.Get(ctx, accountID, third.ID)
	if err != nil || drifted.State != accountexport.StateFailed || drifted.ErrorCode != "placement_changed" {
		t.Fatalf("movement-drift request=%+v err=%v", drifted, err)
	}
}
