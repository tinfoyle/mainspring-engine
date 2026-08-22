// Package scheduling defines the authorized persistence boundary for customer
// schedule definitions. Worker occurrence execution is a separate use case.
package scheduling

import (
	"context"
	"errors"
	"strings"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid    = errors.New("schedule command is invalid")
	ErrNotFound   = errors.New("schedule was not found")
	ErrConflict   = errors.New("schedule conflicts with durable state")
	ErrRepository = errors.New("schedule repository unavailable")
)

type Mutation struct {
	EventID       string
	Kind          string
	ActorUserID   ids.UserID
	Reason        string
	CorrelationID string
	At            time.Time
}

func (m Mutation) Valid() bool {
	return ids.Validate(m.EventID) == nil && ids.Validate(string(m.ActorUserID)) == nil &&
		(m.Kind == "created" || m.Kind == "paused" || m.Kind == "resumed") &&
		len(strings.TrimSpace(m.Reason)) >= 3 && len(strings.TrimSpace(m.Reason)) <= 500 &&
		len(m.CorrelationID) >= 1 && len(m.CorrelationID) <= 200 && !m.At.IsZero()
}

type Store interface {
	Create(context.Context, domain.Schedule, Mutation) (domain.Schedule, bool, error)
	Get(context.Context, ids.AccountID, ids.ScheduleID) (domain.Schedule, error)
	Update(context.Context, domain.Schedule, uint64, Mutation) (domain.Schedule, error)
}
