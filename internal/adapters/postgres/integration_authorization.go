package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func (repository *IntegrationAuthorizationRepository) ExpireAuthorization(ctx context.Context, accountID ids.AccountID,
	sessionID ids.IntegrationAuthorizationSessionID, at time.Time) (domain.AuthorizationSession, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil || at.IsZero() {
		return domain.AuthorizationSession{}, integrationauthorization.ErrInvalid
	}
	var result domain.AuthorizationSession
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		session, err := loadIntegrationAuthorizationSession(ctx, tx, accountID, sessionID, true)
		if err != nil {
			return err
		}
		if session.Status != domain.AuthorizationPending {
			result = session
			return nil
		}
		next, err := session.Expire(session.Version, at)
		if err != nil {
			return err
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, next, session.Version); err != nil {
			return err
		}
		eventID, _ := ids.Derive(string(session.ID), "integration-authorization-expired")
		if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, session.ID,
			integrationauthorization.Event{ID: eventID, Type: "authorization_expired", ActorKind: "workload", ActorID: "integration-authorization",
				CorrelationID: string(session.ID), At: at.UTC()}, map[string]any{"error_code": "authorization_expired"}); err != nil {
			return err
		}
		result = next
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) RejectCallback(ctx context.Context, accountID ids.AccountID, stateSHA256 [sha256.Size]byte,
	code string, at time.Time) (domain.AuthorizationSession, error) {
	code = strings.TrimSpace(code)
	if ids.Validate(string(accountID)) != nil || stateSHA256 == [sha256.Size]byte{} || code == "" || len(code) > 100 || at.IsZero() {
		return domain.AuthorizationSession{}, integrationauthorization.ErrInvalid
	}
	var result domain.AuthorizationSession
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		var sessionID ids.IntegrationAuthorizationSessionID
		if err := tx.QueryRow(ctx, `SELECT id FROM spyglass.integration_authorization_sessions WHERE account_id=$1 AND state_sha256=$2 FOR UPDATE`,
			accountID, stateSHA256[:]).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
			return integrationauthorization.ErrNotFound
		} else if err != nil {
			return err
		}
		session, err := loadIntegrationAuthorizationSession(ctx, tx, accountID, sessionID, false)
		if err != nil {
			return err
		}
		if session.Status == domain.AuthorizationFailed {
			if session.ErrorCode != code {
				return integrationauthorization.ErrConflict
			}
			result = session
			return nil
		}
		if session.Status == domain.AuthorizationExpired {
			result = session
			return nil
		}
		if session.Status != domain.AuthorizationPending {
			return integrationauthorization.ErrConflict
		}
		claimed, err := session.BeginExchange(stateSHA256, session.Version, at)
		if err != nil {
			return err
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, claimed, session.Version); err != nil {
			return err
		}
		if claimed.Status == domain.AuthorizationExpired {
			eventID, _ := ids.Derive(string(session.ID), "integration-authorization-expired")
			if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, session.ID,
				integrationauthorization.Event{ID: eventID, Type: "authorization_expired", ActorKind: "provider_callback", ActorID: domain.GoogleOAuthProvider,
					CorrelationID: string(session.ID), At: at.UTC()}, map[string]any{"error_code": "authorization_expired"}); err != nil {
				return err
			}
			result = claimed
			return nil
		}
		failed, err := claimed.Fail(code, claimed.Version, at)
		if err != nil {
			return err
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, failed, claimed.Version); err != nil {
			return err
		}
		eventID, _ := ids.Derive(string(session.ID), "integration-authorization-provider-rejected")
		if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, session.ID,
			integrationauthorization.Event{ID: eventID, Type: "authorization_failed", ActorKind: "provider_callback", ActorID: domain.GoogleOAuthProvider,
				CorrelationID: string(session.ID), At: at.UTC()}, map[string]any{"error_code": code}); err != nil {
			return err
		}
		result = failed
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) ClaimCallback(ctx context.Context, accountID ids.AccountID, stateSHA256, codeSHA256 [sha256.Size]byte, at time.Time) (integrationauthorization.CallbackProgress, error) {
	if ids.Validate(string(accountID)) != nil || stateSHA256 == [sha256.Size]byte{} || codeSHA256 == [sha256.Size]byte{} || at.IsZero() {
		return integrationauthorization.CallbackProgress{}, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.CallbackProgress
	expired := false
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		var sessionID ids.IntegrationAuthorizationSessionID
		if err := tx.QueryRow(ctx, `SELECT id FROM spyglass.integration_authorization_sessions WHERE account_id=$1 AND state_sha256=$2 FOR UPDATE`, accountID, stateSHA256[:]).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
			return integrationauthorization.ErrNotFound
		} else if err != nil {
			return err
		}
		session, err := loadIntegrationAuthorizationSession(ctx, tx, accountID, sessionID, false)
		if err != nil {
			return err
		}
		if session.Status != domain.AuthorizationPending {
			workflow, err := loadIntegrationAuthorizationWorkflow(ctx, tx, accountID, sessionID, false)
			if err != nil {
				if session.Status == domain.AuthorizationExpired {
					expired = true
					result.Session = session
					return nil
				}
				return err
			}
			if workflow.CodeSHA256 != codeSHA256 {
				return integrationauthorization.ErrConflict
			}
			result = integrationauthorization.CallbackProgress{Session: session, Workflow: workflow}
			return nil
		}
		next, err := session.BeginExchange(stateSHA256, session.Version, at)
		if err != nil {
			return err
		}
		if next.Status == domain.AuthorizationExpired {
			if err := updateIntegrationAuthorizationSession(ctx, tx, next, session.Version); err != nil {
				return err
			}
			eventID, _ := ids.Derive(string(session.ID), "integration-authorization-expired")
			if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, session.ID,
				integrationauthorization.Event{ID: eventID, Type: "authorization_expired", ActorKind: "provider_callback", ActorID: domain.GoogleOAuthProvider, CorrelationID: string(session.ID), At: at},
				map[string]any{"error_code": "authorization_expired"}); err != nil {
				return err
			}
			expired, result.Session = true, next
			return nil
		}
		connection, err := loadIntegrationConnection(ctx, tx, accountID, session.ConnectionID, true)
		if err != nil {
			return err
		}
		revision, err := loadIntegrationRevision(ctx, tx, accountID, connection.CurrentRevisionID)
		if err != nil {
			return err
		}
		if connection.Kind != domain.ConnectorGoogleDrive || connection.State == domain.ConnectionRevoked || connection.CurrentRevisionID != session.ConnectionRevision ||
			integrationauthorization.ScopeRevisionDigest(revision) != session.ScopeRevisionSHA256 {
			return integrationauthorization.ErrConflict
		}
		targetID, err := integrationauthorization.TargetCredentialID(session.ID)
		if err != nil {
			return integrationauthorization.ErrInvalid
		}
		workflow := integrationauthorization.CredentialWorkflow{AccountID: accountID, SessionID: session.ID, State: integrationauthorization.WorkflowClaimed,
			ConnectionID: connection.ID, ConnectionVersion: connection.Version, TargetCredentialID: targetID,
			TargetGeneration: connection.CredentialGeneration + 1, PreviousCredentialID: connection.CredentialID,
			PreviousGeneration: connection.CredentialGeneration, CodeSHA256: codeSHA256, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
		workflow.ReferenceSHA256 = sha256.Sum256(integrationauthorization.CredentialReference(workflow))
		if connection.State == domain.ConnectionPending && (connection.CredentialID != "" || connection.CredentialGeneration != 0 || workflow.TargetGeneration != 1) {
			return integrationauthorization.ErrConflict
		}
		if connection.State != domain.ConnectionPending {
			previous, err := loadIntegrationCredential(ctx, tx, accountID, connection.CredentialID, true)
			if err != nil || previous.State != domain.CredentialActive || previous.Generation != connection.CredentialGeneration {
				if err != nil {
					return err
				}
				return integrationauthorization.ErrConflict
			}
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, next, session.Version); err != nil {
			return err
		}
		if err := insertIntegrationAuthorizationWorkflow(ctx, tx, workflow); err != nil {
			return err
		}
		eventID, _ := ids.Derive(string(session.ID), "integration-authorization-exchange-claimed")
		if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, session.ID,
			integrationauthorization.Event{ID: eventID, Type: "authorization_exchange_claimed", ActorKind: "provider_callback", ActorID: domain.GoogleOAuthProvider, CorrelationID: string(session.ID), At: at},
			map[string]any{"target_generation": workflow.TargetGeneration}); err != nil {
			return err
		}
		result = integrationauthorization.CallbackProgress{Session: next, Workflow: workflow}
		return nil
	})
	if err != nil {
		return integrationauthorization.CallbackProgress{}, classifyIntegrationAuthorization(err)
	}
	if expired {
		return result, integrationauthorization.ErrExpired
	}
	return result, nil
}

