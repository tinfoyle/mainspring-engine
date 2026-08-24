package mcpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	integrationauthorization "github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	mcpIntegrationConnection = "ca000000-0000-4000-8000-00000000000a"
	mcpIntegrationRevision   = "cb000000-0000-4000-8000-00000000000b"
	mcpIntegrationCredential = "cc000000-0000-4000-8000-00000000000c"
	mcpIntegrationExecution  = "cd000000-0000-4000-8000-00000000000d"
	mcpIntegrationRelease    = "ce000000-0000-4000-8000-00000000000e"
	mcpIntegrationApproval   = "cf000000-0000-4000-8000-00000000000f"
	mcpIntegrationAuth       = "d0000000-0000-4000-8000-000000000010"
)

type integrationAuthorizationMCPStub struct {
	now    time.Time
	begin  integrationauthorization.BeginCommand
	status ids.IntegrationAuthorizationSessionID
	revoke integrationauthorization.RevokeCommand
}

func (stub *integrationAuthorizationMCPStub) Begin(_ context.Context, command integrationauthorization.BeginCommand) (integrationauthorization.BeginResult, error) {
	stub.begin = command
	return integrationauthorization.BeginResult{Session: integrationsdomain.AuthorizationSession{
		ID: mcpIntegrationAuth, AccountID: mcpAccount, ConnectionID: mcpIntegrationConnection,
		Status: integrationsdomain.AuthorizationPending, CreatedAt: stub.now, UpdatedAt: stub.now, ExpiresAt: stub.now.Add(10 * time.Minute),
	}, AuthorizationURL: "https://accounts.example.test/authorize?opaque=1"}, nil
}

func (stub *integrationAuthorizationMCPStub) Status(_ context.Context, _ access.Actor, _ ids.AccountID, authorizationID ids.IntegrationAuthorizationSessionID) (integrationauthorization.AuthorizationSummary, error) {
	stub.status = authorizationID
	return integrationauthorization.AuthorizationSummary{
		ID: mcpIntegrationAuth, AccountID: mcpAccount, ConnectionID: mcpIntegrationConnection,
		Status: integrationsdomain.AuthorizationCompleted, CredentialID: mcpIntegrationCredential, CredentialGeneration: 2,
		CreatedAt: stub.now, UpdatedAt: stub.now.Add(time.Minute), ExpiresAt: stub.now.Add(10 * time.Minute),
	}, nil
}

func (stub *integrationAuthorizationMCPStub) Revoke(_ context.Context, command integrationauthorization.RevokeCommand) (integrationauthorization.RevocationWorkflow, error) {
	stub.revoke = command
	return integrationauthorization.RevocationWorkflow{
		ID: "d1000000-0000-4000-8000-000000000011", AccountID: mcpAccount, ConnectionID: mcpIntegrationConnection,
		CredentialID: mcpIntegrationCredential, CredentialGeneration: 2, State: integrationauthorization.RevocationCompleted,
		CreatedAt: stub.now, UpdatedAt: stub.now.Add(time.Minute),
	}, nil
}

type integrationMCPStub struct {
	now               time.Time
	create            integrationsapp.CreateConnectionCommand
	credential        integrationsapp.CredentialCommand
	executionQry      integrationsapp.ExecutionListQuery
	resolutionRequest integrationsapp.RequestExecutionResolutionCommand
	resolutionConfirm integrationsapp.ConfirmExecutionResolutionCommand
	reviseError       error
}

