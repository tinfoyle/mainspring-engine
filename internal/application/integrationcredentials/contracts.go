// Package integrationcredentials defines the narrow secret-broker boundary
// shared by connector execution and provider health probes.
package integrationcredentials

import (
	"context"
	"crypto/sha256"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Purpose string

const (
	PurposeExecute   Purpose = "execute"
	PurposeReconcile Purpose = "reconcile"
	PurposeHealth    Purpose = "health"
)

type Request struct {
	AccountID            ids.AccountID
	OperationID          string
	Purpose              Purpose
	Capability           domain.Capability
	ConnectionID         ids.IntegrationConnectionID
	CredentialID         ids.IntegrationCredentialID
	CredentialGeneration uint64
	CredentialProvider   string
	ReferenceSHA256      [sha256.Size]byte
	ExpiresAt            time.Time
}

type Lease interface {
	Material() []byte
	Close() error
}

type Broker interface {
	Acquire(context.Context, Request) (Lease, error)
}