func (repository *IntegrationAuthorizationRepository) StartProviderExchange(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, at time.Time) (integrationauthorization.CallbackProgress, bool, error) {
	return repository.transitionAuthorizationWorkflow(ctx, accountID, sessionID, integrationauthorization.WorkflowClaimed,
		integrationauthorization.WorkflowProviderExchanging, at, true)
}

func (repository *IntegrationAuthorizationRepository) MarkCredentialStored(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, at time.Time) (integrationauthorization.CallbackProgress, error) {
	result, _, err := repository.transitionAuthorizationWorkflow(ctx, accountID, sessionID, integrationauthorization.WorkflowProviderExchanging,
		integrationauthorization.WorkflowCredentialStored, at, false)
	return result, err
}

func (repository *IntegrationAuthorizationRepository) MarkPreviousFenced(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, at time.Time) (integrationauthorization.CallbackProgress, error) {
	result, _, err := repository.transitionAuthorizationWorkflow(ctx, accountID, sessionID, integrationauthorization.WorkflowCredentialStored,
		integrationauthorization.WorkflowPreviousFenced, at, false)
	return result, err
}

func (repository *IntegrationAuthorizationRepository) transitionAuthorizationWorkflow(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID,
	from, to integrationauthorization.WorkflowState, at time.Time, exchangeStart bool) (integrationauthorization.CallbackProgress, bool, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil || at.IsZero() {
		return integrationauthorization.CallbackProgress{}, false, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.CallbackProgress
	changed := false
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		progress, err := loadIntegrationAuthorizationProgress(ctx, tx, accountID, sessionID, true)
		if err != nil {
			return err
		}
		if progress.Workflow.State == to || progress.Workflow.State != from {
			result = progress
			return nil
		}
		if exchangeStart {
			progress.Workflow.ExchangeStartedAt = timePointer(at)
		}
		progress.Workflow.State, progress.Workflow.UpdatedAt = to, at.UTC()
		command, err := tx.Exec(ctx, `UPDATE spyglass.integration_authorization_workflows SET state=$3,exchange_started_at=$4,updated_at=$5
			WHERE account_id=$1 AND session_id=$2 AND state=$6`, accountID, sessionID, to, progress.Workflow.ExchangeStartedAt, progress.Workflow.UpdatedAt, from)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return integrationauthorization.ErrConflict
		}
		result, changed = progress, true
		return nil
	})
	return result, changed, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) CompleteCallback(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, at time.Time) (integrationauthorization.CallbackProgress, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil || at.IsZero() {
		return integrationauthorization.CallbackProgress{}, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.CallbackProgress
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		progress, err := loadIntegrationAuthorizationProgress(ctx, tx, accountID, sessionID, true)
		if err != nil {
			return err
		}
		if progress.Workflow.State == integrationauthorization.WorkflowCompleted && progress.Session.Status == domain.AuthorizationCompleted {
			result = progress
			return nil
		}
		rotating := progress.Workflow.PreviousCredentialID != ""
		if progress.Session.Status != domain.AuthorizationExchanging ||
			(!rotating && progress.Workflow.State != integrationauthorization.WorkflowCredentialStored) ||
			(rotating && progress.Workflow.State != integrationauthorization.WorkflowPreviousFenced) {
			return integrationauthorization.ErrConflict
		}
		connection, err := loadIntegrationConnection(ctx, tx, accountID, progress.Workflow.ConnectionID, true)
		if err != nil {
			return err
		}
		if connection.Version != progress.Workflow.ConnectionVersion || connection.CurrentRevisionID != progress.Session.ConnectionRevision {
			return integrationauthorization.ErrConflict
		}
		actor := progress.Session.CreatedBy
		input := domain.CredentialInput{ID: progress.Workflow.TargetCredentialID, AccountID: accountID, ConnectionID: connection.ID,
			Generation: progress.Workflow.TargetGeneration, Provider: domain.GoogleOAuthProvider, ReferenceSHA256: progress.Workflow.ReferenceSHA256,
			CreatedBy: actor, CreatedAt: at.UTC()}
		credential, err := domain.NewCredentialBinding(input, accounts.RoleAdministrator)
		if err != nil {
			return err
		}
		var next domain.Connection
		if rotating {
			previous, err := loadIntegrationCredential(ctx, tx, accountID, progress.Workflow.PreviousCredentialID, true)
			if err != nil || previous.Generation != progress.Workflow.PreviousGeneration {
				if err != nil {
					return err
				}
				return integrationauthorization.ErrConflict
			}
			ended, replacement, err := previous.Rotate(input, previous.Generation, actor, accounts.RoleAdministrator, at)
			if err != nil {
				return err
			}
			credential = replacement
			next, err = connection.BindCredential(connection.Version, replacement, actor, accounts.RoleAdministrator, at)
			if err != nil {
				return err
			}
			updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_credentials SET state=$3,ended_by_user_id=$4,ended_at=$5,updated_at=$5
				WHERE account_id=$1 AND id=$2 AND state='active'`, accountID, ended.ID, ended.State, actor.UserID, at)
			if err != nil || updated.RowsAffected() != 1 {
				if err != nil {
					return err
				}
				return integrationauthorization.ErrConflict
			}
		} else {
			next, err = connection.Activate(connection.Version, credential, actor, accounts.RoleAdministrator, at)
			if err != nil {
				return err
			}
		}
		if err := insertIntegrationCredential(ctx, tx, credential); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('spyglass.integration_authorization_completion','on',true)`); err != nil {
			return err
		}
		if err := updateIntegrationConnectionCredential(ctx, tx, next, connection.Version); err != nil {
			return err
		}
		completed, err := progress.Session.Complete(credential.ID, credential.Generation, progress.Session.Version, at)
		if err != nil {
			return err
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, completed, progress.Session.Version); err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_authorization_workflows SET state='completed',updated_at=$3
			WHERE account_id=$1 AND session_id=$2 AND state=$4`, accountID, sessionID, at, progress.Workflow.State)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			return integrationauthorization.ErrConflict
		}
		eventID, _ := ids.Derive(string(sessionID), "integration-authorization-completed")
		if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, sessionID,
			integrationauthorization.Event{ID: eventID, Type: "authorization_completed", ActorKind: "workload", ActorID: "integration-authorization", CorrelationID: string(sessionID), At: at},
			map[string]any{"credential_id": credential.ID, "generation": credential.Generation}); err != nil {
			return err
		}
		kind := "connection_activated"
		subjectType, subjectID := "connection", string(connection.ID)
		if rotating {
			kind, subjectType, subjectID = "credential_rotated", "credential", string(credential.ID)
		}
		integrationEventID, _ := ids.Derive(string(sessionID), "integration-oauth-credential-bound")
		if err := insertIntegrationEvent(ctx, tx, accountID, integrationEventID, subjectType, subjectID, kind, actor,
			string(sessionID), map[string]any{"connection_id": connection.ID, "credential_generation": credential.Generation, "authorization_session_id": sessionID}, at); err != nil {
			return err
		}
		progress.Session, progress.Workflow.State, progress.Workflow.UpdatedAt = completed, integrationauthorization.WorkflowCompleted, at.UTC()
		result = progress
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) FailCallback(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, code string, at time.Time) (integrationauthorization.CallbackProgress, error) {
	code = strings.TrimSpace(code)
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil || code == "" || len(code) > 100 || at.IsZero() {
		return integrationauthorization.CallbackProgress{}, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.CallbackProgress
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		progress, err := loadIntegrationAuthorizationProgress(ctx, tx, accountID, sessionID, true)
		if err != nil {
			return err
		}
		if progress.Workflow.State == integrationauthorization.WorkflowFailed && progress.Session.Status == domain.AuthorizationFailed {
			if progress.Workflow.FailureCode != code || progress.Session.ErrorCode != code {
				return integrationauthorization.ErrConflict
			}
			result = progress
			return nil
		}
		if progress.Session.Status != domain.AuthorizationExchanging ||
			(progress.Workflow.State != integrationauthorization.WorkflowClaimed && progress.Workflow.State != integrationauthorization.WorkflowProviderExchanging) {
			return integrationauthorization.ErrConflict
		}
		failed, err := progress.Session.Fail(code, progress.Session.Version, at)
		if err != nil {
			return err
		}
		if err := updateIntegrationAuthorizationSession(ctx, tx, failed, progress.Session.Version); err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_authorization_workflows SET state='failed',failure_code=$3,updated_at=$4
			WHERE account_id=$1 AND session_id=$2 AND state=$5`, accountID, sessionID, code, at, progress.Workflow.State)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			return integrationauthorization.ErrConflict
		}
		eventID, _ := ids.Derive(string(sessionID), "integration-authorization-failed")
		if err := insertIntegrationAuthorizationEvent(ctx, tx, accountID, sessionID,
			integrationauthorization.Event{ID: eventID, Type: "authorization_failed", ActorKind: "workload", ActorID: "integration-authorization", CorrelationID: string(sessionID), At: at},
			map[string]any{"error_code": code}); err != nil {
			return err
		}
		progress.Session, progress.Workflow.State, progress.Workflow.FailureCode, progress.Workflow.UpdatedAt = failed, integrationauthorization.WorkflowFailed, code, at.UTC()
		result = progress
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) PrepareRevocation(ctx context.Context, accountID ids.AccountID, workflowID string,
	connectionID ids.IntegrationConnectionID, actor domain.Actor, at time.Time) (integrationauthorization.RevocationWorkflow, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(workflowID) != nil || ids.Validate(string(connectionID)) != nil ||
		ids.Validate(string(actor.UserID)) != nil || at.IsZero() {
		return integrationauthorization.RevocationWorkflow{}, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.RevocationWorkflow
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := loadIntegrationCredentialRevocation(ctx, tx, accountID, workflowID, true)
		if err == nil {
			if existing.ConnectionID != connectionID || existing.CreatedBy != actor {
				return integrationauthorization.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, integrationauthorization.ErrNotFound) {
			return err
		}
		connection, err := loadIntegrationConnection(ctx, tx, accountID, connectionID, true)
		if err != nil {
			return err
		}
		if (connection.State != domain.ConnectionActive && connection.State != domain.ConnectionDisabled) || connection.CredentialID == "" || connection.CredentialGeneration == 0 {
			return integrationauthorization.ErrConflict
		}
		var openAuthorization bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM spyglass.integration_authorization_workflows
			WHERE account_id=$1 AND connection_id=$2 AND state NOT IN ('completed','failed'))`, accountID, connectionID).Scan(&openAuthorization); err != nil {
			return err
		}
		if openAuthorization {
			return integrationauthorization.ErrConflict
		}
		credential, err := loadIntegrationCredential(ctx, tx, accountID, connection.CredentialID, true)
		if err != nil {
			return err
		}
		if credential.State != domain.CredentialActive || credential.Generation != connection.CredentialGeneration || credential.Provider != domain.GoogleOAuthProvider {
			return integrationauthorization.ErrConflict
		}
		workflow := integrationauthorization.RevocationWorkflow{AccountID: accountID, ID: workflowID, ConnectionID: connectionID,
			ConnectionVersion: connection.Version, CredentialID: credential.ID, CredentialGeneration: credential.Generation,
			Provider: credential.Provider, ReferenceSHA256: credential.ReferenceSHA256, State: integrationauthorization.RevocationPrepared,
			CreatedBy: actor, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.integration_credential_revocation_workflows
			(account_id,id,connection_id,connection_version,credential_id,credential_generation,provider,reference_sha256,state,
			 created_by_user_id,provider_started_at,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULL,$11,$11)`, workflow.AccountID, workflow.ID, workflow.ConnectionID,
			workflow.ConnectionVersion, workflow.CredentialID, workflow.CredentialGeneration, workflow.Provider, workflow.ReferenceSHA256[:],
			workflow.State, workflow.CreatedBy.UserID, workflow.CreatedAt)
		if err != nil {
			return err
		}
		result = workflow
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) StartProviderRevocation(ctx context.Context, accountID ids.AccountID, workflowID string, at time.Time) (integrationauthorization.RevocationWorkflow, bool, error) {
	return repository.transitionCredentialRevocation(ctx, accountID, workflowID, integrationauthorization.RevocationPrepared,
		integrationauthorization.RevocationProviderRevoking, at, true)
}

