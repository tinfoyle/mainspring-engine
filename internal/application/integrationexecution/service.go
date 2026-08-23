// Package integrationexecution runs bounded connector effects in the dedicated
// connector runtime. It never persists provider payloads or receives reusable
// provider credentials from the application or Agent runtimes.
package integrationexecution

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease        = 2 * time.Minute
	MaximumLease        = 5 * time.Minute
	MaximumPayloadBytes = 16 << 20
)

var (
	ErrInvalid     = errors.New("integration execution claim is invalid")
	ErrUnavailable = errors.New("integration execution is unavailable")
	validCode      = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

type Claim struct {
	AccountID            ids.AccountID
	ExecutionID          ids.IntegrationExecutionID
	AttemptID            ids.IntegrationAttemptID
	Mode                 domain.AttemptMode
	Capability           domain.Capability
	ReleaseID            ids.MarketingReleaseID
	ReleaseVersion       uint64
	ApprovalID           ids.ConsequentialApprovalID
	ConnectionID         ids.IntegrationConnectionID
	ConnectionRevisionID ids.IntegrationConnectionRevisionID
	ConnectionRevision   uint64
	CredentialID         ids.IntegrationCredentialID
	CredentialGeneration uint64
	ManifestSHA256       [sha256.Size]byte
	IdempotencyKey       ids.IntegrationExecutionID
	LeaseExpiresAt       time.Time
}

func (claim Claim) Valid(now time.Time) bool {
	return ids.Validate(string(claim.AccountID)) == nil && ids.Validate(string(claim.ExecutionID)) == nil && ids.Validate(string(claim.AttemptID)) == nil &&
		ids.Validate(string(claim.ReleaseID)) == nil && ids.Validate(string(claim.ApprovalID)) == nil && ids.Validate(string(claim.ConnectionID)) == nil &&
		ids.Validate(string(claim.ConnectionRevisionID)) == nil && ids.Validate(string(claim.CredentialID)) == nil && claim.ReleaseVersion > 0 &&
		claim.ConnectionRevision > 0 && claim.CredentialGeneration > 0 && claim.ManifestSHA256 != [sha256.Size]byte{} &&
		claim.IdempotencyKey == claim.ExecutionID && (claim.Mode == domain.AttemptExecute || claim.Mode == domain.AttemptReconcile) &&
		(claim.Capability == domain.CapabilityEmailSend || claim.Capability == domain.CapabilityWebPublish) && claim.LeaseExpiresAt.After(now.UTC())
}

type Completion struct {
	Claim       Claim
	Outcome     domain.AttemptOutcome
	ErrorCode   string
	RetryAt     *time.Time
	CompletedAt time.Time
}

type Repository interface {
	Claim(context.Context, ids.IntegrationAttemptID, time.Time, time.Time) (Claim, bool, error)
	Complete(context.Context, Completion) error
}

// Authority rechecks current Account placement plus enabled Marketing and
// Integrations packages immediately before the connector receives a payload.
type Authority interface {
	Authorize(context.Context, Claim) error
}

type Payload struct {
	CanonicalManifest []byte
	ProviderPayload   []byte
}

type PayloadSource interface {
	Load(context.Context, Claim) (Payload, error)
}

type ConnectorCall struct {
	Claim   Claim
	Payload Payload
	At      time.Time
}

type ConnectorResult struct {
	Outcome   domain.AttemptOutcome
	ErrorCode string
	RetryAt   *time.Time
}

type Connector interface {
	Execute(context.Context, ConnectorCall) ConnectorResult
	Reconcile(context.Context, ConnectorCall) ConnectorResult
}

type Definition struct {
	Capability domain.Capability
	Timeout    time.Duration
	Connector  Connector
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository  Repository
	authority   Authority
	payloads    PayloadSource
	ids         ids.Generator
	clock       Clock
	lease       time.Duration
	definitions map[domain.Capability]Definition
}

func New(repository Repository, authority Authority, payloads PayloadSource, generator ids.Generator, clock Clock, lease time.Duration, definitions []Definition) (*Service, error) {
	if repository == nil || authority == nil || payloads == nil || generator == nil || clock == nil || lease < time.Second || lease > MaximumLease || len(definitions) == 0 || len(definitions) > 2 {
		return nil, ErrInvalid
	}
	registered := make(map[domain.Capability]Definition, len(definitions))
	for _, definition := range definitions {
		if (definition.Capability != domain.CapabilityEmailSend && definition.Capability != domain.CapabilityWebPublish) || definition.Connector == nil ||
			definition.Timeout < 100*time.Millisecond || definition.Timeout > MaximumLease {
			return nil, ErrInvalid
		}
		if _, exists := registered[definition.Capability]; exists {
			return nil, ErrInvalid
		}
		registered[definition.Capability] = definition
	}
	return &Service{repository: repository, authority: authority, payloads: payloads, ids: generator, clock: clock, lease: lease, definitions: registered}, nil
}

func (service *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := service.clock.Now().UTC()
	attemptID := ids.IntegrationAttemptID(service.ids.New())
	if ids.Validate(string(attemptID)) != nil {
		return false, ErrInvalid
	}
	claim, found, err := service.repository.Claim(ctx, attemptID, now, now.Add(service.lease))
	if err != nil || !found {
		return false, err
	}
	if claim.AttemptID != attemptID || !claim.Valid(now) {
		return true, ErrInvalid
	}
	definition, exists := service.definitions[claim.Capability]
	if !exists {
		return true, service.completeWithoutEffect(ctx, claim, "connector_unavailable")
	}
	if err := service.authority.Authorize(ctx, claim); err != nil {
		return true, errors.Join(service.completeWithoutEffect(ctx, claim, "execution_authority_unavailable"), err)
	}
	payload, err := service.payloads.Load(ctx, claim)
	if err != nil || !validPayload(payload, claim.ManifestSHA256) {
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(service.completeWithoutEffect(ctx, claim, "delivery_manifest_unavailable"), err)
	}
	executionContext, cancel := context.WithDeadline(ctx, minimum(now.Add(definition.Timeout), claim.LeaseExpiresAt))
	defer cancel()
	call := ConnectorCall{Claim: claim, Payload: payload, At: now}
	var result ConnectorResult
	if claim.Mode == domain.AttemptReconcile {
		result = definition.Connector.Reconcile(executionContext, call)
	} else {
		result = definition.Connector.Execute(executionContext, call)
	}
	completedAt := service.clock.Now().UTC()
	if !validResult(result, claim.Mode, completedAt) {
		result = ConnectorResult{Outcome: domain.AttemptUnknown, ErrorCode: "connector_result_invalid"}
	}
	completion := Completion{Claim: claim, Outcome: result.Outcome, ErrorCode: result.ErrorCode, RetryAt: result.RetryAt, CompletedAt: completedAt}
	completionContext, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer completionCancel()
	if err := service.repository.Complete(completionContext, completion); err != nil {
		return true, fmt.Errorf("%w: settle: %v", ErrUnavailable, err)
	}
	if result.Outcome != domain.AttemptSucceeded {
		return true, fmt.Errorf("connector outcome %s: %w", result.Outcome, ErrUnavailable)
	}
	return true, nil
}

func (service *Service) completeWithoutEffect(ctx context.Context, claim Claim, errorCode string) error {
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	err := service.repository.Complete(completionContext, Completion{Claim: claim, Outcome: domain.AttemptFailed, ErrorCode: errorCode,
		CompletedAt: service.clock.Now().UTC()})
	if err != nil {
		return fmt.Errorf("%w: settle: %v", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, errorCode)
}

func validPayload(payload Payload, digest [sha256.Size]byte) bool {
	return len(payload.CanonicalManifest) > 0 && len(payload.CanonicalManifest) <= MaximumPayloadBytes &&
		len(payload.ProviderPayload) > 0 && len(payload.ProviderPayload) <= MaximumPayloadBytes && sha256.Sum256(payload.CanonicalManifest) == digest
}

func validResult(result ConnectorResult, mode domain.AttemptMode, at time.Time) bool {
	if result.Outcome != domain.AttemptSucceeded && result.Outcome != domain.AttemptFailed && result.Outcome != domain.AttemptUnknown &&
		result.Outcome != domain.AttemptNotApplied {
		return false
	}
	if result.Outcome == domain.AttemptNotApplied {
		return mode == domain.AttemptReconcile && validCode.MatchString(result.ErrorCode) && result.RetryAt != nil && result.RetryAt.After(at)
	}
	if result.RetryAt != nil {
		return false
	}
	if result.Outcome == domain.AttemptSucceeded {
		return result.ErrorCode == ""
	}
	return validCode.MatchString(result.ErrorCode)
}

func minimum(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}
