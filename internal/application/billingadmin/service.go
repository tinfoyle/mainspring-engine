// Package billingadmin owns audited operator inspection and safe replay of
// Stripe-derived work. It never accepts provider payloads or grants access.
package billingadmin

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
	ErrInvalidChange = errors.New("billing operator change is invalid")
	ErrNotFound      = errors.New("billing operator target was not found")
	ErrStateConflict = errors.New("billing operator target is not replayable")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Record struct {
	Kind, TargetID, AccountID, Mode, State, LastErrorCode string
	ExplanationCode, Explanation                          string
	AttemptCount                                          int
	NextAttemptAt                                         *time.Time
	CreatedAt                                             time.Time
}

type Change struct{ BatchID, Actor, Reason, Environment, Mode string }

type Store interface {
	Inspect(context.Context, int, Change) ([]Record, error)
	ReplayEvent(context.Context, string, Change) (Record, error)
	QueueRefresh(context.Context, string, Change) (Record, error)
}

type Service struct {
	store Store
	ids   ids.Generator
}

func NewService(store Store, generator ids.Generator) (*Service, error) {
	if store == nil || generator == nil {
		return nil, ErrInvalidChange
	}
	return &Service{store: store, ids: generator}, nil
}

func (s *Service) Inspect(ctx context.Context, limit int, actor, reason, environment, mode string) ([]Record, string, error) {
	if limit == 0 {
		limit = DefaultInspectLimit
	}
	if limit < 1 || limit > MaximumInspectLimit {
		return nil, "", ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment, mode)
	if err != nil {
		return nil, "", err
	}
	records, err := s.store.Inspect(ctx, limit, change)
	for index := range records {
		records[index] = explain(records[index])
	}
	return records, change.BatchID, err
}

func (s *Service) ReplayEvent(ctx context.Context, eventID, actor, reason, environment, mode string) (Record, string, error) {
	if !validProviderID(eventID, "evt_") {
		return Record{}, "", ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment, mode)
	if err != nil {
		return Record{}, "", err
	}
	record, err := s.store.ReplayEvent(ctx, eventID, change)
	return explain(record), change.BatchID, err
}

func (s *Service) QueueRefresh(ctx context.Context, subscriptionID, actor, reason, environment, mode string) (Record, string, error) {
	if !validProviderID(subscriptionID, "sub_") {
		return Record{}, "", ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment, mode)
	if err != nil {
		return Record{}, "", err
	}
	record, err := s.store.QueueRefresh(ctx, subscriptionID, change)
	return explain(record), change.BatchID, err
}

func explain(record Record) Record {
	switch record.LastErrorCode {
	case "subscription_mapping_mismatch":
		record.ExplanationCode = "mapping_conflict"
		record.Explanation = "Provider subscription metadata conflicts with the immutable local account, offer, or catalog mapping; correct the mapping before replaying."
	case "subscription_unmapped":
		record.ExplanationCode = "mapping_missing"
		record.Explanation = "The current provider subscription does not resolve to a published local offer and price mapping."
	case "projection_failed":
		record.ExplanationCode = "projection_retryable"
		record.Explanation = "The verified event could not be projected from current provider state; inspect provider availability and the projection worker."
	case "refresh_failed":
		record.ExplanationCode = "refresh_retryable"
		record.Explanation = "The current provider subscription could not be retrieved or projected; inspect provider availability and reconciliation logs."
	default:
		record.ExplanationCode = "queued_or_healthy"
		record.Explanation = "No classified mapping failure is recorded; use the state and attempt count to determine whether work is queued or healthy."
	}
	return record
}

func (s *Service) change(actor, reason, environment, mode string) (Change, error) {
	actor, reason, environment, mode = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment), strings.TrimSpace(mode)
	batchID := s.ids.New()
	if len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") || len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) || (mode != "test" && mode != "live") || ids.Validate(batchID) != nil {
		return Change{}, ErrInvalidChange
	}
	return Change{BatchID: batchID, Actor: actor, Reason: reason, Environment: environment, Mode: mode}, nil
}

func validProviderID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) > len(prefix) && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t ")
}
