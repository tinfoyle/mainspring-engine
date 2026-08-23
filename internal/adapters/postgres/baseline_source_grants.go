package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const baselineSourceGrantColumns = `id,account_id,assessment_id,connection_id::text,source_kind,folders,since_at,until_at,state,granted_by_user_id,revoked_by_user_id,revoke_reason,version,created_at,updated_at,revoked_at`

func (r *BaselineRepository) CreateSourceGrant(ctx context.Context, grant domain.SourceGrant, mutation baselineapp.Mutation) (domain.SourceGrant, error) {
	grant, err := domain.RestoreSourceGrant(grant)
	if err != nil || grant.State != domain.SourceGrantActive || !mutation.Valid() || !grant.CreatedAt.Equal(mutation.At) || mutation.ReasonCode != "source_granted" {
		return domain.SourceGrant{}, baselineapp.ErrInvalid
	}
	var result domain.SourceGrant
	err = r.cell.WithAccountTx(ctx, grant.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadBaselineSourceGrant(ctx, tx, grant.AccountID, grant.ID, false)
		if loadErr == nil {
			if !reflect.DeepEqual(existing, grant) {
				return baselineapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, baselineapp.ErrNotFound) {
			return loadErr
		}
		_, err := tx.Exec(ctx, `INSERT INTO spyglass.baseline_source_grants(account_id,id,assessment_id,connection_id,source_kind,folders,since_at,until_at,state,granted_by_user_id,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, grant.AccountID, grant.ID, grant.AssessmentID, grant.ConnectionID, grant.Kind, grant.Scope.Folders, grant.Scope.Since, grant.Scope.Until, grant.State, grant.GrantedBy.UserID, grant.Version, grant.CreatedAt, grant.UpdatedAt)
		if err != nil {
			return err
		}
		if err := insertBaselineSourceGrantEvent(ctx, tx, grant, "source_granted", 0, 1, mutation); err != nil {
			return err
		}
		result = grant
		return nil
	})
	return result, classifyBaseline(err)
}

func (r *BaselineRepository) ResolveSourceConnection(ctx context.Context, accountID ids.AccountID, connectionID string) (baselineapp.ResolvedSourceConnection, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(connectionID) != nil {
		return baselineapp.ResolvedSourceConnection{}, baselineapp.ErrInvalid
	}
	var result baselineapp.ResolvedSourceConnection
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT connection.connector_kind,revision.capabilities,revision.drive_folder_ids
			FROM spyglass.integration_connections connection JOIN spyglass.integration_connection_revisions revision
			ON revision.account_id=connection.account_id AND revision.connection_id=connection.id AND revision.revision=connection.current_revision
			WHERE connection.account_id=$1 AND connection.id=$2 AND connection.state='active'`, accountID, connectionID).
			Scan(&kind, &result.Capabilities, &result.DriveFolderIDs)
		if errors.Is(err, pgx.ErrNoRows) {
			return baselineapp.ErrConstraint
		}
		if err != nil {
			return err
		}
		switch kind {
		case "email":
			result.Kind = domain.SourceEmail
		case "google_drive":
			result.Kind = domain.SourceGoogleDrive
		default:
			return baselineapp.ErrConstraint
		}
		return nil
	})
	return result, classifyBaseline(err)
}

func (r *BaselineRepository) GetSourceGrant(ctx context.Context, accountID ids.AccountID, grantID ids.BaselineSourceGrantID) (domain.SourceGrant, error) {
	var result domain.SourceGrant
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadBaselineSourceGrant(ctx, tx, accountID, grantID, false)
		result = value
		return err
	})
	return result, classifyBaseline(err)
}

func (r *BaselineRepository) ListSourceGrants(ctx context.Context, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, after ids.BaselineSourceGrantID, limit uint16) (baselineapp.SourceGrantPage, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(assessmentID)) != nil || (after != "" && ids.Validate(string(after)) != nil) || limit == 0 || limit > 100 {
		return baselineapp.SourceGrantPage{}, baselineapp.ErrInvalid
	}
	result := baselineapp.SourceGrantPage{Items: make([]domain.SourceGrant, 0, limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+baselineSourceGrantColumns+` FROM spyglass.baseline_source_grants WHERE account_id=$1 AND assessment_id=$2 AND ($3::uuid IS NULL OR id>$3) ORDER BY id LIMIT $4`, accountID, assessmentID, nullableUUID(string(after)), int(limit)+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			value, scanErr := scanBaselineSourceGrant(rows)
			if scanErr != nil {
				return scanErr
			}
			result.Items = append(result.Items, value)
		}
		return rows.Err()
	})
	if err != nil {
		return baselineapp.SourceGrantPage{}, classifyBaseline(err)
	}
	if len(result.Items) > int(limit) {
		result.NextCursor = result.Items[limit-1].ID
		result.Items = result.Items[:limit]
	}
	return result, nil
}

