package analyticsconversion_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsconversion"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type conversionClock struct{ now time.Time }

func (c conversionClock) Now() time.Time { return c.now }

type conversionRepository struct{ calls int }

func (r *conversionRepository) Append(context.Context, analytics.HandoffReference, analytics.Envelope) error {
	r.calls++
	return nil
}

func TestMirrorAcceptsOnlyPrivateLaunchMilestonesWithLiveHandoff(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	repository := &conversionRepository{}
	service, err := analyticsconversion.New(repository, conversionClock{now})
	if err != nil {
		t.Fatal(err)
	}
	handoff := analytics.HandoffReference{
		ReceiptEventID: ids.AnalyticsEventID("10000000-0000-4000-8000-000000000001"),
		SubjectID:      ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"),
		ExpiresAt:      now.Add(time.Hour),
	}
	envelope := analytics.Envelope{Name: analytics.RegistrationStarted, Surface: privacy.SurfacePrivate}
	if err := service.Mirror(context.Background(), handoff, envelope); err != nil || repository.calls != 1 {
		t.Fatalf("mirror calls=%d err=%v", repository.calls, err)
	}
	envelope.Name = analytics.YourTurnOpened
	if err := service.Mirror(context.Background(), handoff, envelope); err != nil || repository.calls != 1 {
		t.Fatalf("ineligible calls=%d err=%v", repository.calls, err)
	}
	envelope.Name = analytics.AccountCreated
	handoff.ExpiresAt = now
	if err := service.Mirror(context.Background(), handoff, envelope); err == nil || repository.calls != 1 {
		t.Fatalf("expired calls=%d err=%v", repository.calls, err)
	}
}
