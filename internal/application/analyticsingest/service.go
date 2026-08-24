package analyticsingest

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Clock interface{ Now() time.Time }

type ConsentSource interface {
	Current(context.Context, ids.ConsentSubjectID, privacy.Surface) (privacy.Decision, error)
}

type Sink interface {
	Append(context.Context, AcceptedEvent) error
}

// AcceptedEvent binds an analytics envelope to the exact immutable consent
// decision that authorized ingestion. A later consent change therefore cannot
// rewrite the historical authorization evidence.
type AcceptedEvent struct {
	Envelope          analytics.Envelope
	ConsentDecisionID ids.ConsentDecisionID
}

type Service struct {
	consent       ConsentSource
	sink          Sink
	registry      analytics.Registry
	clock         Clock
	policyVersion uint64
}

func New(consent ConsentSource, sink Sink, registry analytics.Registry, clock Clock, policyVersion uint64) (*Service, error) {
	if consent == nil || sink == nil || clock == nil || policyVersion == 0 {
		return nil, errors.New("analytics ingestion dependencies are required")
	}
	return &Service{consent: consent, sink: sink, registry: registry, clock: clock, policyVersion: policyVersion}, nil
}

func (s *Service) Ingest(ctx context.Context, envelope analytics.Envelope) error {
	decision, err := s.consent.Current(ctx, envelope.SubjectID, envelope.Surface)
	if err != nil {
		return err
	}
	if decision.PolicyVersion != s.policyVersion {
		return analytics.ErrConsentRequired
	}
	if err := s.registry.Validate(envelope, decision, s.clock.Now()); err != nil {
		return err
	}
	return s.sink.Append(ctx, AcceptedEvent{
		Envelope:          envelope,
		ConsentDecisionID: decision.ID,
	})
}