func (r *BaselineRepository) UpdateSourceGrant(ctx context.Context, updated domain.SourceGrant, expected uint64, mutation baselineapp.Mutation) (domain.SourceGrant, error) {
	updated, err := domain.RestoreSourceGrant(updated)
	if err != nil || updated.State != domain.SourceGrantRevoked || updated.Version != expected+1 || !mutation.Valid() || mutation.ReasonCode != "source_revoked" || !updated.UpdatedAt.Equal(mutation.At) {
		return domain.SourceGrant{}, baselineapp.ErrInvalid
	}
	err = r.cell.WithAccountTx(ctx, updated.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadBaselineSourceGrant(ctx, tx, updated.AccountID, updated.ID, true)
		if err != nil {
			return err
		}
		if current.Version != expected {
			return baselineapp.ErrConflict
		}
		if !reflect.DeepEqual(current.SourceGrantDraft, updated.SourceGrantDraft) || current.State != domain.SourceGrantActive || updated.RevokedBy == nil || updated.RevokedAt == nil {
			return baselineapp.ErrConstraint
		}
		commandTag, err := tx.Exec(ctx, `UPDATE spyglass.baseline_source_grants SET state=$3,revoked_by_user_id=$4,revoke_reason=$5,version=$6,updated_at=$7,revoked_at=$8 WHERE account_id=$1 AND id=$2 AND version=$9`, updated.AccountID, updated.ID, updated.State, updated.RevokedBy.UserID, updated.Reason, updated.Version, updated.UpdatedAt, updated.RevokedAt, expected)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() != 1 {
			return baselineapp.ErrConflict
		}
		return insertBaselineSourceGrantEvent(ctx, tx, updated, "source_revoked", expected, updated.Version, mutation)
	})
	return updated, classifyBaseline(err)
}

type baselineSourceGrantScanner interface{ Scan(...any) error }

func loadBaselineSourceGrant(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, grantID ids.BaselineSourceGrantID, lock bool) (domain.SourceGrant, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(grantID)) != nil {
		return domain.SourceGrant{}, baselineapp.ErrInvalid
	}
	query := `SELECT ` + baselineSourceGrantColumns + ` FROM spyglass.baseline_source_grants WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanBaselineSourceGrant(tx.QueryRow(ctx, query, accountID, grantID))
}

func scanBaselineSourceGrant(row baselineSourceGrantScanner) (domain.SourceGrant, error) {
	var value domain.SourceGrant
	var revokedBy *ids.UserID
	if err := row.Scan(&value.ID, &value.AccountID, &value.AssessmentID, &value.ConnectionID, &value.Kind, &value.Scope.Folders, &value.Scope.Since, &value.Scope.Until, &value.State, &value.GrantedBy.UserID, &revokedBy, &value.Reason, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.RevokedAt); errors.Is(err, pgx.ErrNoRows) {
		return domain.SourceGrant{}, baselineapp.ErrNotFound
	} else if err != nil {
		return domain.SourceGrant{}, err
	}
	if revokedBy != nil {
		value.RevokedBy = &domain.Actor{UserID: *revokedBy}
	}
	value, err := domain.RestoreSourceGrant(value)
	if err != nil {
		return domain.SourceGrant{}, baselineapp.ErrRepository
	}
	return value, nil
}

func insertBaselineSourceGrantEvent(ctx context.Context, tx pgx.Tx, grant domain.SourceGrant, eventType string, from, to uint64, mutation baselineapp.Mutation) error {
	eventID, err := ids.Derive(mutation.CorrelationID, fmt.Sprintf("baseline-%s-%s", eventType, grant.ID))
	if err != nil {
		return baselineapp.ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.baseline_source_grant_events(account_id,id,grant_id,event_type,from_version,to_version,actor_user_id,reason_code,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (account_id,id) DO NOTHING`, grant.AccountID, eventID, grant.ID, eventType, from, to, mutation.Actor.UserID, mutation.ReasonCode, mutation.CorrelationID, map[string]any{"source_kind": grant.Kind, "folder_count": len(grant.Scope.Folders), "state": grant.State}, mutation.At)
	return err
}

var _ baselineapp.SourceGrantRepository = (*BaselineRepository)(nil)
var _ baselineapp.SourceConnectionResolver = (*BaselineRepository)(nil)
