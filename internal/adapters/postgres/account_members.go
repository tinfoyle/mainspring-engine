package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountMemberRepository struct {
	pool                   *pgxpool.Pool
	ownershipNotifications accountmembers.OwnershipNotificationPreparer
}

func NewAccountMemberRepository(pool *pgxpool.Pool) *AccountMemberRepository {
	return &AccountMemberRepository{pool: pool}
}

func NewAccountMemberRepositoryWithOwnershipNotifications(pool *pgxpool.Pool, preparer accountmembers.OwnershipNotificationPreparer) *AccountMemberRepository {
	return &AccountMemberRepository{pool: pool, ownershipNotifications: preparer}
}

func (r *AccountMemberRepository) List(ctx context.Context, accountID ids.AccountID) ([]accountmembers.Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id,m.user_id,u.display_name,u.primary_email,m.role,m.state,m.version,m.created_at
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.account_id=$1 AND m.state IN ('active','suspended')
		ORDER BY CASE WHEN m.role='owner' THEN 0 ELSE 1 END,lower(u.display_name),m.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]accountmembers.Member, 0)
	for rows.Next() {
		var member accountmembers.Member
		if err := rows.Scan(&member.MembershipID, &member.UserID, &member.DisplayName, &member.Email, &member.Role, &member.State, &member.Version, &member.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, member)
	}
	return result, rows.Err()
}

func (r *AccountMemberRepository) Current(ctx context.Context, accountID ids.AccountID, userID ids.UserID) (accountmembers.Member, error) {
	var member accountmembers.Member
	err := r.pool.QueryRow(ctx, `
		SELECT m.id,m.user_id,u.display_name,u.primary_email,m.role,m.state,m.version,m.created_at
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.account_id=$1 AND m.user_id=$2 AND m.state='active'`, accountID, userID).
		Scan(&member.MembershipID, &member.UserID, &member.DisplayName, &member.Email, &member.Role, &member.State, &member.Version, &member.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountmembers.Member{}, accountmembers.ErrMembershipNotFound
	}
	return member, err
}