func (repository *IntegrationAuthorizationRepository) MarkProviderRevoked(ctx context.Context, accountID ids.AccountID, workflowID string, at time.Time) (integrationauthorization.RevocationWorkflow, error) {
	result, _, err := repository.transitionCredentialRevocation(ctx, accountID, workflowID, integrationauthorization.RevocationProviderRevoking,
		integrationauthorization.RevocationProviderRevoked, at, false)
	return result, err
}

func (repository *IntegrationAuthorizationRepository) MarkRevocationVaultFenced(ctx context.Context, accountID ids.AccountID, workflowID string, at time.Time) (integrationauthorization.RevocationWorkflow, error) {
	result, _, err := repository.transitionCredentialRevocation(ctx, accountID, workflowID, integrationauthorization.RevocationProviderRevoked,
		integrationauthorization.RevocationVaultFenced, at, false)
	return result, err
}

func (repository *IntegrationAuthorizationRepository) transitionCredentialRevocation(ctx context.Context, accountID ids.AccountID, workflowID string,
	from, to integrationauthorization.RevocationState, at time.Time, providerStart bool) (integrationauthorization.RevocationWorkflow, bool, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(workflowID) != nil || at.IsZero() {
		return integrationauthorization.RevocationWorkflow{}, false, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.RevocationWorkflow
	changed := false
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		workflow, err := loadIntegrationCredentialRevocation(ctx, tx, accountID, workflowID, true)
		if err != nil {
			return err
		}
		if workflow.State == to || workflow.State != from {
			result = workflow
			return nil
		}
		if providerStart {
			workflow.ProviderStartedAt = timePointer(at.UTC())
		}
		workflow.State, workflow.UpdatedAt = to, at.UTC()
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_credential_revocation_workflows
			SET state=$3,provider_started_at=$4,updated_at=$5 WHERE account_id=$1 AND id=$2 AND state=$6`,
			accountID, workflowID, to, workflow.ProviderStartedAt, workflow.UpdatedAt, from)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return integrationauthorization.ErrConflict
		}
		result, changed = workflow, true
		return nil
	})
	return result, changed, classifyIntegrationAuthorization(err)
}

func (repository *IntegrationAuthorizationRepository) CompleteRevocation(ctx context.Context, accountID ids.AccountID, workflowID string, at time.Time) (integrationauthorization.RevocationWorkflow, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(workflowID) != nil || at.IsZero() {
		return integrationauthorization.RevocationWorkflow{}, integrationauthorization.ErrInvalid
	}
	var result integrationauthorization.RevocationWorkflow
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		workflow, err := loadIntegrationCredentialRevocation(ctx, tx, accountID, workflowID, true)
		if err != nil {
			return err
		}
		if workflow.State == integrationauthorization.RevocationCompleted {
			result = workflow
			return nil
		}
		if workflow.State != integrationauthorization.RevocationVaultFenced {
			return integrationauthorization.ErrConflict
		}
		connection, err := loadIntegrationConnection(ctx, tx, accountID, workflow.ConnectionID, true)
		if err != nil {
			return err
		}
		credential, err := loadIntegrationCredential(ctx, tx, accountID, workflow.CredentialID, true)
		if err != nil {
			return err
		}
		if connection.Version != workflow.ConnectionVersion || connection.CredentialID != workflow.CredentialID ||
			connection.CredentialGeneration != workflow.CredentialGeneration || credential.State != domain.CredentialActive ||
			credential.Generation != workflow.CredentialGeneration {
			return integrationauthorization.ErrConflict
		}
		ended, err := credential.Revoke(credential.Generation, workflow.CreatedBy, accounts.RoleAdministrator, at)
		if err != nil {
			return err
		}
		next, err := connection.Revoke(connection.Version, workflow.CreatedBy, accounts.RoleAdministrator, at)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_credentials SET state=$3,ended_by_user_id=$4,ended_at=$5,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND state='active'`, accountID, ended.ID, ended.State, workflow.CreatedBy.UserID, at)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			return integrationauthorization.ErrConflict
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('spyglass.integration_revocation_completion','on',true)`); err != nil {
			return err
		}
		updated, err = tx.Exec(ctx, `UPDATE spyglass.integration_connections SET state=$3,version=$4,revoked_by_user_id=$5,revoked_at=$6,updated_at=$6
			WHERE account_id=$1 AND id=$2 AND version=$7`, accountID, next.ID, next.State, next.Version, workflow.CreatedBy.UserID, next.RevokedAt, connection.Version)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			return integrationauthorization.ErrConflict
		}
		updated, err = tx.Exec(ctx, `UPDATE spyglass.integration_credential_revocation_workflows SET state='completed',updated_at=$3
			WHERE account_id=$1 AND id=$2 AND state='vault_fenced'`, accountID, workflowID, at)
		if err != nil || updated.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			return integrationauthorization.ErrConflict
		}
		connectionEventID, _ := ids.Derive(workflow.ID, "integration-oauth-connection-revoked")
		if err := insertIntegrationEvent(ctx, tx, accountID, connectionEventID, "connection", string(connection.ID), "connection_revoked",
			workflow.CreatedBy, workflow.ID, map[string]any{"state": next.State, "credential_generation": credential.Generation}, at); err != nil {
			return err
		}
		credentialEventID, _ := ids.Derive(workflow.ID, "integration-oauth-credential-revoked")
		if err := insertIntegrationEvent(ctx, tx, accountID, credentialEventID, "credential", string(credential.ID), "credential_revoked",
			workflow.CreatedBy, workflow.ID, map[string]any{"connection_id": connection.ID, "generation": credential.Generation}, at); err != nil {
			return err
		}
		workflow.State, workflow.UpdatedAt = integrationauthorization.RevocationCompleted, at.UTC()
		result = workflow
		return nil
	})
	return result, classifyIntegrationAuthorization(err)
}

func insertIntegrationAuthorizationWorkflow(ctx context.Context, tx pgx.Tx, value integrationauthorization.CredentialWorkflow) error {
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.integration_authorization_workflows
		(account_id,session_id,connection_id,connection_version,state,target_credential_id,target_generation,reference_sha256,
		 previous_credential_id,previous_generation,code_sha256,exchange_started_at,failure_code,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULL,$13,$13)`, value.AccountID, value.SessionID, value.ConnectionID,
		value.ConnectionVersion, value.State, value.TargetCredentialID, value.TargetGeneration, value.ReferenceSHA256[:],
		nullableAuthorizationCredential(value.PreviousCredentialID), value.PreviousGeneration, value.CodeSHA256[:], value.ExchangeStartedAt, value.CreatedAt)
	return err
}

