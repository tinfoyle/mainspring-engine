package analytics_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestLaunchRegistryAcceptsOnlyConsentedReviewedFields(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("10000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePrivate,
		true,
		false,
		now.Add(-time.Minute),
	)
	envelope := analytics.Envelope{
		ID:         ids.AnalyticsEventID("10000000-0000-4000-8000-000000000003"),
		SubjectID:  decision.SubjectID,
		Name:       analytics.YourTurnItemCompleted,
		Surface:    privacy.SurfacePrivate,
		OccurredAt: now,
		Fields: map[string]string{
			"task_category":   "work_review",
			"result":          "completed",
			"duration_bucket": "under_5m",
			"device_class":    "phone",
		},
	}
	if err := analytics.LaunchRegistry().Validate(envelope, decision, now); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRegistryRejectsContentIdentityAndConsentBypass(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("10000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePublic,
		false,
		false,
		now.Add(-time.Minute),
	)
	base := analytics.Envelope{ID: ids.AnalyticsEventID("10000000-0000-4000-8000-000000000003"), SubjectID: decision.SubjectID, Name: analytics.LandingViewed, Surface: privacy.SurfacePublic, OccurredAt: now, Fields: map[string]string{"device_class": "phone"}}
	if err := analytics.LaunchRegistry().Validate(base, decision, now); !errors.Is(err, analytics.ErrConsentRequired) {
		t.Fatalf("consent denial returned %v", err)
	}
	decision.Analytics = true
	base.Fields["email"] = "person@example.com"
	if err := analytics.LaunchRegistry().Validate(base, decision, now); !errors.Is(err, analytics.ErrProhibitedField) {
		t.Fatalf("identity field returned %v", err)
	}
	delete(base.Fields, "email")
	base.Fields["campaign_name"] = "unreviewed"
	if err := analytics.LaunchRegistry().Validate(base, decision, now); !errors.Is(err, analytics.ErrUnsupportedField) {
		t.Fatalf("unknown field returned %v", err)
	}
	delete(base.Fields, "campaign_name")
	base.Fields["route_name"] = "landing?email=person@example.com"
	if err := analytics.LaunchRegistry().Validate(base, decision, now); !errors.Is(err, analytics.ErrInvalidEvent) {
		t.Fatalf("unsafe dimension returned %v", err)
	}
}

func TestLaunchRegistryRestrictsApplicationEntryTaxonomy(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("20000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("20000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePrivate,
		true,
		false,
		now.Add(-time.Minute),
	)
	envelope := analytics.Envelope{ID: ids.AnalyticsEventID("20000000-0000-4000-8000-000000000003"), SubjectID: decision.SubjectID, Name: analytics.ApplicationEntered, Surface: privacy.SurfacePrivate, OccurredAt: now}
	for _, entryPoint := range []string{"checkout", "your_turn", "deep_link"} {
		envelope.Fields = map[string]string{"entry_point": entryPoint}
		if err := analytics.LaunchRegistry().Validate(envelope, decision, now); err != nil {
			t.Fatalf("entry point %q rejected: %v", entryPoint, err)
		}
	}
	envelope.Fields = map[string]string{"entry_point": "affiliate-code-from-url"}
	if err := analytics.LaunchRegistry().Validate(envelope, decision, now); !errors.Is(err, analytics.ErrInvalidEvent) {
		t.Fatalf("unreviewed entry point returned %v", err)
	}
}
