// Package analyticsconversion mirrors a reviewed subset of private milestones
// onto a short-lived anonymous public handoff. It never receives or stores the
// private consent subject, User, or Account identity.
package analyticsconversion

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
)

var ErrInvalidHandoff = errors.New("analytics conversion handoff is invalid")

type Clock interface{ Now() time.Time }

type Repository interface {
	Append(context.Context, analytics.HandoffReference, analytics.Envelope) error
}

type Service struct {
	repository Repository
	clock      Clock
}

var eligible = map[analytics.EventName]struct{}{
	analytics.RegistrationStarted:        {},
	analytics.VerificationCompleted:      {},
	analytics.AccountCreated:             {},
	analytics.SecurityEnrollmentComplete: {},
	analytics.CheckoutReviewed:           {},
	analytics.CheckoutRedirected:         {},
	analytics.SubscriptionProjected:      {},
	analytics.ApplicationEntered:         {},
}

func New(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, errors.New("analytics conversion dependencies are required")
	}
	return &Service{repository: repository, clock: clock}, nil
}

func Eligible(name analytics.EventName) bool {
	_, ok := eligible[name]
	return ok
}

func (s *Service) Mirror(ctx context.Context, handoff analytics.HandoffReference, envelope analytics.Envelope) error {
	if !Eligible(envelope.Name) {
		return nil
	}
	if handoff.Validate(s.clock.Now()) != nil || envelope.Surface != privacy.SurfacePrivate {
		return ErrInvalidHandoff
	}
	if err := s.repository.Append(ctx, handoff, envelope); err != nil {
		return err
	}
	return nil
}