func (stub *integrationMCPStub) connection() integrationsdomain.Connection {
	return integrationsdomain.Connection{ID: mcpIntegrationConnection, AccountID: mcpAccount, Name: "Campaign email", Kind: integrationsdomain.ConnectorEmail,
		State: integrationsdomain.ConnectionActive, CurrentRevisionID: mcpIntegrationRevision, CurrentRevision: 1, CredentialID: mcpIntegrationCredential,
		CredentialGeneration: 1, Version: 2, CreatedBy: integrationsdomain.Actor{UserID: mcpUser}, CreatedAt: stub.now.Add(-time.Hour), UpdatedAt: stub.now}
}
func (stub *integrationMCPStub) revision() integrationsdomain.ConnectionRevision {
	return integrationsdomain.ConnectionRevision{ID: mcpIntegrationRevision, AccountID: mcpAccount, ConnectionID: mcpIntegrationConnection, Revision: 1,
		Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityEmailSend}, Scope: integrationsdomain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:v1"}, CreatedBy: integrationsdomain.Actor{UserID: mcpUser}, CreatedAt: stub.now}
}
func (stub *integrationMCPStub) execution() integrationsdomain.Execution {
	return integrationsdomain.Execution{ID: mcpIntegrationExecution, AccountID: mcpAccount, ReleaseID: mcpIntegrationRelease, ReleaseVersion: 1,
		ApprovalID: mcpIntegrationApproval, Capability: integrationsdomain.CapabilityEmailSend, ConnectionID: mcpIntegrationConnection,
		ConnectionRevisionID: mcpIntegrationRevision, ConnectionRevision: 1, CredentialID: mcpIntegrationCredential, CredentialGeneration: 1,
		PayloadSHA256: sha256.Sum256([]byte("manifest")), State: integrationsdomain.ExecutionPrepared, CreatedAt: stub.now, UpdatedAt: stub.now}
}
func (stub *integrationMCPStub) CreateConnection(_ context.Context, command integrationsapp.CreateConnectionCommand) (integrationsdomain.Connection, bool, error) {
	stub.create = command
	return stub.connection(), true, nil
}
func (stub *integrationMCPStub) GetConnectionDetail(context.Context, access.Actor, ids.AccountID, ids.IntegrationConnectionID) (integrationsapp.ConnectionDetail, error) {
	return integrationsapp.ConnectionDetail{Connection: stub.connection(), Revision: stub.revision()}, nil
}
func (stub *integrationMCPStub) ListConnections(context.Context, access.Actor, ids.AccountID, integrationsapp.ConnectionListQuery) (integrationsapp.ConnectionPage, error) {
	value := stub.connection()
	return integrationsapp.ConnectionPage{Items: []integrationsdomain.Connection{value}, NextCursor: &integrationsapp.ConnectionCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}
func (stub *integrationMCPStub) ReviseConnection(context.Context, integrationsapp.ReviseConnectionCommand) (integrationsdomain.Connection, error) {
	return stub.connection(), stub.reviseError
}
func (stub *integrationMCPStub) ActivateConnection(_ context.Context, command integrationsapp.CredentialCommand) (integrationsdomain.Connection, error) {
	stub.credential = command
	return stub.connection(), nil
}
func (stub *integrationMCPStub) RotateCredential(_ context.Context, command integrationsapp.CredentialCommand) (integrationsdomain.Connection, error) {
	stub.credential = command
	return stub.connection(), nil
}
func (stub *integrationMCPStub) DisableConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	return stub.connection(), nil
}
func (stub *integrationMCPStub) EnableConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	return stub.connection(), nil
}
func (stub *integrationMCPStub) RevokeConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	return stub.connection(), nil
}
func (stub *integrationMCPStub) ListHealth(context.Context, access.Actor, ids.AccountID, integrationsapp.HealthListQuery) (integrationsapp.HealthPage, error) {
	return integrationsapp.HealthPage{}, nil
}
func (stub *integrationMCPStub) PrepareExecution(context.Context, integrationsapp.PrepareExecutionCommand) (integrationsdomain.Execution, bool, error) {
	return stub.execution(), true, nil
}
func (stub *integrationMCPStub) GetExecution(context.Context, access.Actor, ids.AccountID, ids.IntegrationExecutionID) (integrationsapp.ExecutionDetail, error) {
	return integrationsapp.ExecutionDetail{Execution: stub.execution(), Attempts: []integrationsdomain.Attempt{}}, nil
}
func (stub *integrationMCPStub) ListExecutions(_ context.Context, _ access.Actor, _ ids.AccountID, query integrationsapp.ExecutionListQuery) (integrationsapp.ExecutionPage, error) {
	stub.executionQry = query
	value := stub.execution()
	return integrationsapp.ExecutionPage{Items: []integrationsdomain.Execution{value}, NextCursor: &integrationsapp.ExecutionCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}
func (stub *integrationMCPStub) RequestExecutionResolution(_ context.Context, command integrationsapp.RequestExecutionResolutionCommand) (integrationsapp.ExecutionDetail, error) {
	stub.resolutionRequest = command
	return integrationsapp.ExecutionDetail{Execution: stub.execution()}, nil
}
func (stub *integrationMCPStub) ConfirmExecutionResolution(_ context.Context, command integrationsapp.ConfirmExecutionResolutionCommand) (integrationsapp.ExecutionDetail, error) {
	stub.resolutionConfirm = command
	return integrationsapp.ExecutionDetail{Execution: stub.execution()}, nil
}

func TestIntegrationsMCPPublishesClassifiedCompleteTools(t *testing.T) {
	stub := &integrationMCPStub{now: time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)}
	session, cleanup := connectIntegrationMCP(t, &testAuthority{}, stub)
	defer cleanup()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names, count := make([]string, 0, len(result.Tools)), 0
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if strings.HasPrefix(tool.Name, "spyglass_integrations_") {
			count++
			requirement, ok := ToolRequirement(tool.Name)
			if !ok || requirement.Package != catalog.PackageIntegrations || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
				t.Fatalf("tool=%+v requirement=%+v", tool, requirement)
			}
		}
	}
	if count != 15 || !slices.IsSorted(names) {
		t.Fatalf("count=%d sorted=%v names=%v", count, slices.IsSorted(names), names)
	}
}

