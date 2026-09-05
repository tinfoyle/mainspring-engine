package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
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
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	mcpAccount   = "10000000-0000-4000-8000-000000000001"
	mcpUser      = "20000000-0000-4000-8000-000000000002"
	mcpOperation = "30000000-0000-4000-8000-000000000003"
	mcpWork      = "40000000-0000-4000-8000-000000000004"
)

func TestAttentionMCPPublishesTypedDeterministicToolSurface(t *testing.T) {
	authority := &testAuthority{}
	session, cleanup := connectMCP(t, authority, &attentionStub{})
	defer cleanup()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 15 {
		t.Fatalf("tool count=%d", len(result.Tools))
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if _, ok := ToolRequirement(tool.Name); !ok {
			t.Fatalf("tool %q has no global routing requirement", tool.Name)
		}
		if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatalf("incomplete tool contract: %+v", tool)
		}
		if tool.Name == "spyglass_attention_approval_create" {
			inputSchema, _ := json.Marshal(tool.InputSchema)
			outputSchema, _ := json.Marshal(tool.OutputSchema)
			if !strings.Contains(string(inputSchema), `"payload":{"type":"object"}`) || !strings.Contains(string(outputSchema), `"payload":{"type":"object"}`) {
				t.Fatalf("approval payload schemas are not JSON objects: input=%s output=%s", inputSchema, outputSchema)
			}
		}
	}
	if !slices.IsSorted(names) {
		t.Fatalf("tool order is not deterministic: %v", names)
	}
	if authority.authenticatedToken != "reviewed-token" {
		t.Fatalf("authenticated token=%q", authority.authenticatedToken)
	}
}

func TestAttentionMCPUsesSameRoutedServiceAndRedactsApprovalQueue(t *testing.T) {
	now := time.Date(2026, 8, 21, 19, 0, 0, 0, time.UTC)
	service := &attentionStub{}
	service.createInformation = func(ctx context.Context, command attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error) {
		claims, ok := routecontext.FromContext(ctx)
		if !ok || claims.Authority.AccountID != mcpAccount || claims.Authority.PackageAccess.Code != "work" || command.Actor.UserID != mcpUser || command.RequestID != mcpOperation {
			t.Fatalf("claims=%+v command=%+v", claims, command)
		}
		return mcpInformation(t, now), nil
	}
	service.listApprovals = func(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error) {
		return attentionapp.ApprovalSummaryPage{Items: []attentionapp.ApprovalSummary{{
			ID: mcpOperation, WorkItemID: mcpWork, OperationID: mcpOperation, InvocationID: "50000000-0000-4000-8000-000000000005",
			Capability: "email.send", Proposer: attentiondomain.Actor{Kind: attentiondomain.ActorWorkload, ID: "runner:test"}, PolicyVersion: 1,
			ExpiresAt: now.Add(time.Hour), State: attentiondomain.ConsequentialApprovalOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
		}}}, nil
	}
	authority := &testAuthority{}
	session, cleanup := connectMCP(t, authority, service)
	defer cleanup()

	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_information_create", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "parent_work_item_id": mcpWork,
		"requirement": map[string]any{"key": "customer.name", "scope": "account"}, "question": "What is the customer name?",
	}})
	if err != nil || created.IsError {
		t.Fatalf("create err=%v result=%+v", err, created)
	}
	queue, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_approval_list", Arguments: map[string]any{"account_id": mcpAccount}})
	if err != nil || queue.IsError {
		t.Fatalf("queue err=%v result=%+v", err, queue)
	}
	raw := queue.Content[0].(*mcp.TextContent).Text
	if strings.Contains(raw, "payload") || strings.Contains(raw, "sha256") || strings.Contains(raw, "reason") || !strings.Contains(raw, "email.send") {
		t.Fatalf("unsafe queue result: %s", raw)
	}
	if len(authority.requirements) != 2 || authority.requirements[0].Package != catalog.PackageWork || !authority.requirements[0].Mutation || authority.requirements[1].Package != catalog.PackageAgents || authority.requirements[1].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
}

