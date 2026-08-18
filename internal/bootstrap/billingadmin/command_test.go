package billingadmin

import (
	"io"
	"log/slog"
	"testing"

	application "github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
)

func TestConfigRejectsUnconfirmedOrCrossKindOperationsBeforeDatabaseUse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := Config{DatabaseURL: "postgres://unused", Action: "inspect", Actor: "operator@example.com", Reason: "Inspect failed billing work", Environment: "staging", ConfirmEnvironment: "staging", Mode: "test", InspectLimit: application.DefaultInspectLimit}
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	base.ConfirmEnvironment = "production"
	if err := validateConfig(base, logger); err == nil {
		t.Fatal("accepted unconfirmed environment")
	}
	base.ConfirmEnvironment, base.Action, base.TargetID = "staging", "replay-event", "sub_wrong"
	if err := validateConfig(base, logger); err == nil {
		t.Fatal("accepted Subscription ID as an event target")
	}
}
