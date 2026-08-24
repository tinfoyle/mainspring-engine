package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationAuthorizationRepository struct{ cell *database.CellPool }

func NewIntegrationAuthorizationRepository(cell *database.CellPool) (*IntegrationAuthorizationRepository, error) {
	if cell == nil {
		return nil, errors.New("Integration authorization cell pool is required")
	}
	return &IntegrationAuthorizationRepository{cell: cell}, nil
}

func (repository *IntegrationAuthorizationRepository) Authority(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (integrationauthorization.Authority, error) {
	var result integrationauthorization.Authority
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		connection, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, false)
		if err != nil {
			return err
		}
		revision, err := loadIntegrationRevision(ctx, tx, accountID, connection.CurrentRevisionID)
		if err != nil {
			return err
		}
		result = integrationauthorization.Authority{Connection: connection, Revision: revision}
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) Create(ctx context.Context, value domain.AuthorizationSession, role accounts.MembershipRole, event integrationauthorization.Event) (domain.AuthorizationSession, bool, error) {
	value, err := domain.RestoreAuthorizationSession(value)
	if err != nil || (role != accounts.RoleOwner && role != accounts.RoleAdministrator) || !event.Valid() || event.ActorID != string(value.CreatedBy.UserID) || !event.At.Equal(value.CreatedAt) {
		return domain.AuthorizationSession{}, false, integrationauthorization.ErrInvalid
	}
	result, created := value, false
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadIntegrationAuthorizationSession(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, matchErr := integrationAuthorizationEventMatches(ctx, tx, value, event)
			if matchErr != nil {
				return matchErr
			}
			if !matched || !reflect.DeepEqual(existing, value) {
				return integrationauthorization.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, integrationauthorization.ErrNotFound) {
			return loadErr
		}
		_, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_authorization_sessions
			(account_id,id,connection_id,connection_revision_id,provider,requested_scope,scope_revision_sha256,redirect_uri,state_sha256,
			 pkce_challenge_sha256,status,version,created_by_user_id,credential_id,credential_generation,error_code,created_at,updated_at,
			 expires_at,claimed_at,completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULL,0,NULL,$14,$14,$15,NULL,NULL)`, value.AccountID, value.ID,
			value.ConnectionID, value.ConnectionRevision, value.Provider, value.Scope, value.ScopeRevisionSHA256[:], value.RedirectURI,
			value.StateSHA256[:], value.PKCEChallengeSHA256[:], value.Status, value.Version, value.CreatedBy.UserID, value.CreatedAt, value.ExpiresAt)
		if err != nil {
			return err
		}
		payload := map[string]any{"connection_id": value.ConnectionID, "connection_revision_id": value.ConnectionRevision,
			"provider": value.Provider, "requested_scope": value.Scope, "expires_at": value.ExpiresAt}
		if err := insertIntegrationAuthorizationEvent(ctx, tx, value.AccountID, value.ID, event, payload); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) Get(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID) (domain.AuthorizationSession, error) {
	var result domain.AuthorizationSession
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadIntegrationAuthorizationSession(ctx, tx, accountID, sessionID, false)
		result = value
		return err
	})
	return result, classifyIntegrationAuthorization(err)
}

func loadIntegrationAuthorizationSession(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, lock bool) (domain.AuthorizationSession, error) {
	query := `SELECT id,account_id,connection_id,connection_revision_id,provider,requested_scope,scope_revision_sha256,redirect_uri,state_sha256,
		pkce_challenge_sha256,status,version,created_by_user_id::text,credential_id::text,credential_generation,COALESCE(error_code,''),
		created_at,updated_at,expires_at,claimed_at,completed_at FROM spyglass.integration_authorization_sessions WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.AuthorizationSession
	var scopeDigest, stateDigest, challengeDigest []byte
	var credentialID *string
	err := tx.QueryRow(ctx, query, accountID, sessionID).Scan(&value.ID, &value.AccountID, &value.ConnectionID, &value.ConnectionRevision,
		&value.Provider, &value.Scope, &scopeDigest, &value.RedirectURI, &stateDigest, &challengeDigest, &value.Status, &value.Version,
		&value.CreatedBy.UserID, &credentialID, &value.CredentialGeneration, &value.ErrorCode, &value.CreatedAt, &value.UpdatedAt,
		&value.ExpiresAt, &value.ClaimedAt, &value.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AuthorizationSession{}, integrationauthorization.ErrNotFound
	}
	if err != nil {
		return domain.AuthorizationSession{}, err
	}
	if len(scopeDigest) != sha256.Size || len(stateDigest) != sha256.Size || len(challengeDigest) != sha256.Size {
		return domain.AuthorizationSession{}, integrationauthorization.ErrRepository
	}
	copy(value.ScopeRevisionSHA256[:], scopeDigest)
	copy(value.StateSHA256[:], stateDigest)
	copy(value.PKCEChallengeSHA256[:], challengeDigest)
	if credentialID != nil {
		value.CredentialID = ids.IntegrationCredentialID(*credentialID)
	}
	value, err = domain.RestoreAuthorizationSession(value)
	if err != nil {
		return domain.AuthorizationSession{}, integrationauthorization.ErrRepository
	}
	return value, nil
}

func insertIntegrationAuthorizationEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, event integrationauthorization.Event, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_authorization_events
		(account_id,id,session_id,event_type,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, accountID, event.ID, sessionID, event.Type, event.ActorKind, event.ActorID,
		event.CorrelationID, raw, event.At)
	return err
}

func integrationAuthorizationEventMatches(ctx context.Context, tx pgx.Tx, value domain.AuthorizationSession, event integrationauthorization.Event) (bool, error) {
	var eventType, actorKind, actorID, correlationID string
	var occurredAt time.Time
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT event_type,actor_kind,actor_id,correlation_id::text,redacted_payload,occurred_at
		FROM spyglass.integration_authorization_events WHERE account_id=$1 AND id=$2 AND session_id=$3`, value.AccountID, event.ID, value.ID).
		Scan(&eventType, &actorKind, &actorID, &correlationID, &raw, &occurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expected, err := json.Marshal(map[string]any{"connection_id": value.ConnectionID, "connection_revision_id": value.ConnectionRevision,
		"provider": value.Provider, "requested_scope": value.Scope, "expires_at": value.ExpiresAt})
	if err != nil {
		return false, err
	}
	var actualValue, expectedValue any
	if json.Unmarshal(raw, &actualValue) != nil || json.Unmarshal(expected, &expectedValue) != nil {
		return false, integrationauthorization.ErrRepository
	}
	return eventType == event.Type && actorKind == event.ActorKind && actorID == event.ActorID && correlationID == event.CorrelationID &&
		occurredAt.Equal(event.At) && reflect.DeepEqual(actualValue, expectedValue), nil
}

func classifyIntegrationAuthorization(err error) error {
	if err == nil || errors.Is(err, integrationauthorization.ErrInvalid) || errors.Is(err, integrationauthorization.ErrNotFound) || errors.Is(err, integrationauthorization.ErrConflict) {
		return err
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001":
			return errors.Join(integrationauthorization.ErrConflict, err)
		case "23503", "23514", "P0001":
			return errors.Join(integrationauthorization.ErrInvalid, err)
		}
	}
	return errors.Join(integrationauthorization.ErrRepository, err)
}

var _ integrationauthorization.Repository = (*IntegrationAuthorizationRepository)(nil)