func TestIntegrationsMCPAuthorizationLifecycleRequiresStrongEvidenceAndStaysSecretFree(t *testing.T) {
	now := time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)
	integrations := &integrationMCPStub{now: now}
	authorizations := &integrationAuthorizationMCPStub{now: now}
	authority := &testAuthority{strongAuthenticatedAt: &now}
	session, cleanup := connectIntegrationMCPWithAuthorization(t, authority, integrations, authorizations)
	defer cleanup()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	integrationCount := 0
	for _, name := range names {
		if strings.HasPrefix(name, "spyglass_integrations_") {
			integrationCount++
		}
	}
	if integrationCount != 18 || slices.Contains(names, "spyglass_integrations_authorization_callback") {
		t.Fatalf("tools=%v", names)
	}

	begun, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_authorization_begin", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "redirect_uri": "https://app.example.test/callback",
	}})
	if err != nil || begun.IsError || authorizations.begin.Session.ReauthenticationMethod != "passkey" || !authorizations.begin.Session.ReauthenticatedAt.Equal(now) {
		t.Fatalf("begun=%+v err=%v command=%+v", begun, err, authorizations.begin)
	}
	assertSecretFreeMCPResult(t, begun)

	status, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_authorization_status", Arguments: map[string]any{
		"account_id": mcpAccount, "authorization_id": mcpIntegrationAuth,
	}})
	if err != nil || status.IsError || authorizations.status != mcpIntegrationAuth {
		t.Fatalf("status=%+v err=%v authorization=%q", status, err, authorizations.status)
	}
	assertSecretFreeMCPResult(t, status)

	revoked, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_credential_revoke", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection,
	}})
	if err != nil || revoked.IsError || authorizations.revoke.Session.ReauthenticationMethod != "passkey" || !authorizations.revoke.Session.ReauthenticatedAt.Equal(now) {
		t.Fatalf("revoked=%+v err=%v command=%+v", revoked, err, authorizations.revoke)
	}
	assertSecretFreeMCPResult(t, revoked)
}

func assertSecretFreeMCPResult(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	raw := result.Content[0].(*mcp.TextContent).Text
	for _, forbidden := range []string{"state_sha256", "pkce", "scope", "redirect_uri", "reference_sha256", "refresh_token", "authorization_code"} {
		if strings.Contains(strings.ToLower(raw), forbidden) {
			t.Fatalf("unsafe authorization output contains %q: %s", forbidden, raw)
		}
	}
}