func loadIntegrationCredentialRevocation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, workflowID string, lock bool) (integrationauthorization.RevocationWorkflow, error) {
	query := `SELECT account_id,id::text,connection_id,connection_version,credential_id,credential_generation,provider,reference_sha256,state,
		created_by_user_id::text,provider_started_at,created_at,updated_at FROM spyglass.integration_credential_revocation_workflows
		WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value integrationauthorization.RevocationWorkflow
	var referenceDigest []byte
	err := tx.QueryRow(ctx, query, accountID, workflowID).Scan(&value.AccountID, &value.ID, &value.ConnectionID, &value.ConnectionVersion,
		&value.CredentialID, &value.CredentialGeneration, &value.Provider, &referenceDigest, &value.State, &value.CreatedBy.UserID,
		&value.ProviderStartedAt, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationauthorization.RevocationWorkflow{}, integrationauthorization.ErrNotFound
	}
	if err != nil {
		return integrationauthorization.RevocationWorkflow{}, err
	}
	if len(referenceDigest) != sha256.Size {
		return integrationauthorization.RevocationWorkflow{}, integrationauthorization.ErrRepository
	}
	copy(value.ReferenceSHA256[:], referenceDigest)
	if !validIntegrationCredentialRevocation(value) {
		return integrationauthorization.RevocationWorkflow{}, integrationauthorization.ErrRepository
	}
	return value, nil
}

func validIntegrationCredentialRevocation(value integrationauthorization.RevocationWorkflow) bool {
	if ids.Validate(string(value.AccountID)) != nil || ids.Validate(value.ID) != nil || ids.Validate(string(value.ConnectionID)) != nil ||
		ids.Validate(string(value.CredentialID)) != nil || ids.Validate(string(value.CreatedBy.UserID)) != nil || value.ConnectionVersion == 0 ||
		value.CredentialGeneration == 0 || value.Provider != domain.GoogleOAuthProvider || value.ReferenceSHA256 == [sha256.Size]byte{} ||
		value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return false
	}
	switch value.State {
	case integrationauthorization.RevocationPrepared:
		return value.ProviderStartedAt == nil
	case integrationauthorization.RevocationProviderRevoking, integrationauthorization.RevocationProviderRevoked,
		integrationauthorization.RevocationVaultFenced, integrationauthorization.RevocationCompleted:
		return value.ProviderStartedAt != nil
	default:
		return false
	}
}

func loadIntegrationAuthorizationProgress(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, lock bool) (integrationauthorization.CallbackProgress, error) {
	session, err := loadIntegrationAuthorizationSession(ctx, tx, accountID, sessionID, lock)
	if err != nil {
		return integrationauthorization.CallbackProgress{}, err
	}
	workflow, err := loadIntegrationAuthorizationWorkflow(ctx, tx, accountID, sessionID, lock)
	if err != nil {
		return integrationauthorization.CallbackProgress{}, err
	}
	return integrationauthorization.CallbackProgress{Session: session, Workflow: workflow}, nil
}

func loadIntegrationAuthorizationWorkflow(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, lock bool) (integrationauthorization.CredentialWorkflow, error) {
	query := `SELECT account_id,session_id,connection_id,connection_version,state,target_credential_id,target_generation,reference_sha256,
		previous_credential_id::text,previous_generation,code_sha256,exchange_started_at,COALESCE(failure_code,''),created_at,updated_at
		FROM spyglass.integration_authorization_workflows WHERE account_id=$1 AND session_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value integrationauthorization.CredentialWorkflow
	var referenceDigest, codeDigest []byte
	var previousID *string
	err := tx.QueryRow(ctx, query, accountID, sessionID).Scan(&value.AccountID, &value.SessionID, &value.ConnectionID, &value.ConnectionVersion,
		&value.State, &value.TargetCredentialID, &value.TargetGeneration, &referenceDigest, &previousID, &value.PreviousGeneration,
		&codeDigest, &value.ExchangeStartedAt, &value.FailureCode, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationauthorization.CredentialWorkflow{}, integrationauthorization.ErrNotFound
	}
	if err != nil {
		return integrationauthorization.CredentialWorkflow{}, err
	}
	if len(referenceDigest) != sha256.Size || len(codeDigest) != sha256.Size {
		return integrationauthorization.CredentialWorkflow{}, integrationauthorization.ErrRepository
	}
	copy(value.ReferenceSHA256[:], referenceDigest)
	copy(value.CodeSHA256[:], codeDigest)
	if previousID != nil {
		value.PreviousCredentialID = ids.IntegrationCredentialID(*previousID)
	}
	if !validIntegrationAuthorizationWorkflow(value) {
		return integrationauthorization.CredentialWorkflow{}, integrationauthorization.ErrRepository
	}
	return value, nil
}

