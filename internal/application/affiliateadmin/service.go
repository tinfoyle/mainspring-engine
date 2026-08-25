// Package affiliateadmin owns the audited operator boundary for Affiliate
// enrollment inspection, suspension, reactivation, and terminal closure.
package affiliateadmin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidChange = errors.New("Affiliate operator change is invalid")
	ErrNotFound      = errors.New("Affiliate enrollment was not found")
	ErrStateConflict = errors.New("Affiliate enrollment state changed")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Change struct {
	EventID     ids.AffiliateEnrollmentEventID
	Actor       string
	Reason      string
	Environment string
}

type Store interface {
	Inspect(context.Context, ids.AffiliateID, Change) (affiliates.Enrollment, error)
	Transition(context.Context, ids.AffiliateID, uint64, affiliates.EnrollmentState, Change) (affiliates.Enrollment, error)
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

func (s *Service) Inspect(ctx context.Context, affiliateID ids.AffiliateID, actor, reason, environment string) (affiliates.Enrollment, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil {
		return affiliates.Enrollment{}, ErrInvalidChange
	}
	return s.store.Inspect(ctx, affiliateID, change)
}

func (s *Service) Transition(ctx context.Context, affiliateID ids.AffiliateID, expectedVersion uint64, state affiliates.EnrollmentState, actor, reason, environment string) (affiliates.Enrollment, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(string(affiliateID)) != nil || expectedVersion == 0 ||
		(state != affiliates.EnrollmentActive && state != affiliates.EnrollmentSuspended && state != affiliates.EnrollmentClosed) {
		return affiliates.Enrollment{}, ErrInvalidChange
	}
	return s.store.Transition(ctx, affiliateID, expectedVersion, state, change)
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	eventID := ids.AffiliateEnrollmentEventID(s.ids.New())
	if ids.Validate(string(eventID)) != nil || len(actor) < 3 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") ||
		len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}