func TestIntegrationsMCPBindsMutationDigestAndOpaqueExecutionOutput(t *testing.T) {
	stub := &integrationMCPStub{now: time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC)}
	authority := &testAuthority{}
	session, cleanup := connectIntegrationMCP(t, authority, stub)
	defer cleanup()
	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_connection_create", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "name": "Campaign email", "kind": "email", "capabilities": []any{"email.send"}, "scope": map[string]any{"email_address": "launch@example.com", "audience_reference": "audience:v1"}}})
	if err != nil || created.IsError || stub.create.RequestID != mcpOperation {
		t.Fatalf("created=%+v err=%v command=%+v", created, err, stub.create)
	}
	driveCreated, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_connection_create", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": "d1000000-0000-4000-8000-000000000001", "name": "Baseline folders", "kind": "google_drive",
		"capabilities": []any{"google_drive.read"}, "scope": map[string]any{"drive_folder_ids": []any{"folder-a", "folder_0"}},
	}})
	if err != nil || driveCreated.IsError || stub.create.Kind != integrationsdomain.ConnectorGoogleDrive || len(stub.create.Scope.DriveFolderIDs) != 2 {
		t.Fatalf("Drive created=%+v err=%v command=%+v", driveCreated, err, stub.create)
	}
	bound, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_credential_activate", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "expected_version": 1, "provider": "smtp_primary", "reference_sha256": strings.Repeat("11", 32)}})
	if err != nil || bound.IsError || stub.credential.ReferenceSHA256[0] != 0x11 {
		t.Fatalf("bound=%+v err=%v command=%+v", bound, err, stub.credential)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_execution_list", Arguments: map[string]any{"account_id": mcpAccount, "connection_id": mcpIntegrationConnection, "states": []any{"prepared"}, "capabilities": []any{"email.send"}, "limit": 25}})
	if err != nil || listed.IsError || stub.executionQry.Limit != 25 {
		t.Fatalf("listed=%+v err=%v query=%+v", listed, err, stub.executionQry)
	}
	raw := listed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, `"payload_sha256":"`) || !strings.Contains(raw, `"next_cursor":"`) || strings.Contains(raw, "reference_sha256") || strings.Contains(raw, "provider_payload") {
		t.Fatalf("unsafe output=%s", raw)
	}
	requested, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_execution_request_resolution", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "execution_id": mcpIntegrationExecution, "requested_outcome": "succeeded", "evidence": "provider receipt 123"}})
	if err != nil || requested.IsError || stub.resolutionRequest.RequestedOutcome != integrationsdomain.ExecutionSucceeded {
		t.Fatalf("requested=%+v err=%v command=%+v", requested, err, stub.resolutionRequest)
	}
	confirmed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_execution_confirm_resolution", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "execution_id": mcpIntegrationExecution, "resolution_id": mcpOperation}})
	if err != nil || confirmed.IsError || stub.resolutionConfirm.ResolutionID != mcpOperation {
		t.Fatalf("confirmed=%+v err=%v command=%+v", confirmed, err, stub.resolutionConfirm)
	}
	if len(authority.requirements) != 6 || !authority.requirements[0].Mutation || !authority.requirements[1].Mutation || !authority.requirements[2].Mutation || authority.requirements[3].Mutation || !authority.requirements[4].Mutation || !authority.requirements[5].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
}

func TestIntegrationsMCPRedactsBackendFailuresAndRequiresVersion(t *testing.T) {
	stub := &integrationMCPStub{now: time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC), reviseError: fmt.Errorf("database secret: %w", integrationsapp.ErrConflict)}
	session, cleanup := connectIntegrationMCP(t, &testAuthority{}, stub)
	defer cleanup()
	missing, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_connection_revise", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "expected_version": 0, "name": "v2", "capabilities": []any{"email.send"}, "scope": map[string]any{"email_address": "launch@example.com", "audience_reference": "audience:v2"}}})
	if err != nil || !missing.IsError || !strings.Contains(missing.Content[0].(*mcp.TextContent).Text, "integration_version_required") {
		t.Fatalf("missing=%+v err=%v", missing, err)
	}
	failed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_connection_revise", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "expected_version": 2, "name": "v2", "capabilities": []any{"email.send"}, "scope": map[string]any{"email_address": "launch@example.com", "audience_reference": "audience:v2"}}})
	if err != nil || !failed.IsError {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	raw := failed.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, "integration_version_conflict") || strings.Contains(raw, "database") || strings.Contains(raw, "secret") {
		t.Fatalf("unsafe=%s", raw)
	}
}

func connectIntegrationMCP(t *testing.T, authority Authority, integrations IntegrationsService) (*mcp.ClientSession, func()) {
	return connectIntegrationMCPWithAuthorization(t, authority, integrations, nil)
}

func connectIntegrationMCPWithAuthorization(t *testing.T, authority Authority, integrations IntegrationsService, authorization IntegrationAuthorizationService) (*mcp.ClientSession, func()) {
	t.Helper()
	options := []Option{WithIntegrations(integrations)}
	if authorization != nil {
		options = append(options, WithIntegrationAuthorization(authorization))
	}
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, options...)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, MaxRetries: -1, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		httpServer.Close()
		t.Fatal(err)
	}
	return session, func() { _ = session.Close(); httpServer.Close() }
}

var _ IntegrationsService = (*integrationMCPStub)(nil)
var _ IntegrationAuthorizationService = (*integrationAuthorizationMCPStub)(nil)
