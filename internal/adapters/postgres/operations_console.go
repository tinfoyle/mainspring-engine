package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// OperationsConsoleRepository calls only audited security-definer projections.
// The runtime database role needs no SELECT privilege on customer tables.
type OperationsConsoleRepository struct{ pool *pgxpool.Pool }

func NewOperationsConsoleRepository(pool *pgxpool.Pool) *OperationsConsoleRepository {
	return &OperationsConsoleRepository{pool: pool}
}

func (repository *OperationsConsoleRepository) Staff(ctx context.Context, userID ids.UserID) (operations.Staff, error) {
	var result operations.Staff
	var roles []string
	err := repository.pool.QueryRow(ctx, `SELECT user_id,display_name,staff_state,roles FROM spyglass_operations_current_staff($1)`, userID).
		Scan(&result.UserID, &result.DisplayName, &result.State, &roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.Staff{}, operations.ErrStaffUnauthorized
	}
	if err != nil {
		return operations.Staff{}, fmt.Errorf("load operations staff: %w", err)
	}
	result.Roles = make([]operations.StaffRole, len(roles))
	for index, role := range roles {
		result.Roles[index] = operations.StaffRole(role)
	}
	return result, nil
}

func (repository *OperationsConsoleRepository) Lookup(ctx context.Context, staff operations.Staff, query operations.LookupQuery, eventID ids.OperationsAuditEventID, environment string, at time.Time) ([]operations.LookupResult, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT user_id,display_name,email,user_state,email_verified,account_id,account_name,account_state,account_type,
		       membership_role,membership_state,stripe_customer_id,stripe_subscription_id
		FROM spyglass_operations_lookup($1,$2,$3,$4,$5,$6,$7)`,
		eventID, staff.UserID, query.Kind, query.Value, query.Audit.Ticket, query.Audit.Reason, environment)
	if err != nil {
		return nil, classifyOperationsError(err)
	}
	defer rows.Close()
	result := make([]operations.LookupResult, 0)
	for rows.Next() {
		var value operations.LookupResult
		if err := rows.Scan(&value.UserID, &value.DisplayName, &value.Email, &value.UserState, &value.EmailVerified,
			&value.AccountID, &value.AccountName, &value.AccountState, &value.AccountType, &value.MembershipRole,
			&value.MembershipState, &value.StripeCustomerID, &value.StripeSubscription); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, classifyOperationsError(rows.Err())
}

func (repository *OperationsConsoleRepository) CreateGrant(ctx context.Context, grant operations.SupportGrant, eventID ids.OperationsAuditEventID, environment string) (operations.SupportGrant, error) {
	created, err := scanSupportGrant(repository.pool.QueryRow(ctx, `
		SELECT id,staff_user_id,target_user_id,account_id,state,ticket,reason,created_at,expires_at,revoked_at,version
		FROM spyglass_operations_create_support_grant($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		eventID, grant.ID, grant.StaffUserID, grant.TargetUserID, grant.AccountID, grant.Ticket, grant.Reason,
		environment, grant.CreatedAt, grant.ExpiresAt))
	if err != nil {
		return operations.SupportGrant{}, classifyOperationsError(err)
	}
	if created.ID != grant.ID || created.StaffUserID != grant.StaffUserID || created.TargetUserID != grant.TargetUserID ||
		created.AccountID != grant.AccountID || created.State != grant.State || created.Ticket != grant.Ticket ||
		created.Reason != grant.Reason || !sameDatabaseInstant(created.CreatedAt, grant.CreatedAt) || !sameDatabaseInstant(created.ExpiresAt, grant.ExpiresAt) ||
		created.RevokedAt != nil || created.Version != grant.Version {
		return operations.SupportGrant{}, errors.New("created operations support grant does not match requested grant")
	}
	return created, nil
}

func sameDatabaseInstant(left, right time.Time) bool {
	difference := left.Sub(right)
	return difference >= -time.Microsecond && difference <= time.Microsecond
}

