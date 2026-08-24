package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationsRepository struct{ cell *database.CellPool }

func NewIntegrationsRepository(cell *database.CellPool) (*IntegrationsRepository, error) {
	if cell == nil {
		return nil, errors.New("Integrations cell pool is required")
	}
	return &IntegrationsRepository{cell: cell}, nil
}

func (repository *IntegrationsRepository) CreateConnection(ctx context.Context, input domain.ConnectionInput, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, bool, error) {
	value, revision, err := domain.NewConnection(input, role)
	if err != nil || !validIntegrationMutation(mutation, "connection_created", input.CreatedBy, input.CreatedAt) {
		return domain.Connection{}, false, integrationsapp.ErrInvalid
	}
	result, created := value, false
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadIntegrationConnection(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			existingRevision, err := loadIntegrationRevision(ctx, tx, value.AccountID, existing.CurrentRevisionID)
			matched, matchErr := integrationEventMatches(ctx, tx, value.AccountID, mutation.EventID, "connection", string(value.ID), mutation.Kind, mutation.Actor, mutation.CorrelationID)
			comparisonInput := input
			comparisonInput.CreatedAt = existing.CreatedAt
			comparison, comparisonRevision, comparisonErr := domain.NewConnection(comparisonInput, role)
			if err != nil || matchErr != nil || comparisonErr != nil || !matched || !reflect.DeepEqual(existing, comparison) || !reflect.DeepEqual(existingRevision, comparisonRevision) {
				if err != nil {
					return err
				}
				if matchErr != nil {
					return matchErr
				}
				return integrationsapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, integrationsapp.ErrNotFound) {
			return loadErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_connections
			(account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$9)`, value.AccountID, value.ID, value.Name, value.Kind, value.State,
			value.CurrentRevision, value.Version, value.CreatedBy.UserID, value.CreatedAt); err != nil {
			return err
		}
		if err := insertIntegrationRevision(ctx, tx, revision); err != nil {
			return err
		}
		if err := insertIntegrationEvent(ctx, tx, value.AccountID, mutation.EventID, "connection", string(value.ID), mutation.Kind, mutation.Actor,
			mutation.CorrelationID, map[string]any{"connector_kind": value.Kind, "state": value.State, "revision": value.CurrentRevision, "capability_count": len(revision.Capabilities)}, mutation.At); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) GetConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (domain.Connection, error) {
	var result domain.Connection
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, false)
		result = value
		return err
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) GetConnectionDetail(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (integrationsapp.ConnectionDetail, error) {
	var result integrationsapp.ConnectionDetail
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		connection, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, false)
		if err != nil {
			return err
		}
		revision, err := loadIntegrationRevision(ctx, tx, accountID, connection.CurrentRevisionID)
		if err != nil {
			return err
		}
		result.Connection, result.Revision = connection, revision
		var healthID ids.IntegrationHealthObservationID
		err = tx.QueryRow(ctx, `SELECT id FROM spyglass.integration_health_observations WHERE account_id=$1 AND connection_id=$2
			ORDER BY checked_at DESC,id LIMIT 1`, accountID, connectionID).Scan(&healthID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		health, err := loadIntegrationHealth(ctx, tx, accountID, healthID)
		if err != nil {
			return err
		}
		result.LatestHealth = &health
		return nil
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ListConnections(ctx context.Context, accountID ids.AccountID, query integrationsapp.ConnectionListQuery) (integrationsapp.ConnectionPage, error) {
	if !validIntegrationConnectionQuery(query) {
		return integrationsapp.ConnectionPage{}, integrationsapp.ErrInvalid
	}
	var page integrationsapp.ConnectionPage
	states, kinds := integrationStates(query.States), integrationKinds(query.Kinds)
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.integration_connections
			WHERE account_id=$1 AND (cardinality($2::text[])=0 OR state=ANY($2::text[]))
			AND (cardinality($3::text[])=0 OR connector_kind=ANY($3::text[]))
			AND ($4::timestamptz IS NULL OR updated_at<$4 OR (updated_at=$4 AND id>$5))
			ORDER BY updated_at DESC,id LIMIT $6`, accountID, states, kinds, integrationCursorTime(query.After), integrationCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var pageIDs []ids.IntegrationConnectionID
		for rows.Next() {
			var id ids.IntegrationConnectionID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			pageIDs = append(pageIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(pageIDs) > query.Limit
		if hasMore {
			pageIDs = pageIDs[:query.Limit]
		}
		for _, id := range pageIDs {
			value, err := loadIntegrationConnection(ctx, tx, accountID, id, false)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &integrationsapp.ConnectionCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
		return nil
	})
	return page, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ListHealth(ctx context.Context, accountID ids.AccountID, query integrationsapp.HealthListQuery) (integrationsapp.HealthPage, error) {
	if query.Limit < 1 || query.Limit > integrationsapp.MaximumConnectionPageSize || ids.Validate(string(query.ConnectionID)) != nil ||
		(query.After != nil && (query.After.CheckedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil)) {
		return integrationsapp.HealthPage{}, integrationsapp.ErrInvalid
	}
	var page integrationsapp.HealthPage
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.integration_health_observations WHERE account_id=$1 AND connection_id=$2
			AND ($3::timestamptz IS NULL OR checked_at<$3 OR (checked_at=$3 AND id>$4)) ORDER BY checked_at DESC,id LIMIT $5`,
			accountID, query.ConnectionID, integrationHealthCursorTime(query.After), integrationHealthCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var pageIDs []ids.IntegrationHealthObservationID
		for rows.Next() {
			var id ids.IntegrationHealthObservationID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			pageIDs = append(pageIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(pageIDs) > query.Limit
		if hasMore {
			pageIDs = pageIDs[:query.Limit]
		}
		for _, id := range pageIDs {
			value, err := loadIntegrationHealth(ctx, tx, accountID, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &integrationsapp.HealthCursor{CheckedAt: last.CheckedAt, ID: last.ID}
		}
		return nil
	})
	return page, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) GetExecution(ctx context.Context, accountID ids.AccountID, executionID ids.IntegrationExecutionID) (integrationsapp.ExecutionDetail, error) {
	var result integrationsapp.ExecutionDetail
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		execution, err := loadIntegrationExecution(ctx, tx, accountID, executionID)
		if err != nil {
			return err
		}
		result.Execution = execution
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.integration_execution_attempts WHERE account_id=$1 AND execution_id=$2 ORDER BY attempt_number`, accountID, executionID)
		if err != nil {
			return err
		}
		var attemptIDs []ids.IntegrationAttemptID
		for rows.Next() {
			var attemptID ids.IntegrationAttemptID
			if err := rows.Scan(&attemptID); err != nil {
				rows.Close()
				return err
			}
			attemptIDs = append(attemptIDs, attemptID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, attemptID := range attemptIDs {
			attempt, err := loadIntegrationAttempt(ctx, tx, accountID, attemptID)
			if err != nil {
				return err
			}
			result.Attempts = append(result.Attempts, attempt)
		}
		resolution, err := loadIntegrationExecutionResolution(ctx, tx, accountID, executionID)
		if err == nil {
			result.Resolution = &resolution
		} else if !errors.Is(err, integrationsapp.ErrNotFound) {
			return err
		}
		return nil
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) RequestExecutionResolution(ctx context.Context, accountID ids.AccountID,
	executionID ids.IntegrationExecutionID, resolutionID ids.IntegrationResolutionID, outcome domain.ExecutionState,
	evidence [32]byte, role accounts.MembershipRole, mutation integrationsapp.Mutation) error {
	if (role != accounts.RoleOwner && role != accounts.RoleAdministrator) ||
		!validIntegrationMutation(mutation, "execution_resolution_requested", mutation.Actor, mutation.At) {
		return integrationsapp.ErrInvalid
	}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		var changed bool
		if err := tx.QueryRow(ctx, `SELECT public.spyglass_request_integration_execution_resolution($1,$2,$3,$4,$5,$6,$7)`,
			accountID, executionID, resolutionID, outcome, evidence[:], mutation.Actor.UserID, mutation.At).Scan(&changed); err != nil {
			return err
		}
		if !changed {
			matched, err := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "execution", string(executionID), mutation.Kind,
				mutation.Actor, mutation.CorrelationID)
			if err != nil {
				return err
			}
			if !matched {
				return integrationsapp.ErrConflict
			}
			return nil
		}
		return insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "execution", string(executionID), mutation.Kind,
			mutation.Actor, mutation.CorrelationID, map[string]any{"resolution_id": resolutionID, "requested_outcome": outcome,
				"evidence_sha256": hex.EncodeToString(evidence[:])}, mutation.At)
	})
	return classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ConfirmExecutionResolution(ctx context.Context, accountID ids.AccountID,
	executionID ids.IntegrationExecutionID, resolutionID ids.IntegrationResolutionID, role accounts.MembershipRole,
	mutation integrationsapp.Mutation) error {
	if (role != accounts.RoleOwner && role != accounts.RoleAdministrator) ||
		!validIntegrationMutation(mutation, "execution_resolution_confirmed", mutation.Actor, mutation.At) {
		return integrationsapp.ErrInvalid
	}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		var changed bool
		if err := tx.QueryRow(ctx, `SELECT public.spyglass_confirm_integration_execution_resolution($1,$2,$3,$4,$5)`,
			accountID, executionID, resolutionID, mutation.Actor.UserID, mutation.At).Scan(&changed); err != nil {
			return err
		}
		if !changed {
			matched, err := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "execution", string(executionID), mutation.Kind,
				mutation.Actor, mutation.CorrelationID)
			if err != nil {
				return err
			}
			if !matched {
				return integrationsapp.ErrConflict
			}
			return nil
		}
		return insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "execution", string(executionID), mutation.Kind,
			mutation.Actor, mutation.CorrelationID, map[string]any{"resolution_id": resolutionID}, mutation.At)
	})
	return classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ListExecutions(ctx context.Context, accountID ids.AccountID, query integrationsapp.ExecutionListQuery) (integrationsapp.ExecutionPage, error) {
	if !validIntegrationExecutionQuery(query) {
		return integrationsapp.ExecutionPage{}, integrationsapp.ErrInvalid
	}
	var page integrationsapp.ExecutionPage
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM spyglass.integration_executions WHERE account_id=$1
			AND ($2::uuid IS NULL OR connection_id=$2) AND (cardinality($3::text[])=0 OR state=ANY($3::text[]))
			AND (cardinality($4::text[])=0 OR capability=ANY($4::text[]))
			AND ($5::timestamptz IS NULL OR updated_at<$5 OR (updated_at=$5 AND id>$6)) ORDER BY updated_at DESC,id LIMIT $7`,
			accountID, nullableIntegrationConnection(query.ConnectionID), integrationExecutionStates(query.States), integrationCapabilities(query.Capabilities),
			integrationExecutionCursorTime(query.After), integrationExecutionCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		var pageIDs []ids.IntegrationExecutionID
		for rows.Next() {
			var id ids.IntegrationExecutionID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			pageIDs = append(pageIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		hasMore := len(pageIDs) > query.Limit
		if hasMore {
			pageIDs = pageIDs[:query.Limit]
		}
		for _, id := range pageIDs {
			value, err := loadIntegrationExecution(ctx, tx, accountID, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, value)
		}
		if hasMore {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = &integrationsapp.ExecutionCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
		return nil
	})
	return page, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) PrepareExecution(ctx context.Context, request integrationsapp.PrepareExecutionRequest, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Execution, bool, error) {
	if !validIntegrationMutation(mutation, "execution_prepared", request.PreparedBy, request.PreparedAt) ||
		(role != accounts.RoleOwner && role != accounts.RoleAdministrator) {
		return domain.Execution{}, false, integrationsapp.ErrInvalid
	}
	var result domain.Execution
	created := false
	err := repository.cell.WithAccountTx(ctx, request.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadIntegrationExecution(ctx, tx, request.AccountID, request.ID)
		if loadErr == nil {
			matched, err := integrationEventMatches(ctx, tx, request.AccountID, mutation.EventID, "execution", string(request.ID), mutation.Kind,
				request.PreparedBy, mutation.CorrelationID)
			if err != nil {
				return err
			}
			if !matched || existing.ReleaseID != request.ReleaseID || existing.ReleaseVersion != request.ReleaseVersion || existing.Capability != request.Capability ||
				existing.ConnectionID != request.ConnectionID {
				return integrationsapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, integrationsapp.ErrNotFound) {
			return loadErr
		}
		connection, err := loadIntegrationConnection(ctx, tx, request.AccountID, request.ConnectionID, true)
		if err != nil {
			return err
		}
		revision, err := loadIntegrationRevision(ctx, tx, request.AccountID, connection.CurrentRevisionID)
		if err != nil {
			return err
		}
		credential, err := loadIntegrationCredential(ctx, tx, request.AccountID, connection.CredentialID, true)
		if err != nil {
			return err
		}
		var approvalID ids.ConsequentialApprovalID
		if err := tx.QueryRow(ctx, `SELECT approval_id FROM spyglass.marketing_release_plans WHERE account_id=$1 AND id=$2`, request.AccountID, request.ReleaseID).Scan(&approvalID); err != nil {
			return err
		}
		digest, err := integrationDeliveryManifestDigest(ctx, tx, request, connection, revision, credential)
		if err != nil {
			return err
		}
		value, err := domain.NewExecution(domain.ExecutionInput{ID: request.ID, AccountID: request.AccountID, ReleaseID: request.ReleaseID,
			ReleaseVersion: request.ReleaseVersion, ApprovalID: approvalID, Capability: request.Capability, Connection: connection,
			ConnectionRevision: revision, Credential: credential, PayloadSHA256: digest, CreatedAt: request.PreparedAt})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_executions
			(account_id,id,release_id,release_version,approval_id,capability,connection_id,connection_revision_id,connection_revision,
			 credential_id,credential_generation,payload_sha256,state,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`, value.AccountID, value.ID, value.ReleaseID, value.ReleaseVersion,
			value.ApprovalID, value.Capability, value.ConnectionID, value.ConnectionRevisionID, value.ConnectionRevision, value.CredentialID,
			value.CredentialGeneration, value.PayloadSHA256[:], value.State, value.CreatedAt); err != nil {
			return err
		}
		if err := insertIntegrationEvent(ctx, tx, request.AccountID, mutation.EventID, "execution", string(request.ID), mutation.Kind,
			request.PreparedBy, mutation.CorrelationID, map[string]any{"release_id": request.ReleaseID, "release_version": request.ReleaseVersion,
				"capability": request.Capability, "connection_id": request.ConnectionID, "connection_revision": value.ConnectionRevision,
				"credential_generation": value.CredentialGeneration}, mutation.At); err != nil {
			return err
		}
		result, created = value, true
		return nil
	})
	return result, created, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ReviseConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, input domain.ConnectionRevisionInput, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	if !validIntegrationMutation(mutation, "connection_revised", actor, mutation.At) {
		return domain.Connection{}, integrationsapp.ErrInvalid
	}
	var result domain.Connection
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 {
			storedRevision, err := loadIntegrationRevision(ctx, tx, accountID, current.CurrentRevisionID)
			matched, matchErr := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor, mutation.CorrelationID)
			target := domain.ConnectionRevision{ID: input.ID, AccountID: accountID, ConnectionID: connectionID, Revision: current.CurrentRevision,
				Capabilities: input.Capabilities, Scope: input.Scope, CreatedBy: actor, CreatedAt: storedRevision.CreatedAt}
			target, restoreErr := domain.RestoreConnectionRevision(target, current.Kind)
			if err != nil || matchErr != nil || restoreErr != nil || !matched || current.Name != strings.TrimSpace(input.Name) || !reflect.DeepEqual(storedRevision, target) {
				if err != nil {
					return err
				}
				if matchErr != nil {
					return matchErr
				}
				return integrationsapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return integrationsapp.ErrConflict
		}
		next, revision, err := current.Revise(expected, input, actor, role, mutation.At)
		if err != nil {
			return err
		}
		if err := insertIntegrationRevision(ctx, tx, revision); err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_connections SET name=$3,current_revision=$4,version=$5,updated_at=$6
			WHERE account_id=$1 AND id=$2 AND version=$7`, accountID, connectionID, next.Name, next.CurrentRevision, next.Version, next.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return integrationsapp.ErrConflict
		}
		if err := insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor,
			mutation.CorrelationID, map[string]any{"revision": next.CurrentRevision, "capability_count": len(revision.Capabilities)}, mutation.At); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) ActivateConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, input domain.CredentialInput, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	if !validIntegrationMutation(mutation, "connection_activated", actor, input.CreatedAt) || input.Generation != 1 {
		return domain.Connection{}, integrationsapp.ErrInvalid
	}
	var result domain.Connection
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 {
			credential, err := loadIntegrationCredential(ctx, tx, accountID, current.CredentialID, false)
			matched, matchErr := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor, mutation.CorrelationID)
			replayInput := input
			replayInput.CreatedAt = credential.CreatedAt
			replay, restoreErr := domain.NewCredentialBinding(replayInput, role)
			if err != nil || matchErr != nil || restoreErr != nil || !matched || current.State != domain.ConnectionActive || current.CredentialID != input.ID || !reflect.DeepEqual(credential, replay) {
				if err != nil {
					return err
				}
				if matchErr != nil {
					return matchErr
				}
				return integrationsapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return integrationsapp.ErrConflict
		}
		credential, err := domain.NewCredentialBinding(input, role)
		if err != nil {
			return err
		}
		next, err := current.Activate(expected, credential, actor, role, mutation.At)
		if err != nil {
			return err
		}
		if err := insertIntegrationCredential(ctx, tx, credential); err != nil {
			return err
		}
		if err := updateIntegrationConnectionCredential(ctx, tx, next, expected); err != nil {
			return err
		}
		if err := insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor,
			mutation.CorrelationID, map[string]any{"state": next.State, "credential_generation": credential.Generation}, mutation.At); err != nil {
			return err
		}
		if err := insertDerivedIntegrationEvent(ctx, tx, accountID, mutation, "integration-credential-bound", "credential", string(credential.ID), "credential_bound",
			map[string]any{"connection_id": connectionID, "generation": credential.Generation}); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) RotateCredential(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expectedVersion, expectedGeneration uint64, input domain.CredentialInput, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	if !validIntegrationMutation(mutation, "credential_rotated", actor, input.CreatedAt) || input.Generation != expectedGeneration+1 {
		return domain.Connection{}, integrationsapp.ErrInvalid
	}
	var result domain.Connection
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, true)
		if err != nil {
			return err
		}
		if current.Version == expectedVersion+1 {
			credential, err := loadIntegrationCredential(ctx, tx, accountID, current.CredentialID, false)
			matched, matchErr := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "credential", string(credential.ID), mutation.Kind, actor, mutation.CorrelationID)
			replayInput := input
			replayInput.CreatedAt = credential.CreatedAt
			replay, restoreErr := domain.NewCredentialBinding(replayInput, role)
			if err != nil || matchErr != nil || restoreErr != nil || !matched || current.CredentialGeneration != expectedGeneration+1 || !reflect.DeepEqual(credential, replay) {
				if err != nil {
					return err
				}
				if matchErr != nil {
					return matchErr
				}
				return integrationsapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expectedVersion || current.CredentialGeneration != expectedGeneration {
			return integrationsapp.ErrConflict
		}
		previous, err := loadIntegrationCredential(ctx, tx, accountID, current.CredentialID, true)
		if err != nil {
			return err
		}
		ended, replacement, err := previous.Rotate(input, expectedGeneration, actor, role, mutation.At)
		if err != nil {
			return err
		}
		next, err := current.BindCredential(expectedVersion, replacement, actor, role, mutation.At)
		if err != nil {
			return err
		}
		updatedCredential, err := tx.Exec(ctx, `UPDATE spyglass.integration_credentials SET state=$3,ended_by_user_id=$4,ended_at=$5,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND state='active'`, accountID, ended.ID, ended.State, ended.EndedBy.UserID, ended.UpdatedAt)
		if err != nil {
			return err
		}
		if updatedCredential.RowsAffected() != 1 {
			return integrationsapp.ErrConflict
		}
		if err := insertIntegrationCredential(ctx, tx, replacement); err != nil {
			return err
		}
		if err := updateIntegrationConnectionCredential(ctx, tx, next, expectedVersion); err != nil {
			return err
		}
		if err := insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "credential", string(replacement.ID), mutation.Kind, actor,
			mutation.CorrelationID, map[string]any{"connection_id": connectionID, "from_generation": expectedGeneration, "to_generation": replacement.Generation}, mutation.At); err != nil {
			return err
		}
		if err := insertDerivedIntegrationEvent(ctx, tx, accountID, mutation, "integration-credential-bound", "credential", string(replacement.ID), "credential_bound",
			map[string]any{"connection_id": connectionID, "generation": replacement.Generation}); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyIntegrations(err)
}

func (repository *IntegrationsRepository) DisableConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	return repository.transitionConnection(ctx, accountID, connectionID, expected, actor, role, mutation, domain.ConnectionDisabled)
}

func (repository *IntegrationsRepository) EnableConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	return repository.transitionConnection(ctx, accountID, connectionID, expected, actor, role, mutation, domain.ConnectionActive)
}

func (repository *IntegrationsRepository) RevokeConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation) (domain.Connection, error) {
	return repository.transitionConnection(ctx, accountID, connectionID, expected, actor, role, mutation, domain.ConnectionRevoked)
}

func (repository *IntegrationsRepository) transitionConnection(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation integrationsapp.Mutation, target domain.ConnectionState) (domain.Connection, error) {
	wantKind := map[domain.ConnectionState]string{domain.ConnectionDisabled: "connection_disabled", domain.ConnectionActive: "connection_activated", domain.ConnectionRevoked: "connection_revoked"}[target]
	if !validIntegrationMutation(mutation, wantKind, actor, mutation.At) {
		return domain.Connection{}, integrationsapp.ErrInvalid
	}
	var result domain.Connection
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, true)
		if err != nil {
			return err
		}
		if target == domain.ConnectionRevoked && current.Kind == domain.ConnectorGoogleDrive {
			return integrationsapp.ErrConflict
		}
		if current.Version == expected+1 {
			matched, err := integrationEventMatches(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor, mutation.CorrelationID)
			if err != nil {
				return err
			}
			if !matched || current.State != target {
				return integrationsapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return integrationsapp.ErrConflict
		}
		var next domain.Connection
		switch target {
		case domain.ConnectionDisabled:
			next, err = current.Disable(expected, actor, role, mutation.At)
		case domain.ConnectionActive:
			credential, loadErr := loadIntegrationCredential(ctx, tx, accountID, current.CredentialID, false)
			if loadErr != nil {
				return loadErr
			}
			next, err = current.Activate(expected, credential, actor, role, mutation.At)
		case domain.ConnectionRevoked:
			next, err = current.Revoke(expected, actor, role, mutation.At)
		}
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_connections SET state=$3,version=$4,revoked_by_user_id=$5,revoked_at=$6,updated_at=$7
			WHERE account_id=$1 AND id=$2 AND version=$8`, accountID, connectionID, next.State, next.Version, integrationActorID(next.RevokedBy), next.RevokedAt, next.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return integrationsapp.ErrConflict
		}
		if target == domain.ConnectionRevoked && current.CredentialID != "" {
			credential, loadErr := loadIntegrationCredential(ctx, tx, accountID, current.CredentialID, true)
			if loadErr != nil {
				return loadErr
			}
			if credential.State == domain.CredentialActive {
				ended, revokeErr := credential.Revoke(credential.Generation, actor, role, mutation.At)
				if revokeErr != nil {
					return revokeErr
				}
				updatedCredential, err := tx.Exec(ctx, `UPDATE spyglass.integration_credentials SET state=$3,ended_by_user_id=$4,ended_at=$5,updated_at=$5
					WHERE account_id=$1 AND id=$2 AND state='active'`, accountID, ended.ID, ended.State, ended.EndedBy.UserID, ended.UpdatedAt)
				if err != nil {
					return err
				}
				if updatedCredential.RowsAffected() != 1 {
					return integrationsapp.ErrConflict
				}
				if err := insertDerivedIntegrationEvent(ctx, tx, accountID, mutation, "integration-credential-revoked", "credential", string(credential.ID), "credential_revoked",
					map[string]any{"connection_id": connectionID, "generation": credential.Generation}); err != nil {
					return err
				}
			}
		}
		if err := insertIntegrationEvent(ctx, tx, accountID, mutation.EventID, "connection", string(connectionID), mutation.Kind, actor,
			mutation.CorrelationID, map[string]any{"state": next.State, "revision": next.CurrentRevision, "credential_generation": next.CredentialGeneration}, mutation.At); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyIntegrations(err)
}

