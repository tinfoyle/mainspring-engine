package mcpapi

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestActionRecoveryMCPPublishesRedactedDualControlSurface(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("operator reviewed provider evidence"))
	service := &actionRecoveryStub{detail: actionrecovery.Detail{
		Summary:    actionrecovery.Summary{OperationID: mcpOperation, ApprovalID: "60000000-0000-4000-8000-000000000006", InvocationID: "50000000-0000-4000-8000-000000000005", Capability: "stripe.customer.create", ExecutorID: "stripe", ExecutorVersion: 1, PolicyVersion: 1, State: actionrecovery.StateManualResolution, AttemptCount: 2, LastErrorCode: "provider_timeout", StartedAt: now, UpdatedAt: now},
		Resolution: &actionrecovery.Resolution{ID: mcpWork, OperationID: mcpOperation, RequestedOutcome: actionrecovery.StateSucceeded, ReasonSHA256: digest, RequestedByUserID: mcpUser, RequestedAt: now, State: "pending"},
	}}
	authority := &testAuthority{}
	session, cleanup := connectActionMCP(t, authority, service)
	defer cleanup()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 19 {
		t.Fatalf("tool count=%d", len(tools.Tools))
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_action_get", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation}})
	if err != nil || result.IsError {
		t.Fatalf("get err=%v result=%+v", err, result)
	}
	raw := result.Content[0].(*mcp.TextContent).Text
	if strings.Contains(raw, "payload") || strings.Contains(raw, "provider_response") || strings.Contains(raw, "operator reviewed") || !strings.Contains(raw, "reason_sha256") {
		t.Fatalf("unsafe action detail: %s", raw)
	}

	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_action_request_resolution", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "resolution_id": mcpWork, "outcome": "succeeded", "reason": "operator reviewed provider evidence"}})
	if err != nil {
		t.Fatal(err)
	}
	if service.request.Reason != "operator reviewed provider evidence" || service.request.ResolutionID != mcpWork {
		t.Fatalf("request=%+v", service.request)
	}
	if len(authority.requirements) != 2 || authority.requirements[0].Mutation || !authority.requirements[1].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
	for _, requirement := range authority.requirements {
		if requirement.Package != catalog.PackageAgents || len(requirement.Roles) != 2 || requirement.Roles[0] != accounts.RoleOwner || requirement.Roles[1] != accounts.RoleAdministrator {
			t.Fatalf("requirement=%+v", requirement)
		}
	}
}

func connectActionMCP(t *testing.T, authority Authority, actions ActionRecoveryService) (*mcp.ClientSession, func()) {
	t.Helper()
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithActionRecovery(actions))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "spyglass-test", Version: "1.0.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	return session, func() { _ = session.Close(); httpServer.Close() }
}

type actionRecoveryStub struct {
	detail  actionrecovery.Detail
	request actionrecovery.RequestCommand
}

func (s *actionRecoveryStub) List(context.Context, access.Actor, ids.AccountID, actionrecovery.ListQuery) (actionrecovery.Page, error) {
	return actionrecovery.Page{Items: []actionrecovery.Summary{s.detail.Summary}}, nil
}
func (s *actionRecoveryStub) Get(context.Context, access.Actor, ids.AccountID, string) (actionrecovery.Detail, error) {
	return s.detail, nil
}
func (s *actionRecoveryStub) Request(_ context.Context, command actionrecovery.RequestCommand) (actionrecovery.Detail, error) {
	s.request = command
	return s.detail, nil
}
func (s *actionRecoveryStub) Confirm(context.Context, actionrecovery.ConfirmCommand) (actionrecovery.Detail, error) {
	return s.detail, nil
}

var _ ActionRecoveryService = (*actionRecoveryStub)(nil)
