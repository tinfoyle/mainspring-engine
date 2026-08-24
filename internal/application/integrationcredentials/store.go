package integrationcredentials

import (
	"context"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AuthorizationSecret struct {
	AccountID ids.AccountID
	SessionID ids.IntegrationAuthorizationSessionID
	Verifier  []byte
	ExpiresAt time.Time
}

type CredentialSecret struct {
	AccountID    ids.AccountID
	CredentialID ids.IntegrationCredentialID
	Generation   uint64
	Provider     string
	Reference    []byte
	Material     []byte
}

type CredentialEndState string

const (
	CredentialRotated CredentialEndState = "rotated"
	CredentialRevoked CredentialEndState = "revoked"
)

// Store owns provider-secret persistence. Implementations must make creation
// replay-safe, fence ended credentials before returning, and never place raw
// secret material in errors or observable metadata.
type Store interface {
	PutAuthorization(context.Context, AuthorizationSecret) error
	Authorization(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (Lease, error)
	DeleteAuthorization(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID) error
	PutCredential(context.Context, CredentialSecret) error
	FenceCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64, CredentialEndState) error
	PurgeCredential(context.Context, ids.AccountID, ids.IntegrationCredentialID, uint64) error
}
