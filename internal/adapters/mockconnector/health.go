package mockconnector

import (
	"context"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

// HealthProbe is deterministic local-only plumbing. Production composition
// always replaces it with the capability-specific provider adapter.
type HealthProbe struct{}

func (HealthProbe) Probe(_ context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if len(call.Credential) == 0 {
		return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: "mock_health_credential_unavailable"}
	}
	return integrationhealth.ProbeResult{State: domain.HealthHealthy}
}

var _ integrationhealth.Probe = HealthProbe{}
