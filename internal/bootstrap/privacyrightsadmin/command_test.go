package privacyrightsadmin

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	application "github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestConfigRequiresExactEnvironmentAndResolutionEvidence(t *testing.T) {
	base := Config{DatabaseURL: "postgres://example", Action: "resolve", Actor: "privacy@example.test", Reason: "Fulfillment reviewed",
		Environment: "production", ConfirmEnvironment: "production", RequestID: "10000000-0000-4000-8000-000000000001",
		ExpectedVersion: 2, ResolutionState: privacy.RightsCompleted,
		Evidence: application.ResolutionEvidence{ID: "20000000-0000-4000-8000-000000000002", SHA256: [32]byte{1}}}
	if err := validateConfig(base, slog.Default()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"environment": func(value *Config) { value.ConfirmEnvironment = "staging" },
		"request":     func(value *Config) { value.RequestID = ids.PrivacyRightsRequestID("bad") },
		"version":     func(value *Config) { value.ExpectedVersion = 0 },
		"state":       func(value *Config) { value.ResolutionState = privacy.RightsCanceled },
		"evidence":    func(value *Config) { value.Evidence.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateConfig(changed, slog.Default()); !errors.Is(err, application.ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestListOpenRequiresExplicitBoundedDeadlineScope(t *testing.T) {
	base := Config{DatabaseURL: "postgres://example", Action: "list-open", Actor: "privacy@example.test",
		Reason: "Prioritize the open deadline queue", Environment: "production", ConfirmEnvironment: "production",
		DueBefore: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Limit: 50}
	if err := validateConfig(base, slog.Default()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"deadline": func(value *Config) { value.DueBefore = time.Time{} },
		"limit":    func(value *Config) { value.Limit = 101 },
		"request":  func(value *Config) { value.RequestID = "10000000-0000-4000-8000-000000000001" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if err := validateConfig(changed, slog.Default()); !errors.Is(err, application.ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
