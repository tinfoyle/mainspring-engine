// Package affiliatesupportadmin owns the audited operator boundary for
// structured Affiliate support inspections and exact-version decisions.
package affiliatesupportadmin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidChange = errors.New("Affiliate support operator change is invalid")
	ErrNotFound      = errors.New("Affiliate support request was not found")
	ErrStateConflict = errors.New("Affiliate support request state changed")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Change struct {
	EventID     ids.AffiliateSupportEventID
	Actor       string
	Reason      string
	Environment string
}

type Store interface {
	Inspect(context.Context, ids.AffiliateSupportRequestID, Change) (affiliates.SupportRequest, error)
	Transition(context.Context, ids.AffiliateSupportRequestID, uint64, affiliates.SupportState, affiliates.SupportOutcome, Change) (affiliates.SupportRequest, error)
}

type Service struct {
	store Store
	ids   ids.Generator
}

func New(store Store, generator ids.Generator) (*Service, error) {
	if store == nil || generator == nil {
		return nil, ErrInvalidChange
	}
	return &Service{store: store, ids: generator}, nil
}

func (s *Service) Inspect(ctx context.Context, requestID ids.AffiliateSupportRequestID, actor, reason, environment string) (affiliates.SupportRequest, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(requestID)) != nil {
		return affiliates.SupportRequest{}, ErrInvalidChange
	}
	return s.store.Inspect(ctx, requestID, change)
}

func (s *Service) StartReview(ctx context.Context, requestID ids.AffiliateSupportRequestID, expectedVersion uint64, actor, reason, environment string) (affiliates.SupportRequest, error) {
	return s.transition(ctx, requestID, expectedVersion, affiliates.SupportInReview, "", actor, reason, environment)
}

func (s *Service) Resolve(ctx context.Context, requestID ids.AffiliateSupportRequestID, expectedVersion uint64, outcome affiliates.SupportOutcome, actor, reason, environment string) (affiliates.SupportRequest, error) {
	state := affiliates.SupportResolved
	if outcome == affiliates.SupportDenied {
		state = affiliates.SupportDeclined
	} else if outcome != affiliates.SupportApproved {
		return affiliates.SupportRequest{}, ErrInvalidChange
	}
	return s.transition(ctx, requestID, expectedVersion, state, outcome, actor, reason, environment)
}

func (s *Service) transition(ctx context.Context, requestID ids.AffiliateSupportRequestID, expectedVersion uint64, state affiliates.SupportState, outcome affiliates.SupportOutcome, actor, reason, environment string) (affiliates.SupportRequest, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(requestID)) != nil || expectedVersion == 0 {
		return affiliates.SupportRequest{}, ErrInvalidChange
	}
	return s.store.Transition(ctx, requestID, expectedVersion, state, outcome, change)
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	change := Change{EventID: ids.AffiliateSupportEventID(s.ids.New()), Actor: actor, Reason: reason, Environment: environment}
	if ids.Validate(string(change.EventID)) != nil || len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") ||
		len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	return change, nil
}
