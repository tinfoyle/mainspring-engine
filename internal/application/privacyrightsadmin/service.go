// Package privacyrightsadmin owns the audited operator boundary for fulfilling
// verified privacy-rights requests. It records content-free evidence bindings;
// customer data and case documents never enter the command or event log.
package privacyrightsadmin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidChange = errors.New("privacy rights operator change is invalid")
	ErrNotFound      = errors.New("privacy rights request was not found")
	ErrStateConflict = errors.New("privacy rights request state changed")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Change struct {
	EventID     ids.PrivacyRightsEventID
	Actor       string
	Reason      string
	Environment string
}

type ResolutionEvidence struct {
	ID     string
	SHA256 [32]byte
}

type Store interface {
	Inspect(context.Context, ids.PrivacyRightsRequestID, Change) (privacy.RightsRequest, error)
	StartReview(context.Context, ids.PrivacyRightsRequestID, uint64, Change) (privacy.RightsRequest, error)
	Resolve(context.Context, ids.PrivacyRightsRequestID, uint64, privacy.RightsState, ResolutionEvidence, Change) (privacy.RightsRequest, error)
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

func (s *Service) Inspect(ctx context.Context, requestID ids.PrivacyRightsRequestID, actor, reason, environment string) (privacy.RightsRequest, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(requestID)) != nil {
		return privacy.RightsRequest{}, ErrInvalidChange
	}
	return s.store.Inspect(ctx, requestID, change)
}

func (s *Service) StartReview(ctx context.Context, requestID ids.PrivacyRightsRequestID, expectedVersion uint64, actor, reason, environment string) (privacy.RightsRequest, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(requestID)) != nil || expectedVersion == 0 {
		return privacy.RightsRequest{}, ErrInvalidChange
	}
	return s.store.StartReview(ctx, requestID, expectedVersion, change)
}

func (s *Service) Resolve(ctx context.Context, requestID ids.PrivacyRightsRequestID, expectedVersion uint64, state privacy.RightsState, evidence ResolutionEvidence, actor, reason, environment string) (privacy.RightsRequest, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(requestID)) != nil || expectedVersion == 0 || ids.Validate(evidence.ID) != nil ||
		(state != privacy.RightsCompleted && state != privacy.RightsPartiallyCompleted && state != privacy.RightsDeclined) {
		return privacy.RightsRequest{}, ErrInvalidChange
	}
	return s.store.Resolve(ctx, requestID, expectedVersion, state, evidence, change)
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	eventID := ids.PrivacyRightsEventID(s.ids.New())
	if ids.Validate(string(eventID)) != nil || len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") ||
		len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}
