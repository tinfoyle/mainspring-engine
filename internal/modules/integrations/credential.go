package integrations

import (
	"crypto/sha256"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type CredentialState string

const (
	CredentialActive  CredentialState = "active"
	CredentialRotated CredentialState = "rotated"
	CredentialRevoked CredentialState = "revoked"
)

// CredentialBinding contains an attestation digest for an opaque secret-broker
// reference, never the reference or provider credential itself.
type CredentialBinding struct {
	ID              ids.IntegrationCredentialID `json:"id"`
	AccountID       ids.AccountID               `json:"account_id"`
	ConnectionID    ids.IntegrationConnectionID `json:"connection_id"`
	Generation      uint64                      `json:"generation"`
	Provider        string                      `json:"provider"`
	ReferenceSHA256 [sha256.Size]byte           `json:"reference_sha256"`
	State           CredentialState             `json:"state"`
	CreatedBy       Actor                       `json:"created_by"`
	EndedBy         *Actor                      `json:"ended_by,omitempty"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
	ExpiresAt       *time.Time                  `json:"expires_at,omitempty"`
	EndedAt         *time.Time                  `json:"ended_at,omitempty"`
}

type CredentialInput struct {
	ID              ids.IntegrationCredentialID
	AccountID       ids.AccountID
	ConnectionID    ids.IntegrationConnectionID
	Generation      uint64
	Provider        string
	ReferenceSHA256 [sha256.Size]byte
	CreatedBy       Actor
	CreatedAt       time.Time
	ExpiresAt       *time.Time
}

func NewCredentialBinding(input CredentialInput, role accounts.MembershipRole) (CredentialBinding, error) {
	if !canManage(role) || !input.CreatedBy.valid() {
		return CredentialBinding{}, ErrRole
	}
	value := CredentialBinding{ID: input.ID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Generation: input.Generation,
		Provider: strings.TrimSpace(input.Provider), ReferenceSHA256: input.ReferenceSHA256, State: CredentialActive, CreatedBy: input.CreatedBy,
		CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC(), ExpiresAt: input.ExpiresAt}
	return RestoreCredentialBinding(value)
}

func RestoreCredentialBinding(value CredentialBinding) (CredentialBinding, error) {
	value.Provider, value.CreatedAt, value.UpdatedAt = strings.TrimSpace(value.Provider), value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	if value.ExpiresAt != nil {
		at := value.ExpiresAt.UTC()
		value.ExpiresAt = &at
	}
	if value.EndedAt != nil {
		at := value.EndedAt.UTC()
		value.EndedAt = &at
	}
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.ConnectionID)) != nil || value.Generation == 0 ||
		!validText(value.Provider, MaximumProviderCodeBytes, true) || !validCodeValue(value.Provider) || !nonzeroDigest(value.ReferenceSHA256) || !value.CreatedBy.valid() || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) ||
		(value.ExpiresAt != nil && !value.ExpiresAt.After(value.CreatedAt)) {
		return CredentialBinding{}, ErrInvalid
	}
	switch value.State {
	case CredentialActive:
		if value.EndedBy != nil || value.EndedAt != nil || !value.UpdatedAt.Equal(value.CreatedAt) {
			return CredentialBinding{}, ErrInvalid
		}
	case CredentialRotated, CredentialRevoked:
		if value.EndedBy == nil || !value.EndedBy.valid() || value.EndedAt == nil || value.EndedAt.Before(value.CreatedAt) || !value.UpdatedAt.Equal(*value.EndedAt) {
			return CredentialBinding{}, ErrInvalid
		}
	default:
		return CredentialBinding{}, ErrInvalid
	}
	return value, nil
}

func (value CredentialBinding) Available(at time.Time) bool {
	return value.State == CredentialActive && !at.IsZero() && (value.ExpiresAt == nil || value.ExpiresAt.After(at.UTC()))
}

func (value CredentialBinding) Rotate(next CredentialInput, expectedGeneration uint64, actor Actor, role accounts.MembershipRole, at time.Time) (CredentialBinding, CredentialBinding, error) {
	if value.Generation != expectedGeneration {
		return CredentialBinding{}, CredentialBinding{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return CredentialBinding{}, CredentialBinding{}, ErrRole
	}
	if value.State != CredentialActive || next.AccountID != value.AccountID || next.ConnectionID != value.ConnectionID || next.Generation != value.Generation+1 || next.CreatedBy != actor || !validTime(at, value.UpdatedAt) {
		return CredentialBinding{}, CredentialBinding{}, ErrCredential
	}
	next.CreatedAt = at.UTC()
	replacement, err := NewCredentialBinding(next, role)
	if err != nil {
		return CredentialBinding{}, CredentialBinding{}, err
	}
	at = at.UTC()
	value.State, value.EndedBy, value.EndedAt, value.UpdatedAt = CredentialRotated, &actor, &at, at
	value, err = RestoreCredentialBinding(value)
	return value, replacement, err
}

func (value CredentialBinding) Revoke(expectedGeneration uint64, actor Actor, role accounts.MembershipRole, at time.Time) (CredentialBinding, error) {
	if value.Generation != expectedGeneration {
		return CredentialBinding{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return CredentialBinding{}, ErrRole
	}
	if value.State != CredentialActive || !validTime(at, value.UpdatedAt) {
		return CredentialBinding{}, ErrState
	}
	at = at.UTC()
	value.State, value.EndedBy, value.EndedAt, value.UpdatedAt = CredentialRevoked, &actor, &at, at
	return RestoreCredentialBinding(value)
}