func TestAttentionMCPMapsDomainFailureWithoutLeakingBackendDetail(t *testing.T) {
	service := &attentionStub{decideReview: func(context.Context, attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error) {
		return attentiondomain.WorkReview{}, fmt.Errorf("database host secret should never cross MCP: %w", attentionapp.ErrConflict)
	}}
	session, cleanup := connectMCP(t, &testAuthority{}, service)
	defer cleanup()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_review_decide", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "review_id": mcpWork, "expected_version": 1, "decision": "approve", "reason": "Proposal is correct",
	}})
	if err != nil || !result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	raw := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(raw, "attention_version_conflict") || strings.Contains(raw, "database") || strings.Contains(raw, "secret") {
		t.Fatalf("unsafe error=%q", raw)
	}
}

func TestAttentionMCPPreservesCanonicalApprovalNumberLexemes(t *testing.T) {
	now := time.Date(2026, 8, 21, 19, 0, 0, 0, time.UTC)
	var captured string
	service := &attentionStub{createApproval: func(_ context.Context, command attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
		captured = string(command.CanonicalPayload)
		item, err := attentiondomain.NewConsequentialApproval(attentiondomain.ConsequentialApprovalDraft{
			ID: command.ApprovalID, AccountID: command.AccountID, OperationID: command.OperationID, InvocationID: command.InvocationID,
			Capability: command.Capability, CanonicalPayload: command.CanonicalPayload, EvidenceSHA256: command.EvidenceSHA256,
			Proposer: attentiondomain.Actor{Kind: attentiondomain.ActorUser, ID: mcpUser}, PolicyVersion: command.PolicyVersion, ExpiresAt: command.ExpiresAt,
		}, now)
		return item, err
	}}
	session, cleanup := connectMCP(t, &testAuthority{}, service)
	defer cleanup()
	rawArguments := json.RawMessage(`{"account_id":"` + mcpAccount + `","idempotency_key":"` + mcpOperation + `","operation_id":"50000000-0000-4000-8000-000000000005","invocation_id":"60000000-0000-4000-8000-000000000006","capability":"finance.payment.create","payload":{"amount":1.2300,"memo":"exact"},"evidence_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","policy_version":1,"require_independent_review":false,"expires_at":"2026-08-21T20:00:00Z"}`)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_attention_approval_create", Arguments: rawArguments})
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if captured != `{"amount":1.2300,"memo":"exact"}` {
		t.Fatalf("canonical input lexeme changed before domain boundary: %s", captured)
	}
}

