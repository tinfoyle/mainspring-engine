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
