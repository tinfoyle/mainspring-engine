// Package schedulequeueadmin owns audited operator recovery for terminal
// recurring Schedule and trigger-now failures. It exposes identifiers and
// bounded error codes only; Schedule definitions never cross this boundary.
package schedulequeueadmin

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultInspectLimit = 50
	MaximumInspectLimit = 100
	QueueRecurring      = "recurring"
	QueueTrigger        = "trigger"
)

var (
	ErrInvalidChange = errors.New("Schedule queue operator change is invalid")
	ErrNotFound      = errors.New("Schedule queue job was not found")
	ErrStateConflict = errors.New("Schedule queue job is not dead letter")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Target struct {
	Queue      string
	AccountID  ids.AccountID
	ScheduleID string
	TriggerID  string
}

func (t Target) Valid() bool {
	if !validQueue(t.Queue) || ids.Validate(string(t.AccountID)) != nil || ids.Validate(t.ScheduleID) != nil {
		return false
	}
	if t.Queue == QueueRecurring {
		return t.TriggerID == ""
	}
	return ids.Validate(t.TriggerID) == nil
}

type DeadLetter struct {
	Target
	AttemptCount  int
	LastErrorCode string
	OccurrenceAt  time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	NextAttemptAt *time.Time
}

type Change struct {
	BatchID, Actor, Reason, Environment string
}

type Inspection struct {
	AuditBatchID string
	DeadLetters  []DeadLetter
}

type RequeueResult struct {
	AuditBatchID string
	DeadLetter   DeadLetter
}

type Store interface {
	Inspect(context.Context, string, int, Change) ([]DeadLetter, error)
	Requeue(context.Context, Target, Change) (DeadLetter, error)
}

type Service struct {
	store Store
	ids   ids.Generator
}

func NewService(store Store, generator ids.Generator) (*Service, error) {
	if store == nil || generator == nil {
		return nil, errors.New("Schedule queue administration dependencies are required")
	}
	return &Service{store: store, ids: generator}, nil
}

func (s *Service) Inspect(ctx context.Context, queue string, limit int, actor, reason, environment string) (Inspection, error) {
	if limit == 0 {
		limit = DefaultInspectLimit
	}
	if !validQueue(queue) || limit < 1 || limit > MaximumInspectLimit {
		return Inspection{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Inspection{}, err
	}
	records, err := s.store.Inspect(ctx, queue, limit, change)
	return Inspection{AuditBatchID: change.BatchID, DeadLetters: records}, err
}

func (s *Service) Requeue(ctx context.Context, target Target, actor, reason, environment string) (RequeueResult, error) {
	if !target.Valid() {
		return RequeueResult{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return RequeueResult{}, err
	}
	record, err := s.store.Requeue(ctx, target, change)
	return RequeueResult{AuditBatchID: change.BatchID, DeadLetter: record}, err
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	batchID := s.ids.New()
	if actor == "" || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") || len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) || ids.Validate(batchID) != nil {
		return Change{}, ErrInvalidChange
	}
	return Change{BatchID: batchID, Actor: actor, Reason: reason, Environment: environment}, nil
}

func validQueue(queue string) bool { return queue == QueueRecurring || queue == QueueTrigger }
