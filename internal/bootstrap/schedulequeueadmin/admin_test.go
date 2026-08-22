package schedulequeueadmin

import (
	"io"
	"log/slog"
	"testing"

	application "github.com/tinfoyle/spyglass-engine/internal/application/schedulequeueadmin"
)

func TestValidateConfigFailsClosed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{DatabaseURL: "postgres://example", Action: "inspect", Queue: application.QueueRecurring, Actor: "operator", Reason: "investigate dead letters", Environment: "production", ConfirmEnvironment: "staging"}
	if err := validateConfig(config, logger); err == nil {
		t.Fatal("mismatched environment confirmation accepted")
	}
	config.ConfirmEnvironment = config.Environment
	if err := validateConfig(config, logger); err != nil {
		t.Fatalf("valid inspection: %v", err)
	}
	config.Action = "requeue"
	if err := validateConfig(config, logger); err != application.ErrInvalidChange {
		t.Fatalf("invalid exact target: %v", err)
	}
}
