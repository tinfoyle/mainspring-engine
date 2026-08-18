package accounterasureadmin

import (
	"io"
	"log/slog"
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
