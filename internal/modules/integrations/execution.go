package integrations

import (
	"crypto/sha256"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ExecutionState string

const (
	ExecutionPrepared         ExecutionState = "prepared"
	ExecutionExecuting        ExecutionState = "executing"
	ExecutionReconciling      ExecutionState = "reconciling"
	ExecutionRetryWait        ExecutionState = "retry_wait"
	ExecutionUnknown          ExecutionState = "unknown"
	ExecutionManualResolution ExecutionState = "manual_resolution"
	ExecutionSucceeded        ExecutionState = "succeeded"
	ExecutionFailed           ExecutionState = "failed"
	ExecutionCancelled        ExecutionState = "cancelled"
)

type AttemptMode string

const (
	AttemptExecute   AttemptMode = "execute"
	AttemptReconcile AttemptMode = "reconcile"
)

type AttemptOutcome string

const (
	AttemptSucceeded  AttemptOutcome = "succeeded"
	AttemptNotApplied AttemptOutcome = "not_applied"
	AttemptFailed     AttemptOutcome = "failed"
	AttemptUnknown    AttemptOutcome = "unknown"
)

type Execution struct {
	ID                   ids.IntegrationExecutionID          `json:"id"`
	AccountID            ids.AccountID                       `json:"account_id"`
	ReleaseID            ids.MarketingReleaseID              `json:"release_id"`
	ReleaseVersion       uint64                              `json:"release_version"`
	ApprovalID           ids.ConsequentialApprovalID         `json:"approval_id"`
	Capability           Capability                          `json:"capability"`
	ConnectionID         ids.IntegrationConnectionID         `json:"connection_id"`
	ConnectionRevisionID ids.IntegrationConnectionRevisionID `json:"connection_revision_id"`
	ConnectionRevision   uint64                              `json:"connection_revision"`
	CredentialID         ids.IntegrationCredentialID         `json:"credential_id"`
	CredentialGeneration uint64                              `json:"credential_generation"`
	PayloadSHA256        [sha256.Size]byte                   `json:"payload_sha256"`
	State                ExecutionState                      `json:"state"`
	AttemptCount         uint16                              `json:"attempt_count"`
	CurrentAttemptID     ids.IntegrationAttemptID            `json:"current_attempt_id,omitempty"`
	LastErrorCode        string                              `json:"last_error_code,omitempty"`
	LeaseExpiresAt       *time.Time                          `json:"lease_expires_at,omitempty"`
	NextAttemptAt        *time.Time                          `json:"next_attempt_at,omitempty"`
	CreatedAt            time.Time                           `json:"created_at"`
	UpdatedAt            time.Time                           `json:"updated_at"`
	CompletedAt          *time.Time                          `json:"completed_at,omitempty"`
}

type ExecutionInput struct {
	ID                 ids.IntegrationExecutionID
	AccountID          ids.AccountID
	ReleaseID          ids.MarketingReleaseID
	ReleaseVersion     uint64
	ApprovalID         ids.ConsequentialApprovalID
	Capability         Capability
	Connection         Connection
	ConnectionRevision ConnectionRevision
	Credential         CredentialBinding
	PayloadSHA256      [sha256.Size]byte
	CreatedAt          time.Time
}

func NewExecution(input ExecutionInput) (Execution, error) {
	connection, err := RestoreConnection(input.Connection)
	if err != nil {
		return Execution{}, err
	}
	revision, err := RestoreConnectionRevision(input.ConnectionRevision, connection.Kind)
	if err != nil {
		return Execution{}, err
	}
	credential, err := RestoreCredentialBinding(input.Credential)
	if err != nil {
		return Execution{}, err
	}
	input.Connection, input.ConnectionRevision, input.Credential = connection, revision, credential
	if input.Connection.AccountID != input.AccountID || input.Connection.State != ConnectionActive || input.Connection.CurrentRevisionID != input.ConnectionRevision.ID || input.Connection.CurrentRevision != input.ConnectionRevision.Revision ||
		input.ConnectionRevision.AccountID != input.AccountID || input.ConnectionRevision.ConnectionID != input.Connection.ID || !capabilityIn(input.ConnectionRevision.Capabilities, input.Capability) ||
		input.Credential.AccountID != input.AccountID || input.Credential.ConnectionID != input.Connection.ID || input.Credential.ID != input.Connection.CredentialID || input.Credential.Generation != input.Connection.CredentialGeneration || !input.Credential.Available(input.CreatedAt) {
		return Execution{}, ErrCredential
	}
	value := Execution{ID: input.ID, AccountID: input.AccountID, ReleaseID: input.ReleaseID, ReleaseVersion: input.ReleaseVersion, ApprovalID: input.ApprovalID,
		Capability: input.Capability, ConnectionID: input.Connection.ID, ConnectionRevisionID: input.ConnectionRevision.ID, ConnectionRevision: input.ConnectionRevision.Revision,
		CredentialID: input.Credential.ID, CredentialGeneration: input.Credential.Generation, PayloadSHA256: input.PayloadSHA256,
		State: ExecutionPrepared, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC()}
	return RestoreExecution(value)
}

func capabilityIn(values []Capability, target Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func RestoreExecution(value Execution) (Execution, error) {
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	for _, field := range []*time.Time{value.LeaseExpiresAt, value.NextAttemptAt, value.CompletedAt} {
		if field != nil {
			*field = field.UTC()
		}
	}
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.ReleaseID)) != nil || ids.Validate(string(value.ApprovalID)) != nil ||
		ids.Validate(string(value.ConnectionID)) != nil || ids.Validate(string(value.ConnectionRevisionID)) != nil || ids.Validate(string(value.CredentialID)) != nil ||
		value.ReleaseVersion == 0 || value.ConnectionRevision == 0 || value.CredentialGeneration == 0 || !capabilityMatchesExecution(value.Capability) || !nonzeroDigest(value.PayloadSHA256) ||
		value.AttemptCount > MaximumAttempts || !validText(value.LastErrorCode, MaximumMachineCodeBytes, false) || (value.LastErrorCode != "" && !validCodeValue(value.LastErrorCode)) || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Execution{}, ErrInvalid
	}
	inFlight := value.State == ExecutionExecuting || value.State == ExecutionReconciling
	terminal := value.State == ExecutionSucceeded || value.State == ExecutionFailed || value.State == ExecutionCancelled
	if value.State != ExecutionPrepared && value.State != ExecutionExecuting && value.State != ExecutionReconciling && value.State != ExecutionRetryWait && value.State != ExecutionUnknown &&
		value.State != ExecutionManualResolution && !terminal {
		return Execution{}, ErrInvalid
	}
	if inFlight {
		if ids.Validate(string(value.CurrentAttemptID)) != nil || value.LeaseExpiresAt == nil || !value.LeaseExpiresAt.After(value.UpdatedAt) || value.NextAttemptAt != nil || value.CompletedAt != nil || value.AttemptCount == 0 {
			return Execution{}, ErrInvalid
		}
	} else if value.CurrentAttemptID != "" || value.LeaseExpiresAt != nil {
		return Execution{}, ErrInvalid
	}
	if value.State == ExecutionRetryWait {
		if value.NextAttemptAt == nil || !value.NextAttemptAt.After(value.UpdatedAt) || value.CompletedAt != nil || value.LastErrorCode == "" || value.AttemptCount == 0 {
			return Execution{}, ErrInvalid
		}
	} else if value.NextAttemptAt != nil {
		return Execution{}, ErrInvalid
	}
	if terminal {
		if value.CompletedAt == nil || !value.CompletedAt.Equal(value.UpdatedAt) {
			return Execution{}, ErrInvalid
		}
		if value.State == ExecutionSucceeded && value.LastErrorCode != "" || value.State != ExecutionSucceeded && value.LastErrorCode == "" {
			return Execution{}, ErrInvalid
		}
	} else if value.CompletedAt != nil {
		return Execution{}, ErrInvalid
	}
	if value.State == ExecutionPrepared && (value.AttemptCount != 0 || value.LastErrorCode != "") {
		return Execution{}, ErrInvalid
	}
	if value.State == ExecutionUnknown && (value.AttemptCount == 0 || value.AttemptCount >= MaximumAttempts || value.LastErrorCode == "") {
		return Execution{}, ErrInvalid
	}
	if value.State == ExecutionManualResolution && (value.AttemptCount == 0 || value.AttemptCount > MaximumAttempts || value.LastErrorCode == "") {
		return Execution{}, ErrInvalid
	}
	return value, nil
}