func (repository *OperationsConsoleRepository) Grant(ctx context.Context, grantID ids.OperationsSupportGrantID, staffUserID ids.UserID) (operations.SupportGrant, error) {
	grant, err := scanSupportGrant(repository.pool.QueryRow(ctx, `
		SELECT id,staff_user_id,target_user_id,account_id,state,ticket,reason,created_at,expires_at,revoked_at,version
		FROM spyglass_operations_get_support_grant($1,$2)`, staffUserID, grantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.SupportGrant{}, operations.ErrGrantDenied
	}
	return grant, classifyOperationsError(err)
}

func (repository *OperationsConsoleRepository) ViewAccount(ctx context.Context, staff operations.Staff, grant operations.SupportGrant, audit operations.AuditReason, eventID ids.OperationsAuditEventID, environment string, at time.Time) (operations.AccountView, error) {
	var result operations.AccountView
	var packages []byte
	row := repository.pool.QueryRow(ctx, `
		SELECT user_id,user_display_name,user_email,user_state,user_email_verified_at,user_created_at,passkey_count,recovery_codes_remaining,
		       account_id,account_display_name,account_state,account_type,cell_id,placement_generation,entitlement_version,account_created_at,
		       membership_role,membership_state,membership_version,stripe_customer_id,billing_email,subscription_id,provider_mode,
		       subscription_state,offer_code,offer_version,current_period_end,cancel_at,last_synced_at,snapshot_version,catalog_version,
		       evaluated_at,effective_packages,ai_tokens_available,ai_tokens_reserved,ai_tokens_consumed,closure_request_id,closure_state,
		       closure_execute_after,closure_delete_after,closure_blocker_code
		FROM spyglass_operations_account_view($1,$2,$3,$4,$5,$6,$7)`,
		eventID, staff.UserID, grant.ID, audit.Ticket, audit.Reason, environment, at)
	err := row.Scan(
		&result.User.ID, &result.User.DisplayName, &result.User.Email, &result.User.State, &result.User.EmailVerifiedAt,
		&result.User.CreatedAt, &result.User.PasskeyCount, &result.User.RecoveryCodesRemaining,
		&result.Account.ID, &result.Account.DisplayName, &result.Account.State, &result.Account.Type, &result.Account.CellID,
		&result.Account.PlacementGeneration, &result.Account.EntitlementVersion, &result.Account.CreatedAt,
		&result.Membership.Role, &result.Membership.State, &result.Membership.Version,
		&result.Billing.CustomerID, &result.Billing.BillingEmail, &result.Billing.SubscriptionID, &result.Billing.ProviderMode,
		&result.Billing.State, &result.Billing.OfferCode, &result.Billing.OfferVersion, &result.Billing.CurrentPeriodEnd,
		&result.Billing.CancelAt, &result.Billing.LastSyncedAt, &result.Entitlements.Version, &result.Entitlements.CatalogVersion,
		&result.Entitlements.EvaluatedAt, &packages, &result.AITokens.Available, &result.AITokens.Reserved,
		&result.AITokens.Consumed, &result.Lifecycle.RequestID, &result.Lifecycle.State, &result.Lifecycle.ExecuteAfter,
		&result.Lifecycle.DeleteAfter, &result.Lifecycle.BlockerCode)
	if err != nil {
		return operations.AccountView{}, classifyOperationsError(err)
	}
	if err := json.Unmarshal(packages, &result.Entitlements.Packages); err != nil {
		return operations.AccountView{}, fmt.Errorf("decode operations entitlement projection: %w", err)
	}
	result.Grant = grant
	history, err := repository.supportHistory(ctx, staff.UserID, grant.ID, 50, at)
	if err != nil {
		return operations.AccountView{}, err
	}
	result.SupportHistory = history
	return result, nil
}

func (repository *OperationsConsoleRepository) supportHistory(ctx context.Context, staffUserID ids.UserID, grantID ids.OperationsSupportGrantID, limit int, at time.Time) ([]operations.AccessEvent, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id,staff_display_name,action,ticket,reason,occurred_at
		FROM spyglass_operations_support_history($1,$2,$3,$4)`, staffUserID, grantID, limit, at)
	if err != nil {
		return nil, classifyOperationsError(err)
	}
	defer rows.Close()
	result := make([]operations.AccessEvent, 0)
	for rows.Next() {
		var event operations.AccessEvent
		if err := rows.Scan(&event.ID, &event.StaffDisplayName, &event.Action, &event.Ticket, &event.Reason, &event.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, classifyOperationsError(rows.Err())
}

func (repository *OperationsConsoleRepository) RevokeGrant(ctx context.Context, staff operations.Staff, grantID ids.OperationsSupportGrantID, expectedVersion uint64, audit operations.AuditReason, eventID ids.OperationsAuditEventID, environment string, at time.Time) (operations.SupportGrant, error) {
	grant, err := scanSupportGrant(repository.pool.QueryRow(ctx, `
		SELECT id,staff_user_id,target_user_id,account_id,state,ticket,reason,created_at,expires_at,revoked_at,version
		FROM spyglass_operations_revoke_support_grant($1,$2,$3,$4,$5,$6,$7,$8)`, eventID, staff.UserID,
		grantID, expectedVersion, audit.Ticket, audit.Reason, environment, at))
	return grant, classifyOperationsError(err)
}

func (repository *OperationsConsoleRepository) CustomerHistory(ctx context.Context, userID ids.UserID, accountID ids.AccountID, limit int) ([]operations.AccessEvent, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id,staff_display_name,action,ticket,reason,occurred_at
		FROM spyglass_operations_customer_access_history($1,$2,$3)`, userID, accountID, limit)
	if err != nil {
		return nil, classifyOperationsError(err)
	}
	defer rows.Close()
	result := make([]operations.AccessEvent, 0)
	for rows.Next() {
		var event operations.AccessEvent
		if err := rows.Scan(&event.ID, &event.StaffDisplayName, &event.Action, &event.Ticket, &event.Reason, &event.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, classifyOperationsError(rows.Err())
}

func (repository *OperationsConsoleRepository) Analytics(ctx context.Context, staff operations.Staff, query analyticsreport.Query, audit operations.AuditReason, eventID ids.OperationsAuditEventID, environment string, at time.Time) (analyticsreport.Report, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT bucket_start,event_name,surface,dimension_value,event_count,unique_subjects
		FROM spyglass_operations_analytics_report($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		eventID, staff.UserID, query.From, query.To, query.Bucket, query.Dimension, query.MinimumCohort,
		audit.Ticket, audit.Reason, environment, at)
	if err != nil {
		return analyticsreport.Report{}, classifyOperationsError(err)
	}
	defer rows.Close()
	result := analyticsreport.Report{From: query.From.UTC(), To: query.To.UTC(), Bucket: query.Bucket, Dimension: query.Dimension, MinimumCohort: query.MinimumCohort, Rows: make([]analyticsreport.Row, 0)}
	for rows.Next() {
		var value analyticsreport.Row
		if err := rows.Scan(&value.BucketStart, &value.EventName, &value.Surface, &value.Dimension, &value.EventCount, &value.UniqueSubjects); err != nil {
			return analyticsreport.Report{}, err
		}
		result.Rows = append(result.Rows, value)
	}
	return result, classifyOperationsError(rows.Err())
}

func (repository *OperationsConsoleRepository) RecordAuthentication(ctx context.Context, staff operations.Staff, sessionID ids.SessionID, eventID ids.OperationsAuditEventID, environment string, at time.Time) error {
	_, err := repository.pool.Exec(ctx, `SELECT spyglass_operations_record_session_event($1,$2,$3,'staff_authenticated',$4,$5)`, eventID, staff.UserID, sessionID, environment, at)
	return classifyOperationsError(err)
}

func (repository *OperationsConsoleRepository) RecordLogout(ctx context.Context, staff operations.Staff, sessionID ids.SessionID, eventID ids.OperationsAuditEventID, environment string, at time.Time) error {
	_, err := repository.pool.Exec(ctx, `SELECT spyglass_operations_record_session_event($1,$2,$3,'staff_logged_out',$4,$5)`, eventID, staff.UserID, sessionID, environment, at)
	return classifyOperationsError(err)
}

func scanSupportGrant(row pgx.Row) (operations.SupportGrant, error) {
	var result operations.SupportGrant
	err := row.Scan(&result.ID, &result.StaffUserID, &result.TargetUserID, &result.AccountID, &result.State,
		&result.Ticket, &result.Reason, &result.CreatedAt, &result.ExpiresAt, &result.RevokedAt, &result.Version)
	return result, err
}

func classifyOperationsError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return operations.ErrInvalidInput
		case "42501":
			return operations.ErrGrantDenied
		case "P0002":
			return operations.ErrNotFound
		case "P0001":
			return operations.ErrGrantDenied
		}
	}
	return fmt.Errorf("operations console repository unavailable: %w", err)
}

var _ operationsconsole.Repository = (*OperationsConsoleRepository)(nil)
