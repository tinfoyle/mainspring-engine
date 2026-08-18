// Package runnercapability owns server-side authorization and execution of
// runner tool/provider calls. Runners never receive provider credentials.
package runnercapability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	SchemaVersion      = 1
	MaximumInputBytes  = 256 << 10
	MaximumOutputBytes = 256 << 10
	MaximumDefinitions = 64
)

var (
	ErrInvalidCall       = errors.New("runner capability call is invalid")
	ErrUnavailable       = errors.New("runner capability is unavailable")
	ErrActionDenied      = errors.New("runner consequential action was denied")
	ErrActionUnavailable = errors.New("runner consequential action state is unavailable")
	ErrExecutionFailed   = errors.New("runner capability execution failed")
	ErrAuditUnavailable  = errors.New("runner capability audit is unavailable")
	validMachineCode     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
)

type Effect string

const (
	EffectReadOnly      Effect = "read_only"
	EffectConsequential Effect = "consequential"
)

type Call struct {
	SchemaVersion int             `json:"schema_version"`
	OperationID   string          `json:"operation_id"`
	Capability    string          `json:"capability"`
	Input         json.RawMessage `json:"input"`
}

type Result struct {
	SchemaVersion int             `json:"schema_version"`
	Output        json.RawMessage `json:"output"`
}

type AuthorizedCall struct {
	Grant       runnerbroker.CapabilityGrant
	OperationID string
	Input       json.RawMessage
	InputDigest [sha256.Size]byte
	Action      *ActionLease
}

type Handler interface {
	Execute(context.Context, AuthorizedCall) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, AuthorizedCall) (json.RawMessage, error)

func (f HandlerFunc) Execute(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
	return f(ctx, call)
}

// ConsequentialHandler must provide a side-effect-free reconciliation path.
// Reconcile may inspect the provider using the stable idempotency key, but it
// must never create the effect again.
type ConsequentialHandler interface {
	Handler
	Reconcile(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error)
}

type Definition struct {
	Capability string
	Effect     Effect
	Timeout    time.Duration
	Handler    Handler
}

type Authorizer interface {
	AuthorizeCapability(context.Context, string, string, string) (runnerbroker.CapabilityGrant, error)
}

type ActionRequest struct {
	AccountID    ids.AccountID
	InvocationID string
	PodUID       string
	OperationID  string
	Capability   string
	InputDigest  [sha256.Size]byte
	ExpiresAt    time.Time
}

type ActionMode string

const (
	ActionExecute   ActionMode = "execute"
	ActionReconcile ActionMode = "reconcile"
)

type ActionOutcome string

const (
	ActionSucceeded ActionOutcome = "succeeded"
	ActionFailed    ActionOutcome = "failed"
	ActionUnknown   ActionOutcome = "unknown"
)

type ActionLease struct {
	AccountID      ids.AccountID
	InvocationID   string
	OperationID    string
	AttemptID      string
	Capability     string
	InputDigest    [sha256.Size]byte
	Mode           ActionMode
	IdempotencyKey string
	LeaseExpiresAt time.Time
}

type ActionCompletion struct {
	Lease     ActionLease
	Outcome   ActionOutcome
	ErrorCode string
	At        time.Time
}

// ActionAuthorizer owns approval binding, action-ledger leasing, and durable
// outcome settlement. Execute is admitted once; every uncertain or completed
// retry is forced through the handler's side-effect-free reconciliation path.
type ActionAuthorizer interface {
	BeginAction(context.Context, ActionRequest) (ActionLease, error)
	CompleteAction(context.Context, ActionCompletion) error
}

type AuditRecord struct {
	AccountID                         ids.AccountID
	InvocationID, PodUID, OperationID string
	Capability, Effect, Decision      string
	ErrorCode                         string
	OccurredAt                        time.Time
}

type Auditor interface {
	RecordCapability(context.Context, AuditRecord) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer  Authorizer
	actions     ActionAuthorizer
	auditor     Auditor
	clock       Clock
	definitions map[string]Definition
}

