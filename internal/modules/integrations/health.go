package integrations

import (
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type HealthState string

const (
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthUnavailable HealthState = "unavailable"
)

// HealthObservation is immutable and content-free. It can be projected to the
// latest connector health without rewriting the observation history.
type HealthObservation struct {
	ID                   ids.IntegrationHealthObservationID  `json:"id"`
	AccountID            ids.AccountID                       `json:"account_id"`
	ConnectionID         ids.IntegrationConnectionID         `json:"connection_id"`
	ConnectionRevisionID ids.IntegrationConnectionRevisionID `json:"connection_revision_id"`
	CredentialID         ids.IntegrationCredentialID         `json:"credential_id"`
	State                HealthState                         `json:"state"`
	ErrorCode            string                              `json:"error_code,omitempty"`
	LatencyMilliseconds  uint32                              `json:"latency_milliseconds"`
	CheckedAt            time.Time                           `json:"checked_at"`
}

func NewHealthObservation(id ids.IntegrationHealthObservationID, connection Connection, state HealthState, errorCode string, latencyMilliseconds uint32, at time.Time) (HealthObservation, error) {
	if connection.State == ConnectionPending || connection.State == ConnectionRevoked || ids.Validate(string(id)) != nil || !validTime(at, connection.UpdatedAt) || latencyMilliseconds > uint32((5*time.Minute)/time.Millisecond) {
		return HealthObservation{}, ErrState
	}
	if state != HealthHealthy && state != HealthDegraded && state != HealthUnavailable {
		return HealthObservation{}, ErrInvalid
	}
	if state == HealthHealthy {
		if errorCode != "" {
			return HealthObservation{}, ErrInvalid
		}
	} else if !validCodeValue(errorCode) {
		return HealthObservation{}, ErrInvalid
	}
	return RestoreHealthObservation(HealthObservation{ID: id, AccountID: connection.AccountID, ConnectionID: connection.ID, ConnectionRevisionID: connection.CurrentRevisionID,
		CredentialID: connection.CredentialID, State: state, ErrorCode: errorCode, LatencyMilliseconds: latencyMilliseconds, CheckedAt: at.UTC()})
}

func RestoreHealthObservation(value HealthObservation) (HealthObservation, error) {
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.ConnectionID)) != nil ||
		ids.Validate(string(value.ConnectionRevisionID)) != nil || ids.Validate(string(value.CredentialID)) != nil || value.CheckedAt.IsZero() ||
		value.LatencyMilliseconds > uint32((5*time.Minute)/time.Millisecond) {
		return HealthObservation{}, ErrInvalid
	}
	if value.State == HealthHealthy {
		if value.ErrorCode != "" {
			return HealthObservation{}, ErrInvalid
		}
	} else if (value.State != HealthDegraded && value.State != HealthUnavailable) || !validCodeValue(value.ErrorCode) {
		return HealthObservation{}, ErrInvalid
	}
	value.CheckedAt = value.CheckedAt.UTC()
	return value, nil
}
