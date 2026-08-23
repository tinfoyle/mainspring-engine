package integrations

import (
	"crypto/sha256"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ResolutionState string

const (
	ResolutionPending ResolutionState = "pending"
	ResolutionApplied ResolutionState = "applied"
)

// ExecutionResolution is content-free evidence for a dual-controlled decision
// about an external effect whose provider outcome could not be reconciled.
type ExecutionResolution struct {
	ID                ids.IntegrationResolutionID `json:"id"`
	AccountID         ids.AccountID               `json:"account_id"`
	ExecutionID       ids.IntegrationExecutionID  `json:"execution_id"`
	RequestedOutcome  ExecutionState              `json:"requested_outcome"`
	EvidenceSHA256    [sha256.Size]byte           `json:"evidence_sha256"`
	RequestedByUserID ids.UserID                  `json:"requested_by_user_id"`
	RequestedAt       time.Time                   `json:"requested_at"`
	State             ResolutionState             `json:"state"`
	ConfirmedByUserID ids.UserID                  `json:"confirmed_by_user_id,omitempty"`
	ConfirmedAt       *time.Time                  `json:"confirmed_at,omitempty"`
}

func RestoreExecutionResolution(value ExecutionResolution) (ExecutionResolution, error) {
	value.RequestedAt = value.RequestedAt.UTC()
	if value.ConfirmedAt != nil {
		at := value.ConfirmedAt.UTC()
		value.ConfirmedAt = &at
	}
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil ||
		ids.Validate(string(value.ExecutionID)) != nil || ids.Validate(string(value.RequestedByUserID)) != nil ||
		(value.RequestedOutcome != ExecutionSucceeded && value.RequestedOutcome != ExecutionFailed) ||
		!nonzeroDigest(value.EvidenceSHA256) || value.RequestedAt.IsZero() {
		return ExecutionResolution{}, ErrInvalid
	}
	switch value.State {
	case ResolutionPending:
		if value.ConfirmedByUserID != "" || value.ConfirmedAt != nil {
			return ExecutionResolution{}, ErrInvalid
		}
	case ResolutionApplied:
		if ids.Validate(string(value.ConfirmedByUserID)) != nil || value.ConfirmedByUserID == value.RequestedByUserID ||
			value.ConfirmedAt == nil || value.ConfirmedAt.Before(value.RequestedAt) {
			return ExecutionResolution{}, ErrInvalid
		}
	default:
		return ExecutionResolution{}, ErrInvalid
	}
	return value, nil
}
