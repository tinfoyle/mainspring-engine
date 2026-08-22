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
		(m.Kind == "created" || m.Kind == "updated" || m.Kind == "paused" || m.Kind == "resumed" || m.Kind == "deleted" || m.Kind == "trigger_requested") &&
		len(strings.TrimSpace(m.Reason)) >= 3 && len(strings.TrimSpace(m.Reason)) <= 500 &&
		len(m.CorrelationID) >= 1 && len(m.CorrelationID) <= 200 && !m.At.IsZero()
}

type Store interface {
	Create(context.Context, domain.Schedule, Mutation) (domain.Schedule, bool, error)
	Get(context.Context, ids.AccountID, ids.ScheduleID) (domain.Schedule, error)
	List(context.Context, ids.AccountID, ListQuery) (Page, error)
	Update(context.Context, domain.Schedule, uint64, Mutation) (domain.Schedule, error)
	EnqueueTrigger(context.Context, TriggerRequest, Mutation) (Trigger, bool, error)
}

type TriggerRequest struct {
	ID              string
	AccountID       ids.AccountID
	ScheduleID      ids.ScheduleID
	ScheduleVersion uint64
	RequestedBy     ids.UserID
	RequestedAt     time.Time
}

func (request TriggerRequest) Valid() bool {
	return ids.Validate(request.ID) == nil && ids.Validate(string(request.AccountID)) == nil && ids.Validate(string(request.ScheduleID)) == nil &&
		request.ScheduleVersion > 0 && ids.Validate(string(request.RequestedBy)) == nil && !request.RequestedAt.IsZero()
}

type Trigger struct {
	ID              string         `json:"id"`
	AccountID       ids.AccountID  `json:"account_id"`
	ScheduleID      ids.ScheduleID `json:"schedule_id"`
	ScheduleVersion uint64         `json:"schedule_version"`
	RequestedBy     ids.UserID     `json:"requested_by"`
	RequestedAt     time.Time      `json:"requested_at"`
	State           string         `json:"state"`
}

func (value Trigger) Valid() bool {
	return TriggerRequest{ID: value.ID, AccountID: value.AccountID, ScheduleID: value.ScheduleID, ScheduleVersion: value.ScheduleVersion,
		RequestedBy: value.RequestedBy, RequestedAt: value.RequestedAt}.Valid() && value.State == "accepted"
}

const MaximumPageSize = 100

type ListQuery struct {
	Limit          int
	AfterUpdatedAt *time.Time
	AfterID        ids.ScheduleID
}

type Cursor struct {
	UpdatedAt time.Time      `json:"updated_at"`
	ID        ids.ScheduleID `json:"id"`
}

type Page struct {
	Items      []domain.Schedule `json:"items"`
	NextCursor *Cursor           `json:"next_cursor,omitempty"`
}