func New(authorizer Authorizer, actions ActionAuthorizer, auditor Auditor, clock Clock, definitions []Definition) (*Service, error) {
	if authorizer == nil || auditor == nil || clock == nil || len(definitions) == 0 || len(definitions) > MaximumDefinitions {
		return nil, ErrInvalidCall
	}
	registered := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if !runnerbroker.ValidCapability(definition.Capability) || definition.Handler == nil || definition.Timeout < 100*time.Millisecond || definition.Timeout > 5*time.Minute || (definition.Effect != EffectReadOnly && definition.Effect != EffectConsequential) {
			return nil, ErrInvalidCall
		}
		if definition.Effect == EffectConsequential {
			if actions == nil {
				return nil, ErrInvalidCall
			}
			if _, ok := definition.Handler.(ConsequentialHandler); !ok {
				return nil, ErrInvalidCall
			}
		}
		if _, exists := registered[definition.Capability]; exists {
			return nil, ErrInvalidCall
		}
		registered[definition.Capability] = definition
	}
	return &Service{authorizer: authorizer, actions: actions, auditor: auditor, clock: clock, definitions: registered}, nil
}

func (s *Service) Invoke(ctx context.Context, token, invocationID string, call Call) (Result, error) {
	canonicalInput, digest, err := validateCall(call)
	if err != nil {
		return Result{}, err
	}
	grant, err := s.authorizer.AuthorizeCapability(ctx, token, invocationID, call.Capability)
	if err != nil {
		return Result{}, err
	}
	now := s.clock.Now().UTC()
	if !grant.ExpiresAt.After(now) {
		return Result{}, runnerbroker.ErrExchangeExpired
	}
	definition, exists := s.definitions[call.Capability]
	if !exists {
		if auditErr := s.audit(ctx, grant, call, "denied", "capability_unavailable"); auditErr != nil {
			return Result{}, auditErr
		}
		return Result{}, ErrUnavailable
	}
	authorized := AuthorizedCall{Grant: grant, OperationID: call.OperationID, Input: canonicalInput, InputDigest: digest}
	if definition.Effect == EffectConsequential {
		action := ActionRequest{AccountID: grant.AccountID, InvocationID: grant.Identity.InvocationID, PodUID: grant.Identity.PodUID, OperationID: call.OperationID, Capability: call.Capability, InputDigest: digest, ExpiresAt: grant.ExpiresAt}
		lease, err := s.actions.BeginAction(ctx, action)
		if err != nil {
			code := machineError(err, "action_denied")
			if auditErr := s.audit(ctx, grant, call, "denied", code); auditErr != nil {
				return Result{}, auditErr
			}
			if code == "action_in_progress" || code == "action_repository_unavailable" {
				return Result{}, ErrActionUnavailable
			}
			return Result{}, ErrActionDenied
		}
		if !validLease(lease, action, now) {
			if auditErr := s.audit(ctx, grant, call, "denied", "action_lease_invalid"); auditErr != nil {
				return Result{}, auditErr
			}
			return Result{}, ErrActionUnavailable
		}
		authorized.Action = &lease
	}
	if err := s.audit(ctx, grant, call, "authorized", ""); err != nil {
		return Result{}, err
	}
	deadline := now.Add(definition.Timeout)
	if grant.ExpiresAt.Before(deadline) {
		deadline = grant.ExpiresAt
	}
	executionContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var output json.RawMessage
	var executionErr error
	actionOutcome := ActionSucceeded
	if authorized.Action != nil && authorized.Action.Mode == ActionReconcile {
		output, actionOutcome, executionErr = definition.Handler.(ConsequentialHandler).Reconcile(executionContext, authorized)
		if !validActionOutcome(actionOutcome) {
			executionErr = ErrExecutionFailed
			actionOutcome = ActionUnknown
		}
	} else {
		output, executionErr = definition.Handler.Execute(executionContext, authorized)
		if executionErr != nil && authorized.Action != nil {
			actionOutcome = actionFailureOutcome(executionErr)
		}
	}
	if executionErr != nil {
		code := machineError(executionErr, "execution_failed")
		if authorized.Action != nil {
			if err := s.completeAction(ctx, *authorized.Action, actionOutcome, code); err != nil {
				return Result{}, err
			}
		}
		if err := s.auditAfterExecution(ctx, grant, call, "failed", code); err != nil {
			return Result{}, err
		}
		return Result{}, ErrExecutionFailed
	}
	if authorized.Action != nil && actionOutcome != ActionSucceeded {
		code := "action_" + string(actionOutcome)
		if err := s.completeAction(ctx, *authorized.Action, actionOutcome, code); err != nil {
			return Result{}, err
		}
		if err := s.auditAfterExecution(ctx, grant, call, "failed", code); err != nil {
			return Result{}, err
		}
		return Result{}, ErrExecutionFailed
	}
	canonicalOutput, err := canonicalObject(output, MaximumOutputBytes)
	if err != nil {
		if authorized.Action != nil {
			if completeErr := s.completeAction(ctx, *authorized.Action, ActionSucceeded, ""); completeErr != nil {
				return Result{}, completeErr
			}
		}
		if auditErr := s.auditAfterExecution(ctx, grant, call, "failed", "invalid_output"); auditErr != nil {
			return Result{}, auditErr
		}
		return Result{}, ErrExecutionFailed
	}
	if authorized.Action != nil {
		if err := s.completeAction(ctx, *authorized.Action, ActionSucceeded, ""); err != nil {
			return Result{}, err
		}
	}
	if err := s.auditAfterExecution(ctx, grant, call, "succeeded", ""); err != nil {
		return Result{}, err
	}
	return Result{SchemaVersion: SchemaVersion, Output: canonicalOutput}, nil
}