func loadIntegrationConnection(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, lock bool) (domain.Connection, error) {
	query := `SELECT connection.id,connection.account_id,connection.name,connection.connector_kind,connection.state,revision.id,
		connection.current_revision,COALESCE(connection.credential_id::text,''),connection.credential_generation,connection.version,
		connection.created_by_user_id::text,connection.revoked_by_user_id::text,connection.created_at,connection.updated_at,connection.revoked_at
		FROM spyglass.integration_connections connection JOIN spyglass.integration_connection_revisions revision
		ON revision.account_id=connection.account_id AND revision.connection_id=connection.id AND revision.revision=connection.current_revision
		WHERE connection.account_id=$1 AND connection.id=$2`
	if lock {
		query += ` FOR UPDATE OF connection`
	}
	var value domain.Connection
	var revokedBy *string
	err := tx.QueryRow(ctx, query, accountID, connectionID).Scan(&value.ID, &value.AccountID, &value.Name, &value.Kind, &value.State, &value.CurrentRevisionID,
		&value.CurrentRevision, &value.CredentialID, &value.CredentialGeneration, &value.Version, &value.CreatedBy.UserID, &revokedBy,
		&value.CreatedAt, &value.UpdatedAt, &value.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Connection{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.Connection{}, err
	}
	if revokedBy != nil {
		value.RevokedBy = &domain.Actor{UserID: ids.UserID(*revokedBy)}
	}
	value, err = domain.RestoreConnection(value)
	if err != nil {
		return domain.Connection{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationRevision(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, revisionID ids.IntegrationConnectionRevisionID) (domain.ConnectionRevision, error) {
	var value domain.ConnectionRevision
	var capabilities []string
	var kind domain.ConnectorKind
	err := tx.QueryRow(ctx, `SELECT revision.id,revision.account_id,revision.connection_id,revision.revision,revision.capabilities,
		revision.email_address,revision.audience_reference,revision.https_origin,revision.path_prefix,revision.created_by_user_id::text,revision.created_at,
		revision.drive_folder_ids,connection.connector_kind FROM spyglass.integration_connection_revisions revision JOIN spyglass.integration_connections connection
		ON connection.account_id=revision.account_id AND connection.id=revision.connection_id WHERE revision.account_id=$1 AND revision.id=$2`, accountID, revisionID).
		Scan(&value.ID, &value.AccountID, &value.ConnectionID, &value.Revision, &capabilities, &value.Scope.EmailAddress, &value.Scope.AudienceReference,
			&value.Scope.HTTPSOrigin, &value.Scope.PathPrefix, &value.CreatedBy.UserID, &value.CreatedAt, &value.Scope.DriveFolderIDs, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ConnectionRevision{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.ConnectionRevision{}, err
	}
	for _, capability := range capabilities {
		value.Capabilities = append(value.Capabilities, domain.Capability(capability))
	}
	value, err = domain.RestoreConnectionRevision(value, kind)
	if err != nil {
		return domain.ConnectionRevision{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationCredential(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, credentialID ids.IntegrationCredentialID, lock bool) (domain.CredentialBinding, error) {
	query := `SELECT id,account_id,connection_id,generation,provider,reference_sha256,state,created_by_user_id::text,ended_by_user_id::text,
		created_at,updated_at,expires_at,ended_at FROM spyglass.integration_credentials WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.CredentialBinding
	var digest []byte
	var endedBy *string
	err := tx.QueryRow(ctx, query, accountID, credentialID).Scan(&value.ID, &value.AccountID, &value.ConnectionID, &value.Generation, &value.Provider, &digest,
		&value.State, &value.CreatedBy.UserID, &endedBy, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt, &value.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CredentialBinding{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.CredentialBinding{}, err
	}
	if len(digest) != 32 {
		return domain.CredentialBinding{}, integrationsapp.ErrRepository
	}
	copy(value.ReferenceSHA256[:], digest)
	if endedBy != nil {
		value.EndedBy = &domain.Actor{UserID: ids.UserID(*endedBy)}
	}
	value, err = domain.RestoreCredentialBinding(value)
	if err != nil {
		return domain.CredentialBinding{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationHealth(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, healthID ids.IntegrationHealthObservationID) (domain.HealthObservation, error) {
	var value domain.HealthObservation
	err := tx.QueryRow(ctx, `SELECT health.id,health.account_id,health.connection_id,revision.id,health.credential_id,health.state,
		COALESCE(health.error_code,''),health.latency_milliseconds,health.checked_at
		FROM spyglass.integration_health_observations health JOIN spyglass.integration_connection_revisions revision
		ON revision.account_id=health.account_id AND revision.connection_id=health.connection_id AND revision.revision=health.connection_revision
		WHERE health.account_id=$1 AND health.id=$2`, accountID, healthID).Scan(&value.ID, &value.AccountID, &value.ConnectionID,
		&value.ConnectionRevisionID, &value.CredentialID, &value.State, &value.ErrorCode, &value.LatencyMilliseconds, &value.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HealthObservation{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.HealthObservation{}, err
	}
	value, err = domain.RestoreHealthObservation(value)
	if err != nil {
		return domain.HealthObservation{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationExecution(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, executionID ids.IntegrationExecutionID) (domain.Execution, error) {
	var value domain.Execution
	var digest []byte
	err := tx.QueryRow(ctx, `SELECT id,account_id,release_id,release_version,approval_id,capability,connection_id,connection_revision_id,
		connection_revision,credential_id,credential_generation,payload_sha256,state,attempt_count,COALESCE(current_attempt_id::text,''),
		COALESCE(last_error_code,''),lease_expires_at,next_attempt_at,created_at,updated_at,completed_at
		FROM spyglass.integration_executions WHERE account_id=$1 AND id=$2`, accountID, executionID).Scan(&value.ID, &value.AccountID, &value.ReleaseID,
		&value.ReleaseVersion, &value.ApprovalID, &value.Capability, &value.ConnectionID, &value.ConnectionRevisionID, &value.ConnectionRevision,
		&value.CredentialID, &value.CredentialGeneration, &digest, &value.State, &value.AttemptCount, &value.CurrentAttemptID,
		&value.LastErrorCode, &value.LeaseExpiresAt, &value.NextAttemptAt, &value.CreatedAt, &value.UpdatedAt, &value.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Execution{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.Execution{}, err
	}
	if len(digest) != 32 {
		return domain.Execution{}, integrationsapp.ErrRepository
	}
	copy(value.PayloadSHA256[:], digest)
	value, err = domain.RestoreExecution(value)
	if err != nil {
		return domain.Execution{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationAttempt(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, attemptID ids.IntegrationAttemptID) (domain.Attempt, error) {
	var value domain.Attempt
	err := tx.QueryRow(ctx, `SELECT id,account_id,execution_id,attempt_number,mode,COALESCE(outcome,''),COALESCE(error_code,''),started_at,lease_expires_at,completed_at
		FROM spyglass.integration_execution_attempts WHERE account_id=$1 AND id=$2`, accountID, attemptID).Scan(&value.ID, &value.AccountID,
		&value.ExecutionID, &value.Number, &value.Mode, &value.Outcome, &value.ErrorCode, &value.StartedAt, &value.LeaseExpiresAt, &value.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Attempt{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.Attempt{}, err
	}
	value, err = domain.RestoreAttempt(value)
	if err != nil {
		return domain.Attempt{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func loadIntegrationExecutionResolution(ctx context.Context, tx pgx.Tx, accountID ids.AccountID,
	executionID ids.IntegrationExecutionID) (domain.ExecutionResolution, error) {
	var value domain.ExecutionResolution
	var evidence []byte
	var confirmedBy *ids.UserID
	err := tx.QueryRow(ctx, `SELECT id,account_id,execution_id,requested_outcome,evidence_sha256,requested_by_user_id,
		requested_at,state,confirmed_by_user_id,confirmed_at FROM spyglass.integration_execution_resolutions
		WHERE account_id=$1 AND execution_id=$2 ORDER BY requested_at DESC,id DESC LIMIT 1`, accountID, executionID).
		Scan(&value.ID, &value.AccountID, &value.ExecutionID, &value.RequestedOutcome, &evidence, &value.RequestedByUserID,
			&value.RequestedAt, &value.State, &confirmedBy, &value.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExecutionResolution{}, integrationsapp.ErrNotFound
	}
	if err != nil {
		return domain.ExecutionResolution{}, err
	}
	if len(evidence) != 32 {
		return domain.ExecutionResolution{}, integrationsapp.ErrRepository
	}
	copy(value.EvidenceSHA256[:], evidence)
	if confirmedBy != nil {
		value.ConfirmedByUserID = *confirmedBy
	}
	value, err = domain.RestoreExecutionResolution(value)
	if err != nil {
		return domain.ExecutionResolution{}, integrationsapp.ErrRepository
	}
	return value, nil
}

func insertIntegrationRevision(ctx context.Context, tx pgx.Tx, value domain.ConnectionRevision) error {
	capabilities := make([]string, len(value.Capabilities))
	for index, capability := range value.Capabilities {
		capabilities[index] = string(capability)
	}
	driveFolderIDs := value.Scope.DriveFolderIDs
	if driveFolderIDs == nil {
		driveFolderIDs = []string{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_connection_revisions
		(account_id,id,connection_id,revision,capabilities,email_address,audience_reference,https_origin,path_prefix,drive_folder_ids,created_by_user_id,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, value.AccountID, value.ID, value.ConnectionID, value.Revision, capabilities,
		value.Scope.EmailAddress, value.Scope.AudienceReference, value.Scope.HTTPSOrigin, value.Scope.PathPrefix, driveFolderIDs, value.CreatedBy.UserID, value.CreatedAt)
	return err
}

func insertIntegrationCredential(ctx context.Context, tx pgx.Tx, value domain.CredentialBinding) error {
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_credentials
		(account_id,id,connection_id,generation,provider,reference_sha256,state,created_by_user_id,created_at,updated_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)`, value.AccountID, value.ID, value.ConnectionID, value.Generation, value.Provider,
		value.ReferenceSHA256[:], value.State, value.CreatedBy.UserID, value.CreatedAt, value.ExpiresAt)
	return err
}

func integrationDeliveryManifestDigest(ctx context.Context, tx pgx.Tx, request integrationsapp.PrepareExecutionRequest, connection domain.Connection, revision domain.ConnectionRevision, credential domain.CredentialBinding) ([sha256.Size]byte, error) {
	input := integrationexecution.ManifestInput{ReleaseID: request.ReleaseID, ReleaseVersion: request.ReleaseVersion, Capability: request.Capability,
		ConnectionID: connection.ID, ConnectionRevisionID: revision.ID, ConnectionRevision: revision.Revision,
		CredentialID: credential.ID, CredentialGeneration: credential.Generation}
	rows, err := tx.Query(ctx, `SELECT revision.id,revision.content_sha256 FROM spyglass.marketing_release_assets release_asset
		JOIN spyglass.marketing_asset_revisions revision ON revision.account_id=release_asset.account_id AND revision.id=release_asset.asset_revision_id
		WHERE release_asset.account_id=$1 AND release_asset.release_id=$2 ORDER BY revision.id`, request.AccountID, request.ReleaseID)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	for rows.Next() {
		var value integrationexecution.ManifestAsset
		var digest []byte
		if err := rows.Scan(&value.ID, &digest); err != nil || len(digest) != sha256.Size {
			rows.Close()
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			return [sha256.Size]byte{}, integrationexecution.ErrInvalid
		}
		copy(value.SHA256[:], digest)
		input.Assets = append(input.Assets, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	_, digest, err := integrationexecution.BuildManifest(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return digest, nil
}

func updateIntegrationConnectionCredential(ctx context.Context, tx pgx.Tx, value domain.Connection, expected uint64) error {
	updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_connections SET state=$3,credential_id=$4,credential_generation=$5,version=$6,updated_at=$7
		WHERE account_id=$1 AND id=$2 AND version=$8`, value.AccountID, value.ID, value.State, value.CredentialID, value.CredentialGeneration,
		value.Version, value.UpdatedAt, expected)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return integrationsapp.ErrConflict
	}
	return nil
}

func insertIntegrationEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, eventID, aggregateKind, aggregateID, eventType string, actor domain.Actor, correlationID string, payload map[string]any, at time.Time) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_events
		(account_id,id,aggregate_kind,aggregate_id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,'user',$6,$7,$8,$9)`, accountID, eventID, aggregateKind, aggregateID, eventType, actor.UserID,
		correlationID, encoded, at.UTC())
	return err
}

func insertDerivedIntegrationEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, mutation integrationsapp.Mutation, label, aggregateKind, aggregateID, eventType string, payload map[string]any) error {
	eventID, err := ids.Derive(mutation.EventID, label)
	if err != nil {
		return err
	}
	return insertIntegrationEvent(ctx, tx, accountID, eventID, aggregateKind, aggregateID, eventType, mutation.Actor, mutation.CorrelationID, payload, mutation.At)
}

func integrationEventMatches(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, eventID, aggregateKind, aggregateID, eventType string, actor domain.Actor, correlationID string) (bool, error) {
	var storedKind, storedID, storedType, storedActor, storedCorrelation string
	err := tx.QueryRow(ctx, `SELECT aggregate_kind,aggregate_id,event_type,actor_id,correlation_id::text FROM spyglass.integration_events WHERE account_id=$1 AND id=$2`, accountID, eventID).
		Scan(&storedKind, &storedID, &storedType, &storedActor, &storedCorrelation)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && storedKind == aggregateKind && storedID == aggregateID && storedType == eventType && storedActor == string(actor.UserID) && storedCorrelation == correlationID, err
}

func validIntegrationMutation(mutation integrationsapp.Mutation, kind string, actor domain.Actor, at time.Time) bool {
	return mutation.Valid() && mutation.Kind == kind && mutation.Actor == actor && mutation.At.UTC().Equal(at.UTC())
}

func classifyIntegrations(err error) error {
	if err == nil || errors.Is(err, integrationsapp.ErrInvalid) || errors.Is(err, integrationsapp.ErrNotFound) || errors.Is(err, integrationsapp.ErrConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if postgresError.Code == "P2001" {
			return errors.Join(integrationsapp.ErrNotFound, err)
		}
		if postgresError.Code == "P2004" || postgresError.Code == "P2005" {
			return errors.Join(integrationsapp.ErrConflict, err)
		}
		if postgresError.Code == "23505" || postgresError.Code == "40001" {
			return errors.Join(integrationsapp.ErrConflict, err)
		}
		if postgresError.Code == "23503" || postgresError.Code == "23514" || postgresError.Code == "P0001" {
			return errors.Join(integrationsapp.ErrInvalid, err)
		}
	}
	return integrationsapp.ClassifyForAdapter(err)
}

func integrationStates(values []domain.ConnectionState) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func integrationKinds(values []domain.ConnectorKind) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func integrationCursorTime(value *integrationsapp.ConnectionCursor) *time.Time {
	if value == nil {
		return nil
	}
	return &value.UpdatedAt
}

func integrationCursorID(value *integrationsapp.ConnectionCursor) any {
	if value == nil {
		return nil
	}
	return value.ID
}

func validIntegrationConnectionQuery(query integrationsapp.ConnectionListQuery) bool {
	if query.Limit < 1 || query.Limit > integrationsapp.MaximumConnectionPageSize {
		return false
	}
	seenStates := make(map[domain.ConnectionState]struct{}, len(query.States))
	for _, state := range query.States {
		if state != domain.ConnectionPending && state != domain.ConnectionActive && state != domain.ConnectionDisabled && state != domain.ConnectionRevoked {
			return false
		}
		if _, exists := seenStates[state]; exists {
			return false
		}
		seenStates[state] = struct{}{}
	}
	seenKinds := make(map[domain.ConnectorKind]struct{}, len(query.Kinds))
	for _, kind := range query.Kinds {
		if kind != domain.ConnectorEmail && kind != domain.ConnectorWebPublish {
			return false
		}
		if _, exists := seenKinds[kind]; exists {
			return false
		}
		seenKinds[kind] = struct{}{}
	}
	return query.After == nil || (!query.After.UpdatedAt.IsZero() && ids.Validate(string(query.After.ID)) == nil)
}

func validIntegrationExecutionQuery(query integrationsapp.ExecutionListQuery) bool {
	if query.Limit < 1 || query.Limit > integrationsapp.MaximumConnectionPageSize ||
		(query.ConnectionID != "" && ids.Validate(string(query.ConnectionID)) != nil) ||
		(query.After != nil && (query.After.UpdatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil)) {
		return false
	}
	seenStates := make(map[domain.ExecutionState]struct{}, len(query.States))
	for _, state := range query.States {
		if _, exists := seenStates[state]; exists || (state != domain.ExecutionPrepared && state != domain.ExecutionExecuting && state != domain.ExecutionReconciling &&
			state != domain.ExecutionRetryWait && state != domain.ExecutionUnknown && state != domain.ExecutionManualResolution && state != domain.ExecutionSucceeded &&
			state != domain.ExecutionFailed && state != domain.ExecutionCancelled) {
			return false
		}
		seenStates[state] = struct{}{}
	}
	seenCapabilities := make(map[domain.Capability]struct{}, len(query.Capabilities))
	for _, capability := range query.Capabilities {
		if _, exists := seenCapabilities[capability]; exists || (capability != domain.CapabilityEmailSend && capability != domain.CapabilityWebPublish) {
			return false
		}
		seenCapabilities[capability] = struct{}{}
	}
	return true
}

func integrationHealthCursorTime(value *integrationsapp.HealthCursor) *time.Time {
	if value == nil {
		return nil
	}
	return &value.CheckedAt
}

func integrationHealthCursorID(value *integrationsapp.HealthCursor) any {
	if value == nil {
		return nil
	}
	return value.ID
}

func integrationExecutionStates(values []domain.ExecutionState) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func integrationCapabilities(values []domain.Capability) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func integrationExecutionCursorTime(value *integrationsapp.ExecutionCursor) *time.Time {
	if value == nil {
		return nil
	}
	return &value.UpdatedAt
}

func integrationExecutionCursorID(value *integrationsapp.ExecutionCursor) any {
	if value == nil {
		return nil
	}
	return value.ID
}

func nullableIntegrationConnection(value ids.IntegrationConnectionID) any {
	if value == "" {
		return nil
	}
	return value
}

func integrationActorID(value *domain.Actor) any {
	if value == nil {
		return nil
	}
	return value.UserID
}
