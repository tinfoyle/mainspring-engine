// Package integrations defines the authorized customer boundary for scoped
// external connectors. Provider secrets and provider payloads never cross it.
package integrations

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid    = errors.New("integration command is invalid")
	ErrNotFound   = errors.New("integration record was not found")
	ErrConflict   = errors.New("integration command conflicts with durable state")
	ErrRepository = errors.New("integration repository unavailable")
)

type Mutation struct {
	EventID       string
	Kind          string
	Actor         domain.Actor
	CorrelationID string
	At            time.Time
}

func (value Mutation) Valid() bool {
	validKind := map[string]bool{
		"connection_created":   true,
		"connection_revised":   true,
		"connection_activated": true,
		"connection_disabled":  true,
		"connection_revoked":   true,
		"credential_rotated":   true,
	}[value.Kind]
	return ids.Validate(value.EventID) == nil && ids.Validate(value.CorrelationID) == nil &&
		ids.Validate(string(value.Actor.UserID)) == nil && validKind && !value.At.IsZero()
}

type Store interface {
	CreateConnection(context.Context, domain.ConnectionInput, accounts.MembershipRole, Mutation) (domain.Connection, bool, error)
	GetConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID) (domain.Connection, error)
	GetConnectionDetail(context.Context, ids.AccountID, ids.IntegrationConnectionID) (ConnectionDetail, error)
	ListConnections(context.Context, ids.AccountID, ConnectionListQuery) (ConnectionPage, error)
	ListHealth(context.Context, ids.AccountID, HealthListQuery) (HealthPage, error)
	GetExecution(context.Context, ids.AccountID, ids.IntegrationExecutionID) (ExecutionDetail, error)
	ListExecutions(context.Context, ids.AccountID, ExecutionListQuery) (ExecutionPage, error)
	ReviseConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, domain.ConnectionRevisionInput, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
	ActivateConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, domain.CredentialInput, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
	RotateCredential(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, uint64, domain.CredentialInput, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
	DisableConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
	EnableConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
	RevokeConnection(context.Context, ids.AccountID, ids.IntegrationConnectionID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Connection, error)
}

func classify(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return err
	}
	if errors.Is(err, domain.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrState) || errors.Is(err, domain.ErrRole) ||
		errors.Is(err, domain.ErrCapability) || errors.Is(err, domain.ErrCredential) || errors.Is(err, domain.ErrUncertain) {
		return errors.Join(ErrInvalid, err)
	}
	return errors.Join(ErrRepository, err)
}

// ClassifyForAdapter preserves domain causes while keeping infrastructure
// detail behind the application-owned error vocabulary.
func ClassifyForAdapter(err error) error { return classify(err) }
