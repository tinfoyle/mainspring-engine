package integrations

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const PackageCode = catalog.PackageIntegrations

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	store      Store
	clock      Clock
}

func New(authorizer Authorizer, store Store, clock Clock) (*Service, error) {
	if authorizer == nil || store == nil || clock == nil {
		return nil, errors.New("Integrations dependencies are required")
	}
	return &Service{authorizer: authorizer, store: store, clock: clock}, nil
}

type CreateConnectionCommand struct {
	Actor        access.Actor
	AccountID    ids.AccountID
	RequestID    string
	Name         string
	Kind         domain.ConnectorKind
	Capabilities []domain.Capability
	Scope        domain.ConnectionScope
}

func (service *Service) CreateConnection(ctx context.Context, command CreateConnectionCommand) (domain.Connection, bool, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, 0, true)
	if err != nil {
		return domain.Connection{}, false, err
	}
	revisionID, err := ids.Derive(command.RequestID, "integration-connection-revision-1")
	if err != nil {
		return domain.Connection{}, false, ErrInvalid
	}
	input := domain.ConnectionInput{ID: ids.IntegrationConnectionID(command.RequestID), RevisionID: ids.IntegrationConnectionRevisionID(revisionID),
		AccountID: command.AccountID, Name: command.Name, Kind: command.Kind, Capabilities: command.Capabilities, Scope: command.Scope,
		CreatedBy: actor, CreatedAt: now}
	return service.store.CreateConnection(ctx, input, authorized.Role, mutation(command.RequestID, "connection_created", actor, now))
}

func (service *Service) GetConnection(ctx context.Context, actor access.Actor, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (domain.Connection, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return domain.Connection{}, err
	}
	if ids.Validate(string(connectionID)) != nil {
		return domain.Connection{}, ErrInvalid
	}
	return service.store.GetConnection(ctx, accountID, connectionID)
}

func (service *Service) GetConnectionDetail(ctx context.Context, actor access.Actor, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) (ConnectionDetail, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return ConnectionDetail{}, err
	}
	if ids.Validate(string(connectionID)) != nil {
		return ConnectionDetail{}, ErrInvalid
	}
	return service.store.GetConnectionDetail(ctx, accountID, connectionID)
}

func (service *Service) ListConnections(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ConnectionListQuery) (ConnectionPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return ConnectionPage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return ConnectionPage{}, err
	}
	return service.store.ListConnections(ctx, accountID, query)
}

func (service *Service) ListHealth(ctx context.Context, actor access.Actor, accountID ids.AccountID, query HealthListQuery) (HealthPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return HealthPage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return HealthPage{}, err
	}
	return service.store.ListHealth(ctx, accountID, query)
}

func (service *Service) GetExecution(ctx context.Context, actor access.Actor, accountID ids.AccountID, executionID ids.IntegrationExecutionID) (ExecutionDetail, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return ExecutionDetail{}, err
	}
	if ids.Validate(string(executionID)) != nil {
		return ExecutionDetail{}, ErrInvalid
	}
	return service.store.GetExecution(ctx, accountID, executionID)
}

func (service *Service) ListExecutions(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ExecutionListQuery) (ExecutionPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false); err != nil {
		return ExecutionPage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return ExecutionPage{}, err
	}
	return service.store.ListExecutions(ctx, accountID, query)
}

type ReviseConnectionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	ConnectionID    ids.IntegrationConnectionID
	ExpectedVersion uint64
	Name            string
	Capabilities    []domain.Capability
	Scope           domain.ConnectionScope
}

func (service *Service) ReviseConnection(ctx context.Context, command ReviseConnectionCommand) (domain.Connection, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, false)
	if err != nil || ids.Validate(string(command.ConnectionID)) != nil {
		if err != nil {
			return domain.Connection{}, err
		}
		return domain.Connection{}, ErrInvalid
	}
	input := domain.ConnectionRevisionInput{ID: ids.IntegrationConnectionRevisionID(command.RequestID), Name: command.Name,
		Capabilities: command.Capabilities, Scope: command.Scope}
	return service.store.ReviseConnection(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, input, actor, authorized.Role,
		mutation(command.RequestID, "connection_revised", actor, now))
}

type CredentialCommand struct {
	Actor               access.Actor
	AccountID           ids.AccountID
	RequestID           string
	ConnectionID        ids.IntegrationConnectionID
	ExpectedVersion     uint64
	ExpectedGeneration  uint64
	Provider            string
	ReferenceSHA256     [sha256.Size]byte
	CredentialExpiresAt *time.Time
}

