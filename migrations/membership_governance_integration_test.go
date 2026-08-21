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
	noticePreparer := &ownershipNoticePreparer{}
	repository := postgresadapter.NewAccountMemberRepositoryWithOwnershipNotifications(pool, noticePreparer)
	reviewerDirectory := postgresadapter.NewAttentionReviewerDirectory(pool)
	members, err := repository.List(ctx, accountID)
	if err != nil || len(members) != 3 || members[0].Role != accounts.RoleOwner || members[0].Email != "owner@example.com" {
		t.Fatalf("initial roster=%+v err=%v", members, err)
	}
	if role, active, err := reviewerDirectory.ActiveRole(ctx, accountID, targetUserID); err != nil || !active || role != accounts.RoleMember {
		t.Fatalf("active reviewer role=%q active=%t err=%v", role, active, err)
	}

	changed, err := repository.ChangeRole(ctx, accountmembers.ChangeRoleMutation{EventID: "a6000000-0000-4000-8000-000000000006", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 1, Role: accounts.RoleAdministrator, Reason: "Prepare ownership successor", At: now.Add(time.Minute)})
	if err != nil || changed.Role != accounts.RoleAdministrator || changed.Version != 2 {
		t.Fatalf("role change=%+v err=%v", changed, err)
	}
	if _, err := repository.ChangeRole(ctx, accountmembers.ChangeRoleMutation{EventID: "a7000000-0000-4000-8000-000000000007", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 1, Role: accounts.RoleViewer, Reason: "Stale change", At: now.Add(2 * time.Minute)}); !errors.Is(err, accountmembers.ErrVersionConflict) {
		t.Fatalf("stale role change error=%v", err)
	}
	suspended, err := repository.ChangeState(ctx, accountmembers.StateMutation{EventID: "a7100000-0000-4000-8000-000000000017", Action: accountmembers.StateActionSuspend, ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 2, Reason: "Temporary access review", At: now.Add(2 * time.Minute)})
	if err != nil || suspended.State != accounts.MembershipSuspended || suspended.Role != accounts.RoleAdministrator || suspended.Version != 3 {
		t.Fatalf("suspend=%+v err=%v", suspended, err)
	}
	if _, err := repository.Current(ctx, accountID, targetUserID); !errors.Is(err, accountmembers.ErrMembershipNotFound) {
		t.Fatalf("suspended current Membership error=%v", err)
	}
	if role, active, err := reviewerDirectory.ActiveRole(ctx, accountID, targetUserID); err != nil || active || role != "" {
		t.Fatalf("suspended reviewer role=%q active=%t err=%v", role, active, err)
	}
	members, err = repository.List(ctx, accountID)
	suspendedVisible := false
	for _, member := range members {
		suspendedVisible = suspendedVisible || (member.MembershipID == targetMembershipID && member.State == accounts.MembershipSuspended)
	}
	if err != nil || len(members) != 3 || !suspendedVisible {
		t.Fatalf("suspended roster=%+v err=%v", members, err)
	}
	if _, err := repository.ChangeState(ctx, accountmembers.StateMutation{EventID: "a7200000-0000-4000-8000-000000000018", Action: accountmembers.StateActionSuspend, ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 3, Reason: "Duplicate suspension", At: now.Add(2 * time.Minute)}); !errors.Is(err, accountmembers.ErrStateConflict) {
		t.Fatalf("duplicate suspension error=%v", err)
	}
	reactivated, err := repository.ChangeState(ctx, accountmembers.StateMutation{EventID: "a7300000-0000-4000-8000-000000000019", Action: accountmembers.StateActionReactivate, ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 3, Reason: "Access review completed", At: now.Add(3 * time.Minute)})
	if err != nil || reactivated.State != accounts.MembershipActive || reactivated.Role != accounts.RoleAdministrator || reactivated.Version != 4 {
		t.Fatalf("reactivate=%+v err=%v", reactivated, err)
	}
	if role, active, err := reviewerDirectory.ActiveRole(ctx, accountID, targetUserID); err != nil || !active || role != accounts.RoleAdministrator {
		t.Fatalf("reactivated reviewer role=%q active=%t err=%v", role, active, err)
	}
	noticePreparer.fail = true
	if _, err := repository.TransferOwnership(ctx, accountmembers.TransferMutation{EventID: "a7400000-0000-4000-8000-000000000020", PreviousOwnerNoticeID: "a7500000-0000-4000-8000-000000000021", NewOwnerNoticeID: "a7600000-0000-4000-8000-000000000022", ActorUserID: ownerUserID, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedActorVersion: 1, ExpectedTargetVersion: 4, Reason: "Notification preparation must be atomic", At: now.Add(4 * time.Minute)}); err == nil {
		t.Fatal("ownership transfer committed despite notification preparation failure")
	}
	noticePreparer.fail = false
	noticePreparer.notices = nil
	var actorRole, targetRole accounts.MembershipRole
	var actorVersion, targetVersion uint64
	if err := pool.QueryRow(ctx, `SELECT role,version FROM memberships WHERE id=$1`, ownerMembershipID).Scan(&actorRole, &actorVersion); err != nil || actorRole != accounts.RoleOwner || actorVersion != 1 {
		t.Fatalf("actor changed after failed notification: role=%s version=%d err=%v", actorRole, actorVersion, err)
	}
	if err := pool.QueryRow(ctx, `SELECT role,version FROM memberships WHERE id=$1`, targetMembershipID).Scan(&targetRole, &targetVersion); err != nil || targetRole != accounts.RoleAdministrator || targetVersion != 4 {
		t.Fatalf("target changed after failed notification: role=%s version=%d err=%v", targetRole, targetVersion, err)
	}

	transferred, err := repository.TransferOwnership(ctx, accountmembers.TransferMutation{EventID: "a8000000-0000-4000-8000-000000000008", PreviousOwnerNoticeID: "a8100000-0000-4000-8000-000000000023", NewOwnerNoticeID: "a8200000-0000-4000-8000-000000000024", ActorUserID: ownerUserID, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedActorVersion: 1, ExpectedTargetVersion: 4, Reason: "Approved leadership transition", At: now.Add(4 * time.Minute)})
	if err != nil || transferred.PreviousOwner.Role != accounts.RoleAdministrator || transferred.PreviousOwner.Version != 2 || transferred.NewOwner.Role != accounts.RoleOwner || transferred.NewOwner.Version != 5 {
		t.Fatalf("transfer=%+v err=%v", transferred, err)
	}
	var ownerCount, eventCount, ownershipNoticeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE account_id=$1 AND role='owner' AND state='active'`, accountID).Scan(&ownerCount); err != nil || ownerCount != 1 {
		t.Fatalf("active owner count=%d err=%v", ownerCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_membership_events WHERE account_id=$1`, accountID).Scan(&eventCount); err != nil || eventCount != 4 {
		t.Fatalf("audit event count=%d err=%v", eventCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_notification_outbox WHERE kind='ownership_transfer'`).Scan(&ownershipNoticeCount); err != nil || ownershipNoticeCount != 2 || len(noticePreparer.notices) != 2 {
		t.Fatalf("ownership notices persisted=%d prepared=%d err=%v", ownershipNoticeCount, len(noticePreparer.notices), err)
	}
	if noticePreparer.notices[0].RecipientRole != accountmembers.OwnershipNoticePreviousOwner || noticePreparer.notices[0].Email != "owner@example.com" || noticePreparer.notices[1].RecipientRole != accountmembers.OwnershipNoticeNewOwner || noticePreparer.notices[1].Email != "successor@example.com" {
		t.Fatalf("ownership notice recipients=%+v", noticePreparer.notices)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_membership_events SET reason='tampered' WHERE account_id=$1`, accountID); err == nil {
		t.Fatal("immutable Membership audit accepted an update")
	}

	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "a9000000-0000-4000-8000-000000000009", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleAdministrator, AccountID: accountID, TargetMembershipID: targetMembershipID, ExpectedVersion: 5, Reason: "Attempt to remove active owner", At: now.Add(5 * time.Minute)}); !errors.Is(err, accountmembers.ErrOwnershipRequired) {
		t.Fatalf("remove active owner error=%v", err)
	}
	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "ad000000-0000-4000-8000-000000000013", ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleAdministrator, AccountID: accountID, TargetMembershipID: adminMembershipID, ExpectedVersion: 1, Reason: "Peer administrator removal denied", At: now.Add(6 * time.Minute)}); !errors.Is(err, accountmembers.ErrTargetDenied) {
		t.Fatalf("administrator removed peer administrator: %v", err)
	}
	if err := repository.Remove(ctx, accountmembers.RemoveMutation{EventID: "ae000000-0000-4000-8000-000000000014", ActorUserID: targetUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, TargetMembershipID: adminMembershipID, ExpectedVersion: 1, Reason: "Administrator access concluded", At: now.Add(7 * time.Minute)}); err != nil {
		t.Fatalf("owner remove administrator: %v", err)
	}
	if _, err := repository.ChangeState(ctx, accountmembers.StateMutation{EventID: "aa000000-0000-4000-8000-000000000010", Action: accountmembers.StateActionLeave, ActorUserID: targetUserID, ExpectedActorRole: accounts.RoleOwner, AccountID: accountID, ExpectedVersion: 5, Reason: "Owner cannot abandon Account", At: now.Add(8 * time.Minute)}); !errors.Is(err, accountmembers.ErrOwnershipRequired) {
		t.Fatalf("owner self-leave error=%v", err)
	}
	if _, err := repository.ChangeState(ctx, accountmembers.StateMutation{EventID: "af000000-0000-4000-8000-000000000015", Action: accountmembers.StateActionLeave, ActorUserID: ownerUserID, ExpectedActorRole: accounts.RoleAdministrator, AccountID: accountID, ExpectedVersion: 2, Reason: "Previous owner chose to leave", At: now.Add(9 * time.Minute)}); err != nil {
		t.Fatalf("previous owner self-leave: %v", err)
	}
	members, err = repository.List(ctx, accountID)
	if err != nil || len(members) != 1 || members[0].UserID != targetUserID || members[0].Role != accounts.RoleOwner {
		t.Fatalf("final roster=%+v err=%v", members, err)
	}
	invitationRepository := postgresadapter.NewInvitationRepository(pool)
	reinviteHash := [32]byte{1, 2, 3, 4}
	reinvite := accounts.Invitation{ID: "b1000000-0000-4000-8000-000000000021", AccountID: accountID, Email: "owner@example.com", Role: accounts.RoleViewer, State: accounts.InvitationPending, InvitedByUserID: targetUserID, TokenHash: reinviteHash, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(10 * time.Minute)}
	if err := invitationRepository.Create(ctx, reinvite); err != nil {
		t.Fatalf("reinvite departed identity: %v", err)
	}
	returned, err := invitationRepository.Accept(ctx, ownerUserID, reinviteHash, now.Add(11*time.Minute), "b2000000-0000-4000-8000-000000000022")
	if err != nil || returned.Role != accounts.RoleViewer || returned.State != accounts.MembershipActive || returned.Version != 1 || returned.ID == ownerMembershipID {
		t.Fatalf("new Membership after terminal leave=%+v err=%v", returned, err)
	}
	members, err = repository.List(ctx, accountID)
	if err != nil || len(members) != 2 {
		t.Fatalf("rejoined roster=%+v err=%v", members, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_membership_events WHERE account_id=$1`, accountID).Scan(&eventCount); err != nil || eventCount != 6 {
		t.Fatalf("final audit event count=%d err=%v", eventCount, err)
	}
}

type ownershipNoticePreparer struct {
	notices []accountmembers.OwnershipTransferNotice
	fail    bool
}

func (p *ownershipNoticePreparer) PrepareOwnershipTransfer(id string, notice accountmembers.OwnershipTransferNotice) (accountmembers.PreparedNotification, error) {
	p.notices = append(p.notices, notice)
	if p.fail {
		return accountmembers.PreparedNotification{}, errors.New("notification encryption unavailable")
	}
	return accountmembers.PreparedNotification{ID: id, AccountID: notice.AccountID, Ciphertext: []byte("encrypted:" + string(notice.RecipientRole)), Nonce: []byte{1, 2, 3}, KeyVersion: 1, CreatedAt: notice.OccurredAt}, nil
}
