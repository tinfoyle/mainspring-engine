package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresMembershipGovernancePreservesOwnershipAndAudit(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	accountID := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	ownerUserID := ids.UserID("a2000000-0000-4000-8000-000000000002")
	targetUserID := ids.UserID("a3000000-0000-4000-8000-000000000003")
	adminUserID := ids.UserID("ab000000-0000-4000-8000-000000000011")
	ownerMembershipID := ids.MembershipID("a4000000-0000-4000-8000-000000000004")
	targetMembershipID := ids.MembershipID("a5000000-0000-4000-8000-000000000005")
	adminMembershipID := ids.MembershipID("ac000000-0000-4000-8000-000000000012")
	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	_, err := pool.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES
		($1,'owner@example.com','Original Owner','active',$4,1,$4),
		($2,'successor@example.com','Successor','active',$4,1,$4),
		($3,'administrator@example.com','Peer Administrator','active',$4,1,$4)`, ownerUserID, targetUserID, adminUserID, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,created_by_user_id,created_at)
		SELECT $1,'membership-governance','Membership Governance','free','active',id,1,1,$2,$3 FROM cells ORDER BY id LIMIT 1`, accountID, ownerUserID, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at) VALUES
		($1,$2,$3,'owner','active',1,$8),($4,$2,$5,'member','active',1,$8),($6,$2,$7,'administrator','active',1,$8)`, ownerMembershipID, accountID, ownerUserID, targetMembershipID, targetUserID, adminMembershipID, adminUserID, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := postgresadapter.NewAccountMemberRepository(pool)
	members, err := repository.List(ctx, accountID)
	if err != nil || len(members) != 3 || members[0].Role != accounts.RoleOwner || members[0].Email != "owner@example.com" {
		t.Fatalf("initial roster=%+v err=%v", members, err)
	}

	changed, err := repository.ChangeRole(ctx, accountmembers.ChangeRoleMutation{EventID: "a6000000-0000-4000-8000-000000000006", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 1, Role: accounts.RoleAdministrator, Reason: "Prepare ownership successor", At: now.Add(time.Minute)})
	if err != nil || changed.Role != accounts.RoleAdministrator || changed.Version != 2 {
		t.Fatalf("role change=%+v err=%v", changed, err)
	}
	if _, err := repository.ChangeRole(ctx, accountmembers.ChangeRoleMutation{EventID: "a7000000-0000-4000-8000-000000000007", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 1, Role: accounts.RoleViewer, Reason: "Stale change", At: now.Add(2 * time.Minute)}); !errors.Is(err, accountmembers.ErrVersionConflict) {
		t.Fatalf("stale role change error=%v", err)
	}

	transferred, err := repository.TransferOwnership(ctx, accountmembers.TransferMutation{EventID: "a8000000-0000-4000-8000-000000000008", ActorUserID: ownerUserID, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedActorVersion: 1, ExpectedTargetVersion: 2, Reason: "Approved leadership transition", At: now.Add(3 * time.Minute)})
	if err != nil || transferred.PreviousOwner.Role != accounts.RoleAdministrator || transferred.PreviousOwner.Version != 2 || transferred.NewOwner.Role != accounts.RoleOwner || transferred.NewOwner.Version != 3 {
		t.Fatalf("transfer=%+v err=%v", transferred, err)
	}
	var ownerCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE account_id=$1 AND role='owner' AND state='active'`, accountID).Scan(&ownerCount); err != nil || ownerCount != 1 {
		t.Fatalf("active owner count=%d err=%v", ownerCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_membership_events WHERE account_id=$1`, accountID).Scan(&eventCount); err != nil || eventCount != 2 {
		t.Fatalf("audit event count=%d err=%v", eventCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_membership_events SET reason='tampered' WHERE account_id=$1`, accountID); err == nil {
		t.Fatal("immutable Membership audit accepted an update")
	}

	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "a9000000-0000-4000-8000-000000000009", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleAdministrator, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 3, Reason: "Attempt to remove active owner", At: now.Add(4 * time.Minute)}); !errors.Is(err, accountmembers.ErrOwnershipRequired) {
		t.Fatalf("remove active owner error=%v", err)
	}
	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "ad000000-0000-4000-8000-000000000013", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleAdministrator, AccountID: accountID, TargetMembershipID: adminMembershipID, ExpectedVersion: 1, Reason: "Peer administrator removal denied", At: now.Add(5 * time.Minute)}); !errors.Is(err, accountmembers.ErrTargetDenied) {
		t.Fatalf("administrator removed peer administrator: %v", err)
	}
	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "ae000000-0000-4000-8000-000000000014", ActorUserID: targetUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: adminMembershipID, ExpectedVersion: 1, Reason: "Administrator access concluded", At: now.Add(6 * time.Minute)}); err != nil {
		t.Fatalf("owner remove administrator: %v", err)
	}
	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "aa000000-0000-4000-8000-000000000010", ActorUserID: targetUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: ownerMembershipID, ExpectedVersion: 2, Reason: "Previous owner access concluded", At: now.Add(7 * time.Minute)}); err != nil {
		t.Fatalf("remove previous owner: %v", err)
	}
	members, err = repository.List(ctx, accountID)
	if err != nil || len(members) != 1 || members[0].UserID != targetUserID || members[0].Role != accounts.RoleOwner {
		t.Fatalf("final roster=%+v err=%v", members, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_membership_events WHERE account_id=$1`, accountID).Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("final audit event count=%d err=%v", eventCount, err)
	}
}