func TestAttentionMCPRejectsCookiesUntrustedOriginsAndInvalidBearer(t *testing.T) {
	server := newMCPServer(t, &testAuthority{}, &attentionStub{})
	for _, test := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{name: "missing bearer", headers: map[string]string{}, status: http.StatusUnauthorized},
		{name: "cookie", headers: map[string]string{"Authorization": "Bearer reviewed-token", "Cookie": "session=opaque"}, status: http.StatusBadRequest},
		{name: "origin", headers: map[string]string{"Authorization": "Bearer reviewed-token", "Origin": "https://attacker.example"}, status: http.StatusForbidden},
		{name: "malformed bearer", headers: map[string]string{"Authorization": "Bearer token with spaces"}, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://mcp.infiniteocean.net/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusUnauthorized && !strings.Contains(response.Header().Get("WWW-Authenticate"), "oauth-protected-resource") {
				t.Fatalf("challenge=%q", response.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func connectMCP(t *testing.T, authority Authority, service AttentionService) (*mcp.ClientSession, func()) {
	t.Helper()
	server := httptest.NewServer(newMCPServer(t, authority, service).Handler())
	transport := &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "spyglass-test", Version: "1.0.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return session, func() { _ = session.Close(); server.Close() }
}

func newMCPServer(t *testing.T, authority Authority, service AttentionService) *Server {
	t.Helper()
	server, err := New(authority, service, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type bearerRoundTripper struct{ base http.RoundTripper }

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer reviewed-token")
	return transport.base.RoundTrip(clone)
}

type testAuthority struct {
	authenticatedToken    string
	requirements          []access.Requirement
	strongAuthenticatedAt *time.Time
}

func (a *testAuthority) Authenticate(_ context.Context, token string) (access.Actor, error) {
	a.authenticatedToken = token
	if token != "reviewed-token" {
		return access.Actor{}, errors.New("invalid")
	}
	return access.Actor{UserID: mcpUser}, nil
}
func (a *testAuthority) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (routecontext.Claims, error) {
	a.requirements = append(a.requirements, requirement)
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: accountID, ActorKind: "user", ActorID: string(actor.UserID), Role: "owner", StrongAuthenticatedAt: a.strongAuthenticatedAt, CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: string(requirement.Package), Version: 1, Mode: "enabled"}}}, nil
}

func mcpInformation(t *testing.T, now time.Time) attentiondomain.InformationRequest {
	t.Helper()
	item, err := attentiondomain.NewInformationRequest(attentiondomain.InformationRequestDraft{ID: mcpOperation, AccountID: mcpAccount, ParentWorkItemID: mcpWork, Requirement: attentiondomain.FactRequirement{Key: "customer.name", Scope: attentiondomain.InformationScopeAccount}, Question: "What is the customer name?", RequestedBy: attentiondomain.Actor{Kind: attentiondomain.ActorUser, ID: mcpUser}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

type attentionStub struct {
	createInformation func(context.Context, attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error)
	createApproval    func(context.Context, attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	listApprovals     func(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error)
	decideReview      func(context.Context, attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error)
}

func (s *attentionStub) CreateInformation(ctx context.Context, command attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error) {
	if s.createInformation != nil {
		return s.createInformation(ctx, command)
	}
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionStub) AnswerInformation(context.Context, attentionapp.AnswerInformationCommand) (attentionapp.InformationCompletion, error) {
	return attentionapp.InformationCompletion{}, nil
}
func (*attentionStub) CancelInformation(context.Context, attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error) {
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionStub) GetInformation(context.Context, access.Actor, ids.AccountID, ids.InformationRequestID) (attentiondomain.InformationRequest, error) {
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionStub) ListInformation(context.Context, access.Actor, ids.AccountID, attentionapp.InformationListQuery) (attentionapp.InformationSummaryPage, error) {
	return attentionapp.InformationSummaryPage{}, nil
}
func (*attentionStub) CreateWorkReview(context.Context, attentionapp.CreateWorkReviewCommand) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (s *attentionStub) DecideWorkReview(ctx context.Context, command attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error) {
	if s.decideReview != nil {
		return s.decideReview(ctx, command)
	}
	return attentiondomain.WorkReview{}, nil
}
func (*attentionStub) CancelWorkReview(context.Context, attentionapp.CancelWorkReviewCommand) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionStub) GetWorkReview(context.Context, access.Actor, ids.AccountID, ids.WorkReviewID) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionStub) ListWorkReviews(context.Context, access.Actor, ids.AccountID, attentionapp.WorkReviewListQuery) (attentionapp.WorkReviewSummaryPage, error) {
	return attentionapp.WorkReviewSummaryPage{}, nil
}
func (s *attentionStub) CreateApproval(ctx context.Context, command attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	if s.createApproval != nil {
		return s.createApproval(ctx, command)
	}
	return attentiondomain.ConsequentialApproval{}, nil
}
func (*attentionStub) DecideApproval(context.Context, attentionapp.DecideApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (*attentionStub) CancelApproval(context.Context, attentionapp.CancelApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (*attentionStub) GetApproval(context.Context, access.Actor, ids.AccountID, ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (s *attentionStub) ListApprovals(ctx context.Context, actor access.Actor, accountID ids.AccountID, query attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error) {
	if s.listApprovals != nil {
		return s.listApprovals(ctx, actor, accountID, query)
	}
	return attentionapp.ApprovalSummaryPage{}, nil
}

var _ AttentionService = (*attentionStub)(nil)

func TestRecurringReportMCPToolsPublishValidSchemas(t *testing.T) {
	server := newMCPServer(t, &testAuthority{}, &attentionStub{})
	server.scheduling = &scheduleapp.Service{}
	server.agents = &agentapp.Service{}
	endpoint := httptest.NewServer(server.Handler())
	defer endpoint.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schedule-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 29 {
		t.Fatalf("tool count=%d", len(result.Tools))
	}
	for _, tool := range result.Tools {
		if _, ok := ToolRequirement(tool.Name); !ok {
			t.Fatalf("unroutable tool %s", tool.Name)
		}
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("untyped tool %s", tool.Name)
		}
		if tool.Name == "spyglass_schedule_create" {
			schema, _ := json.Marshal(tool.InputSchema)
			for _, field := range []string{"email_self", "source_urls", "operation_id"} {
				if !strings.Contains(string(schema), field) {
					t.Fatalf("missing %s", field)
				}
			}
		}
	}
}
