package passkeyadmin

import (
	"io"
	"log/slog"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
)

func TestValidateConfigRequiresExactEnvironmentAndActiveKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := Config{DatabaseURL: "postgres://global", Action: "reencrypt", Actor: "security@example.com", Reason: "rotate passkey envelope key", Environment: "production", ConfirmEnvironment: "production", EncryptionKeys: map[int][]byte{1: make([]byte, 32), 2: make([]byte, 32)}, ActiveKeyVersion: 2, Batch: 100}
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"wrong environment":  func(value *Config) { value.ConfirmEnvironment = "staging" },
		"missing active key": func(value *Config) { value.ActiveKeyVersion = 3 },
		"zero batch":         func(value *Config) { value.Batch = 0 },
		"large batch":        func(value *Config) { value.Batch = passkeys.MaximumRotationBatch + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := base
			mutate(&invalid)
			if err := validateConfig(invalid, logger); err == nil {
				t.Fatal("invalid passkey rotation configuration was accepted")
			}
		})
	}
	inspect := base
	inspect.Action, inspect.Batch = "inspect", 0
	if err := validateConfig(inspect, logger); err != nil {
		t.Fatalf("inspection config: %v", err)
	}
}
