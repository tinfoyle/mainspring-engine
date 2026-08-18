// Package workreleaseadmin owns audited operator recovery for terminal Work
// capacity-release failures. It exposes technical identifiers only.
package workreleaseadmin

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
)

var (
	ErrInvalidChange = errors.New("Work release operator change is invalid")
	ErrNotFound      = errors.New("Work release job was not found")
	ErrStateConflict = errors.New("Work release job is not dead letter")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Target struct {
	AccountID     ids.AccountID
	WorkItemID    ids.WorkItemID
	ReservationID string
}

func (t Target) Valid() bool {
	return ids.Validate(string(t.AccountID)) == nil && ids.Validate(string(t.WorkItemID)) == nil && ids.Validate(t.ReservationID) == nil
}

type DeadLetter struct {
	Target
	AttemptCount  int
	LastErrorCode string
	QueuedAt      time.Time
	LastAttemptAt *time.Time
	NextAttemptAt *time.Time
}

type Change struct {
	BatchID     string
	Actor       string
	Reason      string
	Environment string
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
	Inspect(context.Context, int, Change) ([]DeadLetter, error)
	Requeue(context.Context, Target, Change) (DeadLetter, error)
}

type Service struct {
	store Store
	ids   ids.Generator
}

func NewService(store Store, generator ids.Generator) (*Service, error) {
	if store == nil || generator == nil {
		return nil, errors.New("Work release administration dependencies are required")
	}
	return &Service{store: store, ids: generator}, nil
}

func (s *Service) Inspect(ctx context.Context, limit int, actor, reason, environment string) (Inspection, error) {
	if limit == 0 {
		limit = DefaultInspectLimit
	}
	if limit < 1 || limit > MaximumInspectLimit {
		return Inspection{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Inspection{}, err
	}
	records, err := s.store.Inspect(ctx, limit, change)
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
	if actor == "" || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") || len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	batchID := s.ids.New()
	if ids.Validate(batchID) != nil {
		return Change{}, ErrInvalidChange
	}
	return Change{BatchID: batchID, Actor: actor, Reason: reason, Environment: environment}, nil
}
