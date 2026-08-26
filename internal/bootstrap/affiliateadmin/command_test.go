package affiliateadmin

import (
	"io"
	"log/slog"
	"testing"
)

func TestValidateConfigRequiresExactEnvironmentAndVersion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := Config{DatabaseURL: "postgres://example", Action: "suspend", Actor: "operator@example.test",
		Reason: "Suspend during a documented review", Environment: "local", ConfirmEnvironment: "local",
		AffiliateID: "10000000-0000-4000-8000-000000000001", ExpectedVersion: 2}
	if err := validateConfig(base, logger); err != nil {
		t.Fatal(err)
	}
	base.ConfirmEnvironment = "staging"
	if err := validateConfig(base, logger); err == nil {
		t.Fatal("mismatched environment was accepted")
	}
}

func TestValidateConfigAcceptsRiskInspectionWithoutVersion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{DatabaseURL: "postgres://example", Action: "inspect-risk", Actor: "operator@example.test",
		Reason: "Review aggregate referral risk evidence", Environment: "local", ConfirmEnvironment: "local",
		AffiliateID: "10000000-0000-4000-8000-000000000001"}
	if err := validateConfig(config, logger); err != nil {
		t.Fatal(err)
	}
	config.ExpectedVersion = 2
	if err := validateConfig(config, logger); err == nil {
		t.Fatal("risk inspection accepted a mutation version")
	}
}
