package integrations

import (
	"slices"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultConnectionPageSize = 50
	MaximumConnectionPageSize = 200
)

type ConnectionCursor struct {
	UpdatedAt time.Time
	ID        ids.IntegrationConnectionID
}

type ConnectionListQuery struct {
	States []domain.ConnectionState
	Kinds  []domain.ConnectorKind
	After  *ConnectionCursor
	Limit  int
}

func (query ConnectionListQuery) normalized() (ConnectionListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultConnectionPageSize
	}
	if query.Limit < 1 || query.Limit > MaximumConnectionPageSize {
		return ConnectionListQuery{}, ErrInvalid
	}
	query.States = append([]domain.ConnectionState(nil), query.States...)
	query.Kinds = append([]domain.ConnectorKind(nil), query.Kinds...)
	slices.Sort(query.States)
	slices.Sort(query.Kinds)
	for index, state := range query.States {
		if (index > 0 && query.States[index-1] == state) || (state != domain.ConnectionPending && state != domain.ConnectionActive && state != domain.ConnectionDisabled && state != domain.ConnectionRevoked) {
			return ConnectionListQuery{}, ErrInvalid
		}
	}
	for index, kind := range query.Kinds {
		if (index > 0 && query.Kinds[index-1] == kind) || (kind != domain.ConnectorEmail && kind != domain.ConnectorWebPublish) {
			return ConnectionListQuery{}, ErrInvalid
		}
	}
	if query.After != nil {
		if query.After.UpdatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil {
			return ConnectionListQuery{}, ErrInvalid
		}
		value := *query.After
		value.UpdatedAt = value.UpdatedAt.UTC()
		query.After = &value
	}
	return query, nil
}

type ConnectionPage struct {
	Items      []domain.Connection
	NextCursor *ConnectionCursor
}

type ConnectionDetail struct {
	Connection   domain.Connection
	Revision     domain.ConnectionRevision
	LatestHealth *domain.HealthObservation
}

type HealthCursor struct {
	CheckedAt time.Time
	ID        ids.IntegrationHealthObservationID
}

type HealthListQuery struct {
	ConnectionID ids.IntegrationConnectionID
	After        *HealthCursor
	Limit        int
}

func (query HealthListQuery) normalized() (HealthListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultConnectionPageSize
	}
	if ids.Validate(string(query.ConnectionID)) != nil || query.Limit < 1 || query.Limit > MaximumConnectionPageSize {
		return HealthListQuery{}, ErrInvalid
	}
	if query.After != nil {
		if query.After.CheckedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil {
			return HealthListQuery{}, ErrInvalid
		}
		value := *query.After
		value.CheckedAt = value.CheckedAt.UTC()
		query.After = &value
	}
	return query, nil
}

type HealthPage struct {
	Items      []domain.HealthObservation
	NextCursor *HealthCursor
}

type ExecutionCursor struct {
	UpdatedAt time.Time
	ID        ids.IntegrationExecutionID
}

type ExecutionListQuery struct {
	ConnectionID ids.IntegrationConnectionID
	States       []domain.ExecutionState
	Capabilities []domain.Capability
	After        *ExecutionCursor
	Limit        int
}

func (query ExecutionListQuery) normalized() (ExecutionListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultConnectionPageSize
	}
	if (query.ConnectionID != "" && ids.Validate(string(query.ConnectionID)) != nil) || query.Limit < 1 || query.Limit > MaximumConnectionPageSize {
		return ExecutionListQuery{}, ErrInvalid
	}
	query.States = append([]domain.ExecutionState(nil), query.States...)
	query.Capabilities = append([]domain.Capability(nil), query.Capabilities...)
	slices.Sort(query.States)
	slices.Sort(query.Capabilities)
	for index, state := range query.States {
		if (index > 0 && query.States[index-1] == state) || !validExecutionState(state) {
			return ExecutionListQuery{}, ErrInvalid
		}
	}
	for index, capability := range query.Capabilities {
		if (index > 0 && query.Capabilities[index-1] == capability) || (capability != domain.CapabilityEmailSend && capability != domain.CapabilityWebPublish) {
			return ExecutionListQuery{}, ErrInvalid
		}
	}
	if query.After != nil {
		if query.After.UpdatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil {
			return ExecutionListQuery{}, ErrInvalid
		}
		value := *query.After
		value.UpdatedAt = value.UpdatedAt.UTC()
		query.After = &value
	}
	return query, nil
}

func validExecutionState(state domain.ExecutionState) bool {
	return state == domain.ExecutionPrepared || state == domain.ExecutionExecuting || state == domain.ExecutionReconciling ||
		state == domain.ExecutionRetryWait || state == domain.ExecutionUnknown || state == domain.ExecutionManualResolution ||
		state == domain.ExecutionSucceeded || state == domain.ExecutionFailed || state == domain.ExecutionCancelled
}

type ExecutionPage struct {
	Items      []domain.Execution
	NextCursor *ExecutionCursor
}

type ExecutionDetail struct {
	Execution domain.Execution
	Attempts  []domain.Attempt
}