func validIntegrationAuthorizationWorkflow(value integrationauthorization.CredentialWorkflow) bool {
	if ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.SessionID)) != nil || ids.Validate(string(value.ConnectionID)) != nil ||
		ids.Validate(string(value.TargetCredentialID)) != nil || value.ConnectionVersion == 0 || value.TargetGeneration == 0 ||
		value.ReferenceSHA256 == [sha256.Size]byte{} || value.CodeSHA256 == [sha256.Size]byte{} || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) ||
		(value.PreviousCredentialID == "") != (value.PreviousGeneration == 0) {
		return false
	}
	if value.PreviousCredentialID != "" && ids.Validate(string(value.PreviousCredentialID)) != nil {
		return false
	}
	switch value.State {
	case integrationauthorization.WorkflowClaimed:
		return value.ExchangeStartedAt == nil && value.FailureCode == ""
	case integrationauthorization.WorkflowProviderExchanging, integrationauthorization.WorkflowCredentialStored,
		integrationauthorization.WorkflowPreviousFenced, integrationauthorization.WorkflowCompleted:
		return value.ExchangeStartedAt != nil && value.FailureCode == ""
	case integrationauthorization.WorkflowFailed:
		return value.FailureCode != ""
	default:
		return false
	}
}

func updateIntegrationAuthorizationSession(ctx context.Context, tx pgx.Tx, value domain.AuthorizationSession, expectedVersion uint64) error {
	updated, err := tx.Exec(ctx, `UPDATE spyglass.integration_authorization_sessions SET status=$3,version=$4,credential_id=$5,
		credential_generation=$6,error_code=$7,updated_at=$8,claimed_at=$9,completed_at=$10 WHERE account_id=$1 AND id=$2 AND version=$11`,
		value.AccountID, value.ID, value.Status, value.Version, nullableAuthorizationCredential(value.CredentialID), value.CredentialGeneration,
		nullableAuthorizationCode(value.ErrorCode), value.UpdatedAt, value.ClaimedAt, value.CompletedAt, expectedVersion)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return integrationauthorization.ErrConflict
	}
	return nil
}

func nullableAuthorizationCredential(value ids.IntegrationCredentialID) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAuthorizationCode(value string) any {
	if value == "" {
		return nil
	}
	return value
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
