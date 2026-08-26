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
			"task_category":   "review",
			"result":          "completed",
			"duration_bucket": "1m_5m",
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

func TestLaunchRegistryRestrictsReviewedCategoricalValues(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("30000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("30000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePrivate,
		true,
		false,
		now.Add(-time.Minute),
	)
	tests := []struct {
		name   analytics.EventName
		fields map[string]string
	}{
		{analytics.SecurityEnrollmentComplete, map[string]string{"method": "password_only"}},
		{analytics.CheckoutReviewed, map[string]string{"referral_present": "sometimes"}},
		{analytics.ReferralCodeAccepted, map[string]string{"entry_method": "query_string"}},
		{analytics.CheckoutReturned, map[string]string{"result": "success"}},
		{analytics.SubscriptionProjected, map[string]string{"result": "pending"}},
		{analytics.YourTurnOpened, map[string]string{"queue_state": "busy"}},
		{analytics.YourTurnItemCompleted, map[string]string{"task_category": "customer_name"}},
		{analytics.YourTurnItemCompleted, map[string]string{"result": "approved"}},
		{analytics.YourTurnItemCompleted, map[string]string{"duration_bucket": "exactly_37_seconds"}},
		{analytics.LandingViewed, map[string]string{"device_class": "watch"}},
	}
	for index, test := range tests {
		envelope := analytics.Envelope{
			ID:         ids.AnalyticsEventID("30000000-0000-4000-8000-000000000003"),
			SubjectID:  decision.SubjectID,
			Name:       test.name,
			Surface:    privacy.SurfacePrivate,
			OccurredAt: now,
			Fields:     test.fields,
		}
		if err := analytics.LaunchRegistry().Validate(envelope, decision, now); !errors.Is(err, analytics.ErrInvalidEvent) {
			t.Fatalf("case %d (%s) returned %v", index, test.name, err)
		}
	}
}

func TestLaunchRegistryAcceptsReviewedCategoricalValues(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("40000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("40000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePrivate,
		true,
		false,
		now.Add(-time.Minute),
	)
	publicDecision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("40000000-0000-4000-8000-000000000004"),
		ids.ConsentSubjectID("40000000-0000-4000-8000-000000000005"),
		1,
		privacy.SurfacePublic,
		true,
		false,
		now.Add(-time.Minute),
	)
	tests := []struct {
		name   analytics.EventName
		field  string
		values []string
		public bool
	}{
		{analytics.SecurityEnrollmentComplete, "method", []string{"passkey_recovery_codes"}, false},
		{analytics.CheckoutReviewed, "referral_present", []string{"false", "true"}, false},
		{analytics.ReferralCodeAccepted, "entry_method", []string{"link", "manual"}, false},
		{analytics.CheckoutRedirected, "referral_present", []string{"false", "true"}, false},
		{analytics.CheckoutReturned, "result", []string{"cancelled", "returned"}, false},
		{analytics.SubscriptionProjected, "result", []string{"active", "attention", "failed"}, false},
		{analytics.ApplicationEntered, "entry_point", []string{"checkout", "deep_link", "your_turn"}, false},
		{analytics.YourTurnOpened, "queue_state", []string{"empty", "open"}, false},
		{analytics.YourTurnItemCompleted, "task_category", []string{"action", "approval", "information", "review"}, false},
		{analytics.YourTurnItemCompleted, "result", []string{"completed"}, false},
		{analytics.YourTurnItemCompleted, "duration_bucket", []string{"under_1m", "1m_5m", "over_5m"}, false},
		{analytics.LandingViewed, "device_class", []string{"desktop", "phone", "tablet"}, true},
	}
	for index, test := range tests {
		eventDecision := decision
		if test.public {
			eventDecision = publicDecision
		}
		for _, value := range test.values {
			envelope := analytics.Envelope{
				ID:         ids.AnalyticsEventID("40000000-0000-4000-8000-000000000003"),
				SubjectID:  eventDecision.SubjectID,
				Name:       test.name,
				Surface:    eventDecision.Surface,
				OccurredAt: now,
				Fields:     map[string]string{test.field: value},
			}
			if err := analytics.LaunchRegistry().Validate(envelope, eventDecision, now); err != nil {
				t.Fatalf("case %d (%s.%s=%s) returned %v", index, test.name, test.field, value, err)
			}
		}
	}
}

func TestLaunchRegistryRejectsEventOnUnreviewedSurface(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	decision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("50000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("50000000-0000-4000-8000-000000000002"),
		1,
		privacy.SurfacePrivate,
		true,
		false,
		now.Add(-time.Minute),
	)
	publicEnvelope := analytics.Envelope{
		ID:         ids.AnalyticsEventID("50000000-0000-4000-8000-000000000003"),
		SubjectID:  decision.SubjectID,
		Name:       analytics.LandingViewed,
		Surface:    privacy.SurfacePrivate,
		OccurredAt: now,
	}
	if err := analytics.LaunchRegistry().Validate(publicEnvelope, decision, now); !errors.Is(err, analytics.ErrInvalidEvent) {
		t.Fatalf("public event on private surface returned %v", err)
	}
	publicDecision, _ := privacy.NewDecision(
		ids.ConsentDecisionID("50000000-0000-4000-8000-000000000004"),
		ids.ConsentSubjectID("50000000-0000-4000-8000-000000000005"),
		1,
		privacy.SurfacePublic,
		true,
		false,
		now.Add(-time.Minute),
	)
	privateEnvelope := analytics.Envelope{
		ID:         ids.AnalyticsEventID("50000000-0000-4000-8000-000000000006"),
		SubjectID:  publicDecision.SubjectID,
		Name:       analytics.AccountCreated,
		Surface:    privacy.SurfacePublic,
		OccurredAt: now,
	}
	if err := analytics.LaunchRegistry().Validate(privateEnvelope, publicDecision, now); !errors.Is(err, analytics.ErrInvalidEvent) {
		t.Fatalf("private event on public surface returned %v", err)
	}
}
