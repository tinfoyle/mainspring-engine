package mockconnector

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Credential struct {
	ID         ids.IntegrationCredentialID
	Generation uint64
	Material   []byte
}

type Acquisition struct {
	ExecutionID  ids.IntegrationExecutionID
	AttemptID    ids.IntegrationAttemptID
	CredentialID ids.IntegrationCredentialID
	Generation   uint64
}

type Broker struct {
	mu           sync.Mutex
	credentials  map[string][]byte
	acquisitions []Acquisition
}

func NewBroker(credentials []Credential) (*Broker, error) {
	if len(credentials) == 0 {
		return nil, errors.New("mock connector credentials are required")
	}
	values := make(map[string][]byte, len(credentials))
	for _, credential := range credentials {
		key := credentialKey(credential.ID, credential.Generation)
		if ids.Validate(string(credential.ID)) != nil || credential.Generation == 0 || len(credential.Material) == 0 || len(credential.Material) > 64<<10 || values[key] != nil {
			return nil, errors.New("mock connector credential is invalid")
		}
		values[key] = append([]byte(nil), credential.Material...)
	}
	return &Broker{credentials: values}, nil
}

func (broker *Broker) Acquire(_ context.Context, request integrationexecution.CredentialRequest) (integrationexecution.CredentialLease, error) {
	if broker == nil || ids.Validate(string(request.ExecutionID)) != nil || ids.Validate(string(request.AttemptID)) != nil || request.ExpiresAt.IsZero() {
		return nil, errors.New("mock credential request is invalid")
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	material, exists := broker.credentials[credentialKey(request.CredentialID, request.CredentialGeneration)]
	if !exists {
		return nil, errors.New("mock connector credential is unavailable")
	}
	broker.acquisitions = append(broker.acquisitions, Acquisition{ExecutionID: request.ExecutionID, AttemptID: request.AttemptID,
		CredentialID: request.CredentialID, Generation: request.CredentialGeneration})
	return &credentialLease{material: append([]byte(nil), material...)}, nil
}

func (broker *Broker) Acquisitions() []Acquisition {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return append([]Acquisition(nil), broker.acquisitions...)
}

type credentialLease struct {
	mu       sync.Mutex
	material []byte
	closed   bool
}

func (lease *credentialLease) Material() []byte {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	return lease.material
}

func (lease *credentialLease) Close() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	for index := range lease.material {
		lease.material[index] = 0
	}
	lease.material, lease.closed = nil, true
	return nil
}

func credentialKey(id ids.IntegrationCredentialID, generation uint64) string {
	return string(id) + "/" + strconv.FormatUint(generation, 10)
}

var _ integrationexecution.CredentialBroker = (*Broker)(nil)