func (service *Service) ActivateConnection(ctx context.Context, command CredentialCommand) (domain.Connection, error) {
	return service.credentialCommand(ctx, command, false)
}

func (service *Service) RotateCredential(ctx context.Context, command CredentialCommand) (domain.Connection, error) {
	return service.credentialCommand(ctx, command, true)
}

func (service *Service) credentialCommand(ctx context.Context, command CredentialCommand, rotate bool) (domain.Connection, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, false)
	if err != nil || ids.Validate(string(command.ConnectionID)) != nil || (rotate && command.ExpectedGeneration == 0) || (!rotate && command.ExpectedGeneration != 0) {
		if err != nil {
			return domain.Connection{}, err
		}
		return domain.Connection{}, ErrInvalid
	}
	credentialID, err := ids.Derive(command.RequestID, "integration-credential")
	if err != nil {
		return domain.Connection{}, ErrInvalid
	}
	generation := uint64(1)
	if rotate {
		generation = command.ExpectedGeneration + 1
	}
	input := domain.CredentialInput{ID: ids.IntegrationCredentialID(credentialID), AccountID: command.AccountID, ConnectionID: command.ConnectionID,
		Generation: generation, Provider: command.Provider, ReferenceSHA256: command.ReferenceSHA256, CreatedBy: actor, CreatedAt: now,
		ExpiresAt: command.CredentialExpiresAt}
	if rotate {
		return service.store.RotateCredential(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, command.ExpectedGeneration, input, actor,
			authorized.Role, mutation(command.RequestID, "credential_rotated", actor, now))
	}
	return service.store.ActivateConnection(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, input, actor, authorized.Role,
		mutation(command.RequestID, "connection_activated", actor, now))
}

type TransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	ConnectionID    ids.IntegrationConnectionID
	ExpectedVersion uint64
}

func (service *Service) DisableConnection(ctx context.Context, command TransitionCommand) (domain.Connection, error) {
	return service.transition(ctx, command, "connection_disabled")
}

func (service *Service) EnableConnection(ctx context.Context, command TransitionCommand) (domain.Connection, error) {
	return service.transition(ctx, command, "connection_activated")
}

func (service *Service) RevokeConnection(ctx context.Context, command TransitionCommand) (domain.Connection, error) {
	return service.transition(ctx, command, "connection_revoked")
}

func (service *Service) transition(ctx context.Context, command TransitionCommand, kind string) (domain.Connection, error) {
	authorized, actor, now, err := service.managementCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, false)
	if err != nil || ids.Validate(string(command.ConnectionID)) != nil {
		if err != nil {
			return domain.Connection{}, err
		}
		return domain.Connection{}, ErrInvalid
	}
	change := mutation(command.RequestID, kind, actor, now)
	switch kind {
	case "connection_disabled":
		return service.store.DisableConnection(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, actor, authorized.Role, change)
	case "connection_activated":
		return service.store.EnableConnection(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, actor, authorized.Role, change)
	default:
		return service.store.RevokeConnection(ctx, command.AccountID, command.ConnectionID, command.ExpectedVersion, actor, authorized.Role, change)
	}
}

func (service *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation bool) (access.AccountContext, error) {
	if !actor.Valid() || actor.UserID == "" || ids.Validate(string(actor.UserID)) != nil || ids.Validate(string(accountID)) != nil {
		return access.AccountContext{}, ErrInvalid
	}
	requirement := access.Requirement{Package: PackageCode, Mutation: mutation}
	if mutation {
		requirement.Roles = []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}
	}
	return service.authorizer.Authorize(ctx, actor, accountID, requirement)
}

func (service *Service) managementCommand(ctx context.Context, actor access.Actor, accountID ids.AccountID, requestID string, expectedVersion uint64, allowZeroVersion bool) (access.AccountContext, domain.Actor, time.Time, error) {
	authorized, err := service.authorize(ctx, actor, accountID, true)
	if err != nil {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, err
	}
	if ids.Validate(requestID) != nil || (!allowZeroVersion && expectedVersion == 0) {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	return authorized, domain.Actor{UserID: actor.UserID}, now, nil
}

func mutation(requestID, kind string, actor domain.Actor, at time.Time) Mutation {
	return Mutation{EventID: requestID, Kind: kind, Actor: actor, CorrelationID: requestID, At: at}
}
