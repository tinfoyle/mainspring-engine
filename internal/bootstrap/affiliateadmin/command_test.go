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

func TestValidateConfigAcceptsVersionedCheckThresholdWithoutAffiliateTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := Config{DatabaseURL: "postgres://example", Action: "set-check-threshold", Actor: "support@example.test",
		Reason: "Adjust the reviewed check eligibility threshold", Environment: "local", ConfirmEnvironment: "local",
		ExpectedPolicyVersion: 1, NewPolicyVersion: 2, CheckThresholdMinor: 25_000}
	if err := validateConfig(config, logger); err != nil {
		t.Fatal(err)
	}
	config.NewPolicyVersion = 3
	if err := validateConfig(config, logger); err == nil {
		t.Fatal("skipped policy version was accepted")
	}
}

func TestValidateConfigRequiresExactSupportCheckInputs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reserve := Config{DatabaseURL: "postgres://example", Action: "reserve-check", Actor: "support@example.test",
		Reason: "Reserve the reviewed Affiliate check amount", Environment: "local", ConfirmEnvironment: "local",
		AffiliateID:       "10000000-0000-4000-8000-000000000001",
		CustomerSessionID: "10000000-0000-4000-8000-000000000002", CheckAmountMinor: 10_000}
	if err := validateConfig(reserve, logger); err != nil {
		t.Fatal(err)
	}
	reserve.CustomerSessionID = ""
	if err := validateConfig(reserve, logger); err == nil {
		t.Fatal("Support check reservation accepted without customer passkey session evidence")
	}
	settle := Config{DatabaseURL: "postgres://example", Action: "settle-check", Actor: "support@example.test",
		Reason: "Record external check accounting completion", Environment: "local", ConfirmEnvironment: "local",
		CheckReservationID: "10000000-0000-4000-8000-000000000003", ExpectedVersion: 1}
	if err := validateConfig(settle, logger); err != nil {
		t.Fatal(err)
	}
	settle.AffiliateID = "10000000-0000-4000-8000-000000000001"
	if err := validateConfig(settle, logger); err == nil {
		t.Fatal("Support check transition accepted an unrelated Affiliate target")
	}
}
