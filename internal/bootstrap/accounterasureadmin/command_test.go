package accounterasureadmin

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestValidateConfigRequiresExactEnvironmentAndAccountConfirmation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := Config{GlobalDatabaseURL: "postgres://global", CellDatabaseURL: "postgres://cell", CellID: "cell-a", Action: "prepare", Actor: "operator@example.com", Reason: "prepare reviewed Account erasure", Environment: "production", ConfirmEnvironment: "production", AccountID: ids.AccountID("11111111-1111-4111-8111-111111111111"), ConfirmAccountID: ids.AccountID("11111111-1111-4111-8111-111111111111"), PolicyVersion: 1, BackupExpiresAt: time.Now().Add(time.Hour), Export: accounterasure.ExportEvidence{Disposition: accounterasure.ExportNotApplicable, Reason: "policy-approved exception"}}
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	wrongEnvironment := base
	wrongEnvironment.ConfirmEnvironment = "staging"
	if err := validateConfig(wrongEnvironment, logger); err == nil {
		t.Fatal("mismatched environment confirmation was accepted")
	}
	wrongAccount := base
	wrongAccount.ConfirmAccountID = "22222222-2222-4222-8222-222222222222"
	if err := validateConfig(wrongAccount, logger); err == nil {
		t.Fatal("mismatched Account confirmation was accepted")
	}
}

func TestValidateConfigKeepsInspectionGlobalOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{GlobalDatabaseURL: "postgres://global", Action: "inspect", Actor: "reviewer@example.com", Reason: "inspect prepared Account erasure", Environment: "test", ConfirmEnvironment: "test", RequestID: "11111111-1111-4111-8111-111111111111"}
	if err := validateConfig(config, logger); err != nil {
		t.Fatal(err)
	}
}

func TestValidateConfigRequiresSplitExecutionAuthorityAndExactTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{
		GlobalDatabaseURL: "postgres://global", CellDatabaseURL: "postgres://cell", CellID: "cell-a",
		Action: "execute", Actor: "executor@example.com", Reason: "execute reviewed Account erasure",
		Environment: "production", ConfirmEnvironment: "production",
		AccountID: "11111111-1111-4111-8111-111111111111", ConfirmAccountID: "11111111-1111-4111-8111-111111111111",
		RequestID: "22222222-2222-4222-8222-222222222222", ExpectedVersion: 2,
		EvidenceKey: make([]byte, 32), LeaseDuration: 5 * time.Minute,
	}
	if err := validateConfig(config, logger); err != nil {
		t.Fatal(err)
	}
	missingCell := config
	missingCell.CellDatabaseURL = ""
	if err := validateConfig(missingCell, logger); err == nil {
		t.Fatal("execution without a distinct cell database was accepted")
	}
	wrongAccount := config
	wrongAccount.ConfirmAccountID = "33333333-3333-4333-8333-333333333333"
	if err := validateConfig(wrongAccount, logger); err == nil {
		t.Fatal("execution with mismatched Account confirmation was accepted")
	}
	shortKey := config
	shortKey.EvidenceKey = make([]byte, 31)
	if err := validateConfig(shortKey, logger); err == nil {
		t.Fatal("execution with a short evidence key was accepted")
	}
}

func TestValidateConfigRequiresRestoreReplayAuthorityAndExactTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{
		GlobalDatabaseURL: "postgres://global", CellDatabaseURL: "postgres://cell", CellID: "cell-a",
		Action: "restore-replay", Actor: "restore-operator@example.com", Reason: "replay archived erasure directive",
		Environment: "production", ConfirmEnvironment: "production",
		AccountID: "11111111-1111-4111-8111-111111111111", ConfirmAccountID: "11111111-1111-4111-8111-111111111111",
		RequestID: "22222222-2222-4222-8222-222222222222", RestoreSigningKey: make([]byte, 32),
		RestoreDirectiveFile: "reviewed-directive.json",
	}
	if err := validateConfig(config, logger); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing cell database": func(value *Config) { value.CellDatabaseURL = "" },
		"wrong Account":         func(value *Config) { value.ConfirmAccountID = "33333333-3333-4333-8333-333333333333" },
		"short signing key":     func(value *Config) { value.RestoreSigningKey = make([]byte, 31) },
		"missing directive":     func(value *Config) { value.RestoreDirectiveFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := config
			mutate(&invalid)
			if err := validateConfig(invalid, logger); err == nil {
				t.Fatal("invalid restore replay configuration was accepted")
			}
		})
	}
}

func TestReadRestoreDirectiveRejectsUnknownAndTrailingJSON(t *testing.T) {
	directory := t.TempDir()
	for name, contents := range map[string][]byte{
		"unknown":  []byte(`{"directive":{},"signature":"","unexpected":true}`),
		"trailing": []byte(`{"directive":{},"signature":""} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name+".json")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRestoreDirective(path); err == nil {
				t.Fatal("malformed restore directive file was accepted")
			}
		})
	}
	validPath := filepath.Join(directory, "valid.json")
	raw, err := json.Marshal(accounterasure.SignedRestoreDirective{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRestoreDirective(validPath); err != nil {
		t.Fatalf("strict JSON envelope was rejected: %v", err)
	}
}
