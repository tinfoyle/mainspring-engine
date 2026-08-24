// Package integrationhealth runs non-mutating provider checks in the dedicated
// connector runtime and appends content-free health observations.
package integrationhealth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const MaximumLease = 5 * time.Minute

var (
	ErrInvalid     = errors.New("integration health claim is invalid")
	ErrUnavailable = errors.New("integration health probe is unavailable")
	validCode      = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

type Claim struct {
	AccountID                 ids.AccountID
	ProbeID                   ids.IntegrationHealthObservationID
	ConnectionID              ids.IntegrationConnectionID
	ConnectionRevisionID      ids.IntegrationConnectionRevisionID
	ConnectionRevision        uint64
	ConnectorKind             domain.ConnectorKind
	Capabilities              []domain.Capability
	Scope                     domain.ConnectionScope
	CredentialID              ids.IntegrationCredentialID
	CredentialGeneration      uint64
	CredentialProvider        string
	CredentialReferenceSHA256 [sha256.Size]byte
	LeaseExpiresAt            time.Time
}

func (claim Claim) Valid(now time.Time) bool {
	if ids.Validate(string(claim.AccountID)) != nil || ids.Validate(string(claim.ProbeID)) != nil || ids.Validate(string(claim.ConnectionID)) != nil ||
		ids.Validate(string(claim.ConnectionRevisionID)) != nil || ids.Validate(string(claim.CredentialID)) != nil || claim.ConnectionRevision == 0 ||
		claim.CredentialGeneration == 0 || !validCode.MatchString(claim.CredentialProvider) || claim.CredentialReferenceSHA256 == [sha256.Size]byte{} ||
		!claim.LeaseExpiresAt.After(now.UTC()) || len(claim.Capabilities) == 0 {
		return false
	}
	_, err := domain.RestoreConnectionRevision(domain.ConnectionRevision{ID: claim.ConnectionRevisionID, AccountID: claim.AccountID,
		ConnectionID: claim.ConnectionID, Revision: claim.ConnectionRevision, Capabilities: claim.Capabilities, Scope: claim.Scope,
		CreatedBy: domain.Actor{UserID: ids.UserID(claim.ConnectionID)}, CreatedAt: now.UTC()}, claim.ConnectorKind)
	return err == nil
}

type Completion struct {
	Claim               Claim
	State               domain.HealthState
	ErrorCode           string
	LatencyMilliseconds uint32
	CheckedAt           time.Time
}

type Repository interface {
	Claim(context.Context, ids.IntegrationHealthObservationID, time.Time, time.Time) (Claim, bool, error)
	Complete(context.Context, Completion) error
}

type Authority interface {
	AuthorizeAccount(context.Context, ids.AccountID) error
}

type ProbeCall struct {
	Claim      Claim
	Credential []byte
}

type ProbeResult struct {
	State     domain.HealthState
	ErrorCode string
}

type Probe interface {
	Probe(context.Context, ProbeCall) ProbeResult
}

type Definition struct {
	Kind               domain.ConnectorKind
	CredentialProvider string
	Capability         domain.Capability
	Timeout            time.Duration
	Probe              Probe
}

type definitionKey struct {
	kind     domain.ConnectorKind
	provider string
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository  Repository
	authority   Authority
	broker      integrationcredentials.Broker
	ids         ids.Generator
	clock       Clock
	lease       time.Duration
	definitions map[definitionKey]Definition
}

func New(repository Repository, authority Authority, broker integrationcredentials.Broker, generator ids.Generator, clock Clock,
	lease time.Duration, definitions []Definition) (*Service, error) {
	if repository == nil || authority == nil || broker == nil || generator == nil || clock == nil || lease < time.Second || lease > MaximumLease || len(definitions) == 0 {
		return nil, ErrInvalid
	}
	registered := make(map[definitionKey]Definition, len(definitions))
	for _, definition := range definitions {
		if (definition.Kind != domain.ConnectorEmail && definition.Kind != domain.ConnectorWebPublish && definition.Kind != domain.ConnectorGoogleDrive) || definition.Probe == nil ||
			definition.Timeout < 100*time.Millisecond || definition.Timeout > MaximumLease ||
			(definition.CredentialProvider != "" && !validCode.MatchString(definition.CredentialProvider)) {
			return nil, ErrInvalid
		}
		if definition.Capability == "" {
			definition.Capability = defaultCapability(definition.Kind)
		}
		if !capabilityMatchesKind(definition.Kind, definition.Capability) {
			return nil, ErrInvalid
		}
		key := definitionKey{kind: definition.Kind, provider: definition.CredentialProvider}
		if _, exists := registered[key]; exists {
			return nil, ErrInvalid
		}
		registered[key] = definition
	}
	return &Service{repository: repository, authority: authority, broker: broker, ids: generator, clock: clock, lease: lease, definitions: registered}, nil
}

func (service *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := service.clock.Now().UTC()
	probeID := ids.IntegrationHealthObservationID(service.ids.New())
	if ids.Validate(string(probeID)) != nil {
		return false, ErrInvalid
	}
	claim, found, err := service.repository.Claim(ctx, probeID, now, now.Add(service.lease))
	if err != nil || !found {
		return false, err
	}
	if claim.ProbeID != probeID || !claim.Valid(now) {
		return true, ErrInvalid
	}
	definition, exists := service.definitions[definitionKey{kind: claim.ConnectorKind, provider: claim.CredentialProvider}]
	if !exists {
		definition, exists = service.definitions[definitionKey{kind: claim.ConnectorKind}]
	}
	if !exists {
		return true, errors.Join(service.complete(ctx, claim, ProbeResult{State: domain.HealthUnavailable, ErrorCode: "health_probe_unavailable"}, now), ErrUnavailable)
	}
	if err := service.authority.AuthorizeAccount(ctx, claim.AccountID); err != nil {
		return true, errors.Join(service.complete(ctx, claim, ProbeResult{State: domain.HealthUnavailable, ErrorCode: "health_authority_unavailable"}, now), err)
	}
	credentialLease, err := service.broker.Acquire(ctx, integrationcredentials.Request{AccountID: claim.AccountID, OperationID: string(claim.ProbeID),
		Purpose: integrationcredentials.PurposeHealth, Capability: definition.Capability, ConnectionID: claim.ConnectionID, CredentialID: claim.CredentialID,
		CredentialGeneration: claim.CredentialGeneration, CredentialProvider: claim.CredentialProvider,
		ReferenceSHA256: claim.CredentialReferenceSHA256, ExpiresAt: claim.LeaseExpiresAt})
	if err != nil || credentialLease == nil {
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(service.complete(ctx, claim, ProbeResult{State: domain.HealthUnavailable, ErrorCode: "credential_unavailable"}, now), err)
	}
	material := credentialLease.Material()
	if len(material) == 0 || len(material) > 64<<10 {
		closeErr := credentialLease.Close()
		return true, errors.Join(service.complete(ctx, claim, ProbeResult{State: domain.HealthUnavailable, ErrorCode: "credential_unavailable"}, now), ErrInvalid, closeErr)
	}
	credential := append([]byte(nil), material...)
	started := time.Now()
	probeContext, cancel := context.WithDeadline(ctx, minimum(now.Add(definition.Timeout), claim.LeaseExpiresAt))
	result := definition.Probe.Probe(probeContext, ProbeCall{Claim: claim, Credential: credential})
	cancel()
	for index := range credential {
		credential[index] = 0
	}
	closeErr := credentialLease.Close()
	checkedAt := service.clock.Now().UTC()
	latency := time.Since(started).Milliseconds()
	if latency < 0 {
		latency = 0
	}
	if latency > 300000 {
		latency = 300000
	}
	if !validResult(result) {
		result = ProbeResult{State: domain.HealthUnavailable, ErrorCode: "health_probe_result_invalid"}
	}
	completeErr := service.complete(ctx, claim, result, checkedAt, uint32(latency))
	if closeErr != nil || completeErr != nil {
		return true, fmt.Errorf("%w: settle health probe: %v", ErrUnavailable, errors.Join(closeErr, completeErr))
	}
	if result.State == domain.HealthUnavailable {
		return true, fmt.Errorf("%w: %s", ErrUnavailable, result.ErrorCode)
	}
	return true, nil
}

func (service *Service) complete(ctx context.Context, claim Claim, result ProbeResult, checkedAt time.Time, latency ...uint32) error {
	value := uint32(0)
	if len(latency) > 0 {
		value = latency[0]
	}
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return service.repository.Complete(completionContext, Completion{Claim: claim, State: result.State, ErrorCode: result.ErrorCode,
		LatencyMilliseconds: value, CheckedAt: checkedAt.UTC()})
}

func validResult(result ProbeResult) bool {
	if result.State == domain.HealthHealthy {
		return result.ErrorCode == ""
	}
	return (result.State == domain.HealthDegraded || result.State == domain.HealthUnavailable) && validCode.MatchString(result.ErrorCode)
}

func minimum(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func defaultCapability(kind domain.ConnectorKind) domain.Capability {
	switch kind {
	case domain.ConnectorEmail:
		return domain.CapabilityEmailSend
	case domain.ConnectorGoogleDrive:
		return domain.CapabilityDriveRead
	case domain.ConnectorWebPublish:
		return domain.CapabilityWebPublish
	default:
		return ""
	}
}

func capabilityMatchesKind(kind domain.ConnectorKind, capability domain.Capability) bool {
	switch kind {
	case domain.ConnectorEmail:
		return capability == domain.CapabilityEmailRead || capability == domain.CapabilityEmailSend
	case domain.ConnectorGoogleDrive:
		return capability == domain.CapabilityDriveRead
	case domain.ConnectorWebPublish:
		return capability == domain.CapabilityWebPublish
	default:
		return false
	}
}