func capabilityMatchesExecution(value Capability) bool {
	return value == CapabilityEmailSend || value == CapabilityWebPublish
}

type Attempt struct {
	ID             ids.IntegrationAttemptID   `json:"id"`
	AccountID      ids.AccountID              `json:"account_id"`
	ExecutionID    ids.IntegrationExecutionID `json:"execution_id"`
	Number         uint16                     `json:"number"`
	Mode           AttemptMode                `json:"mode"`
	Outcome        AttemptOutcome             `json:"outcome,omitempty"`
	ErrorCode      string                     `json:"error_code,omitempty"`
	StartedAt      time.Time                  `json:"started_at"`
	LeaseExpiresAt time.Time                  `json:"lease_expires_at"`
	CompletedAt    *time.Time                 `json:"completed_at,omitempty"`
}

func (value Execution) Claim(attemptID ids.IntegrationAttemptID, leaseExpiresAt, at time.Time) (Execution, Attempt, error) {
	if ids.Validate(string(attemptID)) != nil || !validTime(at, value.UpdatedAt) || leaseExpiresAt.UTC().After(at.UTC().Add(5*time.Minute)) || !leaseExpiresAt.UTC().After(at.UTC()) {
		return Execution{}, Attempt{}, ErrInvalid
	}
	mode := AttemptExecute
	switch value.State {
	case ExecutionPrepared:
	case ExecutionRetryWait:
		if value.NextAttemptAt == nil || value.NextAttemptAt.After(at.UTC()) {
			return Execution{}, Attempt{}, ErrState
		}
	case ExecutionUnknown:
		mode = AttemptReconcile
		if value.AttemptCount >= MaximumAttempts {
			return Execution{}, Attempt{}, ErrUncertain
		}
	default:
		return Execution{}, Attempt{}, ErrState
	}
	value.AttemptCount++
	value.State = ExecutionExecuting
	if mode == AttemptReconcile {
		value.State = ExecutionReconciling
	}
	at, leaseExpiresAt = at.UTC(), leaseExpiresAt.UTC()
	value.CurrentAttemptID, value.LastErrorCode, value.LeaseExpiresAt, value.NextAttemptAt, value.UpdatedAt = attemptID, "", &leaseExpiresAt, nil, at
	attempt := Attempt{ID: attemptID, AccountID: value.AccountID, ExecutionID: value.ID, Number: value.AttemptCount, Mode: mode, StartedAt: at, LeaseExpiresAt: leaseExpiresAt}
	value, err := RestoreExecution(value)
	return value, attempt, err
}

