package affiliatesupportadmin

import (
	"io"
	"log/slog"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
)

func TestValidateConfigRequiresDecisionOnlyForResolution(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := Config{DatabaseURL: "postgres://example", Action: "resolve", Actor: "operator@example.test",
		Reason: "Approve after evidence review", Environment: "local", ConfirmEnvironment: "local",
		RequestID: "20000000-0000-4000-8000-000000000001", ExpectedVersion: 2, Outcome: affiliates.SupportApproved}
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	base.Action, base.Outcome = "start-review", ""
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	base.Outcome = affiliates.SupportDenied
	if err := validateConfig(base, logger); err == nil {
		t.Fatal("start-review accepted an outcome")
	}
}
