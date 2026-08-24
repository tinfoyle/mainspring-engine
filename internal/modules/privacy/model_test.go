package privacy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestDecisionIsExplicitAndFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	decision, err := privacy.NewDecision(
		ids.ConsentDecisionID("10000000-0000-4000-8000-000000000001"),
		ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"),
		3,
		privacy.SurfacePublic,
		true,
		false,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allows(privacy.CategoryNecessary) || !decision.Allows(privacy.CategoryAnalytics) || decision.Allows(privacy.CategoryMarketing) || decision.Allows("unknown") {
		t.Fatalf("unexpected consent projection: %+v", decision)
	}
	if decision.EffectiveAt.Location() != time.UTC {
		t.Fatal("decision time was not normalized")
	}
}

func TestDecisionRejectsUnverifiableInputs(t *testing.T) {
	now := time.Now()
	validID := ids.ConsentDecisionID("10000000-0000-4000-8000-000000000001")
	validSubject := ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002")
	tests := []struct {
		id      ids.ConsentDecisionID
		subject ids.ConsentSubjectID
		version uint64
		surface privacy.Surface
		at      time.Time
	}{
		{"bad", validSubject, 1, privacy.SurfacePublic, now},
		{validID, "bad", 1, privacy.SurfacePublic, now},
		{validID, validSubject, 0, privacy.SurfacePublic, now},
		{validID, validSubject, 1, "shared", now},
		{validID, validSubject, 1, privacy.SurfacePublic, time.Time{}},
	}
	for _, test := range tests {
		if _, err := privacy.NewDecision(test.id, test.subject, test.version, test.surface, false, false, test.at); !errors.Is(err, privacy.ErrInvalidDecision) {
			t.Fatalf("input %+v returned %v", test, err)
		}
	}
}
