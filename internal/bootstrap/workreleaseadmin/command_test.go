package workreleaseadmin

import (
	"io"
	"log/slog"
	"testing"

	application "github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
)

func TestValidateConfigRequiresExactEnvironmentConfirmation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{DatabaseURL: "postgres://example", Action: "inspect", Actor: "operator", Reason: "investigate dead letters", Environment: "production", ConfirmEnvironment: "staging"}
	if err := validateConfig(config, logger); err == nil {
		t.Fatal("expected mismatched environment confirmation rejection")
	}
	config.ConfirmEnvironment = config.Environment
	if err := validateConfig(config, logger); err != nil {
		t.Fatalf("valid inspection config: %v", err)
	}
	config.Action = "requeue"
	if err := validateConfig(config, logger); err != application.ErrInvalidChange {
		t.Fatalf("invalid requeue target: %v", err)
	}
}