func (r *AccountMemberRepository) ChangeRole(ctx context.Context, mutation accountmembers.ChangeRoleMutation) (accountmembers.Member, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountmembers.Member{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, target, err := lockActorAndTarget(ctx, tx, mutation.AccountID, mutation.ActorUserID, mutation.TargetMembershipID)
	if err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	if actor.State != accounts.MembershipActive || actor.Role != mutation.ExpectedActorRole || actor.Role != accounts.RoleOwner {
		return accountmembers.Member{}, accountmembers.ErrTargetDenied
	}
	if target.State != accounts.MembershipActive {
		return accountmembers.Member{}, accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.Member{}, accountmembers.ErrOwnershipRequired
	}
	if target.Version != mutation.ExpectedVersion {
		return accountmembers.Member{}, accountmembers.ErrVersionConflict
	}
	if !nonOwnerRole(mutation.Role) {
		return accountmembers.Member{}, accountmembers.ErrRoleInvalid
	}
	if target.Role != mutation.Role {
		previousRole := target.Role
		target.Role = mutation.Role
		target.Version++
		if _, err := tx.Exec(ctx, `UPDATE memberships SET role=$2,version=$3 WHERE id=$1`, target.MembershipID, target.Role, target.Version); err != nil {
			return accountmembers.Member{}, classifyMembershipMutation(err)
		}
		if err := insertMembershipEvent(ctx, tx, mutation.EventID, mutation.AccountID, mutation.ActorUserID, "role_changed", target.MembershipID, "", previousRole, target.Role, "", "", mutation.Reason, mutation.At); err != nil {
			return accountmembers.Member{}, classifyMembershipMutation(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	return target, nil
}

func (r *AccountMemberRepository) Remove(ctx context.Context, mutation accountmembers.RemoveMutation) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, target, err := lockActorAndTarget(ctx, tx, mutation.AccountID, mutation.ActorUserID, mutation.TargetMembershipID)
	if err != nil {
		return classifyMembershipMutation(err)
	}
	if actor.State != accounts.MembershipActive || actor.Role != mutation.ExpectedActorRole || (actor.Role != accounts.RoleOwner && actor.Role != accounts.RoleAdministrator) {
		return accountmembers.ErrTargetDenied
	}
	if target.State != accounts.MembershipActive && target.State != accounts.MembershipSuspended {
		return accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.ErrOwnershipRequired
	}
	if actor.Role == accounts.RoleAdministrator && target.Role == accounts.RoleAdministrator {
		return accountmembers.ErrTargetDenied
	}
	if target.Version != mutation.ExpectedVersion {
		return accountmembers.ErrVersionConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET state='removed',version=version+1 WHERE id=$1`, target.MembershipID); err != nil {
		return classifyMembershipMutation(err)
	}
	if err := insertMembershipEvent(ctx, tx, mutation.EventID, mutation.AccountID, mutation.ActorUserID, "membership_removed", target.MembershipID, "", target.Role, "", target.State, accounts.MembershipRemoved, mutation.Reason, mutation.At); err != nil {
		return classifyMembershipMutation(err)
	}
	return classifyMembershipMutation(tx.Commit(ctx))
}

func (r *AccountMemberRepository) ChangeState(ctx context.Context, mutation accountmembers.StateMutation) (accountmembers.Member, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountmembers.Member{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actor, target accountmembers.Member
	if mutation.Action == accountmembers.StateActionLeave {
		actor, err = lockCurrentMember(ctx, tx, mutation.AccountID, mutation.ActorUserID)
		target = actor
	} else {
		actor, target, err = lockActorAndTarget(ctx, tx, mutation.AccountID, mutation.ActorUserID, mutation.TargetMembershipID)
	}
	if err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	if actor.State != accounts.MembershipActive || actor.Role != mutation.ExpectedActorRole {
		return accountmembers.Member{}, accountmembers.ErrTargetDenied
	}
	if target.Version != mutation.ExpectedVersion {
		return accountmembers.Member{}, accountmembers.ErrVersionConflict
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.Member{}, accountmembers.ErrOwnershipRequired
	}
	if mutation.Action != accountmembers.StateActionLeave {
		if actor.Role != accounts.RoleOwner && actor.Role != accounts.RoleAdministrator {
			return accountmembers.Member{}, accountmembers.ErrTargetDenied
		}
		if actor.Role == accounts.RoleAdministrator && target.Role == accounts.RoleAdministrator {
			return accountmembers.Member{}, accountmembers.ErrTargetDenied
		}
	}
	previousState, nextState, action := target.State, accounts.MembershipState(""), ""
	switch mutation.Action {
	case accountmembers.StateActionLeave:
		if target.State != accounts.MembershipActive {
			return accountmembers.Member{}, accountmembers.ErrStateConflict
		}
		nextState, action = accounts.MembershipRemoved, "membership_left"
	case accountmembers.StateActionSuspend:
		if target.State != accounts.MembershipActive {
			return accountmembers.Member{}, accountmembers.ErrStateConflict
		}
		nextState, action = accounts.MembershipSuspended, "membership_suspended"
	case accountmembers.StateActionReactivate:
		if target.State != accounts.MembershipSuspended {
			return accountmembers.Member{}, accountmembers.ErrStateConflict
		}
		nextState, action = accounts.MembershipActive, "membership_reactivated"
	default:
		return accountmembers.Member{}, accountmembers.ErrStateConflict
	}
	command, err := tx.Exec(ctx, `UPDATE memberships SET state=$2,version=version+1 WHERE id=$1 AND version=$3`, target.MembershipID, nextState, target.Version)
	if err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	if command.RowsAffected() != 1 {
		return accountmembers.Member{}, accountmembers.ErrVersionConflict
	}
	newRole := target.Role
	if mutation.Action == accountmembers.StateActionLeave {
		newRole = ""
	}
	if err := insertMembershipEvent(ctx, tx, mutation.EventID, mutation.AccountID, mutation.ActorUserID, action, target.MembershipID, "", target.Role, newRole, previousState, nextState, mutation.Reason, mutation.At); err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	target.State, target.Version = nextState, target.Version+1
	if err := tx.Commit(ctx); err != nil {
		return accountmembers.Member{}, classifyMembershipMutation(err)
	}
	return target, nil
}

func (r *AccountMemberRepository) TransferOwnership(ctx context.Context, mutation accountmembers.TransferMutation) (accountmembers.TransferResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountmembers.TransferResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, target, err := lockActorAndTarget(ctx, tx, mutation.AccountID, mutation.ActorUserID, mutation.TargetMembershipID)
	if err != nil {
		return accountmembers.TransferResult{}, classifyMembershipMutation(err)
	}
	if actor.State != accounts.MembershipActive || actor.Role != accounts.RoleOwner {
		return accountmembers.TransferResult{}, accountmembers.ErrTargetDenied
	}
	if target.State != accounts.MembershipActive || target.MembershipID == actor.MembershipID {
		return accountmembers.TransferResult{}, accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.TransferResult{}, accountmembers.ErrOwnershipRequired
	}
	if actor.Version != mutation.ExpectedActorVersion || target.Version != mutation.ExpectedTargetVersion {
		return accountmembers.TransferResult{}, accountmembers.ErrVersionConflict
	}
	notices := make([]accountmembers.PreparedNotification, 0, 2)
	if r.ownershipNotifications != nil {
		if mutation.PreviousOwnerNoticeID == "" || mutation.NewOwnerNoticeID == "" {
			return accountmembers.TransferResult{}, errors.New("ownership notification IDs are required")
		}
		var accountName string
		if err := tx.QueryRow(ctx, `SELECT display_name FROM accounts WHERE id=$1`, mutation.AccountID).Scan(&accountName); err != nil {
			return accountmembers.TransferResult{}, err
		}
		previousNotice, err := r.ownershipNotifications.PrepareOwnershipTransfer(mutation.PreviousOwnerNoticeID, accountmembers.OwnershipTransferNotice{AccountID: mutation.AccountID, Email: actor.Email, DisplayName: actor.DisplayName, AccountName: accountName, CounterpartDisplayName: target.DisplayName, RecipientRole: accountmembers.OwnershipNoticePreviousOwner, OccurredAt: mutation.At})
		if err != nil {
			return accountmembers.TransferResult{}, err
		}
		newNotice, err := r.ownershipNotifications.PrepareOwnershipTransfer(mutation.NewOwnerNoticeID, accountmembers.OwnershipTransferNotice{AccountID: mutation.AccountID, Email: target.Email, DisplayName: target.DisplayName, AccountName: accountName, CounterpartDisplayName: actor.DisplayName, RecipientRole: accountmembers.OwnershipNoticeNewOwner, OccurredAt: mutation.At})
		if err != nil {
			return accountmembers.TransferResult{}, err
		}
		notices = append(notices, previousNotice, newNotice)
	}
	previousTargetRole := target.Role
	command, err := tx.Exec(ctx, `UPDATE memberships SET role=CASE WHEN id=$2 THEN 'owner' ELSE 'administrator' END,version=version+1 WHERE account_id=$1 AND id IN ($2,$3)`, mutation.AccountID, target.MembershipID, actor.MembershipID)
	if err != nil {
		return accountmembers.TransferResult{}, classifyMembershipMutation(err)
	}
	if command.RowsAffected() != 2 {
		return accountmembers.TransferResult{}, accountmembers.ErrVersionConflict
	}
	if err := insertMembershipEvent(ctx, tx, mutation.EventID, mutation.AccountID, mutation.ActorUserID, "ownership_transferred", target.MembershipID, actor.MembershipID, previousTargetRole, accounts.RoleOwner, "", "", mutation.Reason, mutation.At); err != nil {
		return accountmembers.TransferResult{}, classifyMembershipMutation(err)
	}
	for _, notice := range notices {
		if err := insertOwnershipNotification(ctx, tx, notice); err != nil {
			return accountmembers.TransferResult{}, classifyMembershipMutation(err)
		}
	}
	actor.Role, actor.Version = accounts.RoleAdministrator, actor.Version+1
	target.Role, target.Version = accounts.RoleOwner, target.Version+1
	if err := tx.Commit(ctx); err != nil {
		return accountmembers.TransferResult{}, classifyMembershipMutation(err)
	}
	return accountmembers.TransferResult{PreviousOwner: actor, NewOwner: target}, nil
}

func insertOwnershipNotification(ctx context.Context, tx pgx.Tx, notice accountmembers.PreparedNotification) error {
	if notice.ID == "" || notice.AccountID == "" || len(notice.Ciphertext) == 0 || len(notice.Nonce) == 0 || notice.KeyVersion <= 0 || notice.CreatedAt.IsZero() {
		return errors.New("prepared ownership notification is invalid")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO identity_notification_outbox
		(id,account_id,kind,ciphertext,nonce,key_version,processing_state,attempt_count,created_at)
		VALUES ($1,$2,'ownership_transfer',$3,$4,$5,'queued',0,$6)`, notice.ID, notice.AccountID, notice.Ciphertext, notice.Nonce, notice.KeyVersion, notice.CreatedAt.UTC())
	return err
}

func lockCurrentMember(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, actorUserID ids.UserID) (accountmembers.Member, error) {
	var member accountmembers.Member
	err := tx.QueryRow(ctx, `
		SELECT m.id,m.user_id,u.display_name,u.primary_email,m.role,m.state,m.version,m.created_at
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.account_id=$1 AND m.user_id=$2
		FOR UPDATE OF m`, accountID, actorUserID).
		Scan(&member.MembershipID, &member.UserID, &member.DisplayName, &member.Email, &member.Role, &member.State, &member.Version, &member.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountmembers.Member{}, accountmembers.ErrMembershipNotFound
	}
	return member, err
}

func lockActorAndTarget(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, actorUserID ids.UserID, targetMembershipID ids.MembershipID) (accountmembers.Member, accountmembers.Member, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.id,m.user_id,u.display_name,u.primary_email,m.role,m.state,m.version,m.created_at
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.account_id=$1 AND (m.user_id=$2 OR m.id=$3)
		ORDER BY m.id FOR UPDATE OF m`, accountID, actorUserID, targetMembershipID)
	if err != nil {
		return accountmembers.Member{}, accountmembers.Member{}, err
	}
	defer rows.Close()
	var actor, target accountmembers.Member
	for rows.Next() {
		var member accountmembers.Member
		if err := rows.Scan(&member.MembershipID, &member.UserID, &member.DisplayName, &member.Email, &member.Role, &member.State, &member.Version, &member.CreatedAt); err != nil {
			return accountmembers.Member{}, accountmembers.Member{}, err
		}
		if member.UserID == actorUserID {
			actor = member
		}
		if member.MembershipID == targetMembershipID {
			target = member
		}
	}
	if err := rows.Err(); err != nil {
		return accountmembers.Member{}, accountmembers.Member{}, err
	}
	if actor.MembershipID == "" || target.MembershipID == "" {
		return accountmembers.Member{}, accountmembers.Member{}, accountmembers.ErrMembershipNotFound
	}
	return actor, target, nil
}

func insertMembershipEvent(ctx context.Context, tx pgx.Tx, eventID string, accountID ids.AccountID, actorUserID ids.UserID, action string, targetID, previousOwnerID ids.MembershipID, previousRole, newRole accounts.MembershipRole, previousState, newState accounts.MembershipState, reason string, at time.Time) error {
	var previousOwner any
	if previousOwnerID != "" {
		previousOwner = previousOwnerID
	}
	var nextRole any
	if newRole != "" {
		nextRole = newRole
	}
	var beforeState, nextState any
	if previousState != "" {
		beforeState = previousState
	}
	if newState != "" {
		nextState = newState
	}
	_, err := tx.Exec(ctx, `INSERT INTO account_membership_events(id,account_id,actor_user_id,action,target_membership_id,previous_owner_membership_id,previous_role,new_role,previous_state,new_state,reason,occurred_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, eventID, accountID, actorUserID, action, targetID, previousOwner, previousRole, nextRole, beforeState, nextState, reason, at)
	return err
}

func nonOwnerRole(role accounts.MembershipRole) bool {
	return role == accounts.RoleAdministrator || role == accounts.RoleBillingAdmin || role == accounts.RoleMember || role == accounts.RoleViewer
}

func classifyMembershipMutation(err error) error {
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "40001" || postgresError.Code == "23505") {
		return accountmembers.ErrVersionConflict
	}
	return err
}

var _ accountmembers.Repository = (*AccountMemberRepository)(nil)
