package accountapi

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestAffiliateSettlementConfigurationFailsBeforeStartup(t *testing.T) {
	base := Config{
		DatabaseURL:                  "postgres://unused",
		StripeMode:                   "test",
		AppOrigin:                    "https://app.example.test",
		PublicOrigin:                 "https://www.example.test",
		AnalyticsHandoffCookieDomain: "example.test",
		MCPResourceOrigin:            "https://mcp.example.test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	unconfigured := base
	unconfigured.AffiliateEnrollmentOpen = true
	unconfigured.AffiliateSettlementMode = "unconfigured"
	if _, err := New(context.Background(), unconfigured, logger); err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("unconfigured enrollment error=%v", err)
	}
	unconfiguredAttribution := base
	unconfiguredAttribution.AffiliateAttributionEnabled = true
	unconfiguredAttribution.AffiliateSettlementMode = "unconfigured"
	if _, err := New(context.Background(), unconfiguredAttribution, logger); err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("unconfigured attribution error=%v", err)
	}

	unknown := base
	unknown.AffiliateSettlementMode = "future"
	if _, err := New(context.Background(), unknown, logger); err == nil || !strings.Contains(err.Error(), "settlement mode") {
		t.Fatalf("unknown settlement error=%v", err)
	}
}

func TestAnalyticsHandoffCookieDomainMustContainBothOrigins(t *testing.T) {
	if err := validateAnalyticsHandoffCookieDomain("example.test", "https://www.example.test", "https://app.example.test"); err != nil {
		t.Fatal(err)
	}
	invalid := []string{"", ".example.test", "evil.test", "example.test:443"}
	for _, domain := range invalid {
		if err := validateAnalyticsHandoffCookieDomain(domain, "https://www.example.test", "https://app.example.test"); err == nil {
			t.Fatalf("invalid handoff cookie domain %q was accepted", domain)
		}
	}
}