func (value Execution) Complete(attempt Attempt, outcome AttemptOutcome, errorCode string, retryAt *time.Time, at time.Time) (Execution, Attempt, error) {
	if (value.State != ExecutionExecuting && value.State != ExecutionReconciling) || value.CurrentAttemptID != attempt.ID || attempt.ExecutionID != value.ID || attempt.AccountID != value.AccountID || attempt.Number != value.AttemptCount || attempt.Outcome != "" || attempt.CompletedAt != nil || !validTime(at, value.UpdatedAt) {
		return Execution{}, Attempt{}, ErrState
	}
	if value.State == ExecutionExecuting && attempt.Mode != AttemptExecute || value.State == ExecutionReconciling && attempt.Mode != AttemptReconcile || !attempt.StartedAt.Equal(value.UpdatedAt) || value.LeaseExpiresAt == nil || !attempt.LeaseExpiresAt.Equal(*value.LeaseExpiresAt) {
		return Execution{}, Attempt{}, ErrState
	}
	if outcome != AttemptSucceeded && outcome != AttemptNotApplied && outcome != AttemptFailed && outcome != AttemptUnknown {
		return Execution{}, Attempt{}, ErrInvalid
	}
	if outcome == AttemptSucceeded {
		if errorCode != "" || retryAt != nil {
			return Execution{}, Attempt{}, ErrInvalid
		}
	} else if !validCodeValue(errorCode) {
		return Execution{}, Attempt{}, ErrInvalid
	}
	if outcome == AttemptNotApplied {
		if value.State != ExecutionReconciling || retryAt == nil || !retryAt.UTC().After(at.UTC()) {
			return Execution{}, Attempt{}, ErrInvalid
		}
	} else if retryAt != nil {
		return Execution{}, Attempt{}, ErrInvalid
	}
	at = at.UTC()
	attempt.Outcome, attempt.ErrorCode, attempt.CompletedAt = outcome, errorCode, &at
	value.CurrentAttemptID, value.LeaseExpiresAt, value.UpdatedAt = "", nil, at
	switch outcome {
	case AttemptSucceeded:
		value.State, value.LastErrorCode, value.CompletedAt = ExecutionSucceeded, "", &at
	case AttemptFailed:
		value.State, value.LastErrorCode, value.CompletedAt = ExecutionFailed, errorCode, &at
	case AttemptNotApplied:
		if value.AttemptCount >= MaximumAttempts {
			value.State, value.LastErrorCode, value.CompletedAt = ExecutionFailed, errorCode, &at
		} else {
			next := retryAt.UTC()
			value.State, value.LastErrorCode, value.NextAttemptAt = ExecutionRetryWait, errorCode, &next
		}
	case AttemptUnknown:
		value.State, value.LastErrorCode = ExecutionUnknown, errorCode
		if value.AttemptCount >= MaximumAttempts {
			value.State = ExecutionManualResolution
		}
	}
	value, err := RestoreExecution(value)
	return value, attempt, err
}

func (value Execution) ExpireLease(attempt Attempt, at time.Time) (Execution, Attempt, error) {
	if (value.State != ExecutionExecuting && value.State != ExecutionReconciling) || value.LeaseExpiresAt == nil || value.LeaseExpiresAt.After(at.UTC()) {
		return Execution{}, Attempt{}, ErrState
	}
	return value.Complete(attempt, AttemptUnknown, "lease_expired", nil, at)
}

func (value Execution) Cancel(errorCode string, at time.Time) (Execution, error) {
	if value.State != ExecutionPrepared && value.State != ExecutionRetryWait {
		return Execution{}, ErrState
	}
	if !validCodeValue(errorCode) || !validTime(at, value.UpdatedAt) {
		return Execution{}, ErrInvalid
	}
	at = at.UTC()
	value.State, value.LastErrorCode, value.NextAttemptAt, value.UpdatedAt, value.CompletedAt = ExecutionCancelled, errorCode, nil, at, &at
	return RestoreExecution(value)
}