func (s *Service) completeAction(ctx context.Context, lease ActionLease, outcome ActionOutcome, errorCode string) error {
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.actions.CompleteAction(completionContext, ActionCompletion{Lease: lease, Outcome: outcome, ErrorCode: errorCode, At: s.clock.Now().UTC()}); err != nil {
		return ErrActionUnavailable
	}
	return nil
}

func (s *Service) auditAfterExecution(ctx context.Context, grant runnerbroker.CapabilityGrant, call Call, decision, errorCode string) error {
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return s.audit(auditContext, grant, call, decision, errorCode)
}

func validateCall(call Call) (json.RawMessage, [sha256.Size]byte, error) {
	if call.SchemaVersion != SchemaVersion || ids.Validate(call.OperationID) != nil || !runnerbroker.ValidCapability(call.Capability) {
		return nil, [sha256.Size]byte{}, ErrInvalidCall
	}
	input, err := canonicalObject(call.Input, MaximumInputBytes)
	if err != nil {
		return nil, [sha256.Size]byte{}, ErrInvalidCall
	}
	return input, sha256.Sum256(input), nil
}

func canonicalObject(raw json.RawMessage, maximum int) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum {
		return nil, ErrInvalidCall
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrInvalidCall
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > maximum {
		return nil, ErrInvalidCall
	}
	return canonical, nil
}

func (s *Service) audit(ctx context.Context, grant runnerbroker.CapabilityGrant, call Call, decision, errorCode string) error {
	effect := "unregistered"
	if definition, exists := s.definitions[call.Capability]; exists {
		effect = string(definition.Effect)
	}
	record := AuditRecord{AccountID: grant.AccountID, InvocationID: grant.Identity.InvocationID, PodUID: grant.Identity.PodUID, OperationID: call.OperationID, Capability: call.Capability, Effect: effect, Decision: decision, ErrorCode: errorCode, OccurredAt: s.clock.Now().UTC()}
	if err := s.auditor.RecordCapability(ctx, record); err != nil {
		return ErrAuditUnavailable
	}
	return nil
}

type codedError interface{ Code() string }

func machineError(err error, fallback string) string {
	var coded codedError
	if errors.As(err, &coded) && validMachineCode.MatchString(coded.Code()) {
		return coded.Code()
	}
	return fallback
}

type definitiveError interface{ Definitive() bool }

func actionFailureOutcome(err error) ActionOutcome {
	var definitive definitiveError
	if errors.As(err, &definitive) && definitive.Definitive() {
		return ActionFailed
	}
	return ActionUnknown
}

func validActionOutcome(outcome ActionOutcome) bool {
	return outcome == ActionSucceeded || outcome == ActionFailed || outcome == ActionUnknown
}

func validLease(lease ActionLease, request ActionRequest, now time.Time) bool {
	return lease.AccountID == request.AccountID && lease.InvocationID == request.InvocationID && lease.OperationID == request.OperationID &&
		lease.Capability == request.Capability && lease.InputDigest == request.InputDigest && ids.Validate(lease.AttemptID) == nil &&
		lease.IdempotencyKey == request.OperationID && (lease.Mode == ActionExecute || lease.Mode == ActionReconcile) && lease.LeaseExpiresAt.After(now)
}
