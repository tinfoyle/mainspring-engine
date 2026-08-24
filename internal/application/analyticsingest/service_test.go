package analyticsingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type consentSource struct{ decision privacy.Decision }

func (s consentSource) Current(context.Context, ids.ConsentSubjectID, privacy.Surface) (privacy.Decision, error) {
	return s.decision, nil
}

type sink struct {
	events []analyticsingest.AcceptedEvent
}

func (s *sink) Append(_ context.Context, event analyticsingest.AcceptedEvent) error {
	s.events = append(s.events, event)
	return nil
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestIngestRechecksCurrentConsentBeforeWriting(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(ids.ConsentDecisionID("10000000-0000-4000-8000-000000000001"), ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"), 1, privacy.SurfacePublic, true, false, now.Add(-time.Minute))
	written := &sink{}
	service, _ := analyticsingest.New(consentSource{decision}, written, analytics.LaunchRegistry(), clock{now}, 1)
	event := analytics.Envelope{ID: ids.AnalyticsEventID("10000000-0000-4000-8000-000000000003"), SubjectID: decision.SubjectID, Name: analytics.LandingViewed, Surface: privacy.SurfacePublic, OccurredAt: now, Fields: map[string]string{"device_class": "phone"}}
	if err := service.Ingest(context.Background(), event); err != nil || len(written.events) != 1 {
		t.Fatalf("ingest err=%v events=%d", err, len(written.events))
	}
	if written.events[0].ConsentDecisionID != decision.ID {
		t.Fatalf("consent decision = %q, want %q", written.events[0].ConsentDecisionID, decision.ID)
	}
	decision.Analytics = false
	service, _ = analyticsingest.New(consentSource{decision}, written, analytics.LaunchRegistry(), clock{now}, 1)
	if err := service.Ingest(context.Background(), event); !errors.Is(err, analytics.ErrConsentRequired) || len(written.events) != 1 {
		t.Fatalf("withdrawn ingest err=%v events=%d", err, len(written.events))
	}
}
