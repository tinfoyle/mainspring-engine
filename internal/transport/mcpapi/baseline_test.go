package mcpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type baselineMCPStub struct {
	start  baselineapp.StartCommand
	answer baselineapp.AnswerCommand
	now    time.Time
}

func (stub *baselineMCPStub) Start(_ context.Context, command baselineapp.StartCommand) (baselinedomain.Assessment, error) {
	stub.start = command
	return baselinedomain.NewAssessment(baselinedomain.AssessmentDraft{ID: command.AssessmentID, AccountID: command.AccountID, CatalogVersion: baselinedomain.EvidenceCatalogVersion, ScopePolicyVersion: baselinedomain.ScopePolicyVersion, CreatedBy: baselinedomain.Actor{UserID: command.Actor.UserID}}, stub.now)
}
func (*baselineMCPStub) Get(context.Context, access.Actor, ids.AccountID, ids.BaselineAssessmentID) (baselinedomain.Assessment, error) {
	return baselinedomain.Assessment{}, baselineapp.ErrNotFound
}
func (stub *baselineMCPStub) Answer(_ context.Context, command baselineapp.AnswerCommand) (baselinedomain.Assessment, error) {
	stub.answer = command
	assessment, err := baselinedomain.NewAssessment(baselinedomain.AssessmentDraft{ID: command.AssessmentID, AccountID: command.AccountID, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1", CreatedBy: baselinedomain.Actor{UserID: command.Actor.UserID}}, stub.now)
	if err != nil {
		return baselinedomain.Assessment{}, err
	}
	return assessment.AnswerInterview(baselinedomain.AnswerInterviewCommand{Answer: baselinedomain.InterviewAnswer{QuestionKey: command.QuestionKey, Kind: command.Kind, Fact: command.Fact, Reason: command.Reason, AnsweredBy: baselinedomain.Actor{UserID: command.Actor.UserID}, AnsweredAt: stub.now.Add(time.Minute)}, Role: "owner", ExpectedVersion: command.ExpectedVersion})
}
func (*baselineMCPStub) BeginInventory(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected BeginInventory")
}
func (*baselineMCPStub) CompleteInventory(context.Context, baselineapp.CompleteInventoryCommand) (baselinedomain.Assessment, error) {
	panic("unexpected CompleteInventory")
}
func (*baselineMCPStub) DecideEvidence(context.Context, baselineapp.DecideEvidenceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected DecideEvidence")
}
func (*baselineMCPStub) Disposition(context.Context, baselineapp.DispositionCommand) (baselinedomain.Assessment, error) {
	panic("unexpected Disposition")
}
func (*baselineMCPStub) SubmitPlan(context.Context, baselineapp.SubmitPlanCommand) (baselinedomain.Assessment, error) {
	panic("unexpected SubmitPlan")
}
func (*baselineMCPStub) ApprovePlan(context.Context, baselineapp.ApprovePlanCommand) (baselinedomain.Assessment, error) {
	panic("unexpected ApprovePlan")
}
func (*baselineMCPStub) MaterializePlan(context.Context, baselineapp.MaterializePlanCommand) ([]workdomain.Item, error) {
	panic("unexpected MaterializePlan")
}
func (*baselineMCPStub) MarkReady(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected MarkReady")
}
func (*baselineMCPStub) Reassess(context.Context, baselineapp.ReassessCommand) (baselinedomain.Assessment, baselinedomain.Assessment, error) {
	panic("unexpected Reassess")
}
func (*baselineMCPStub) GrantSource(context.Context, baselineapp.GrantSourceCommand) (baselinedomain.SourceGrant, error) {
	panic("unexpected GrantSource")
}
func (*baselineMCPStub) ListSourceGrants(context.Context, baselineapp.ListSourceGrantsQuery) (baselineapp.SourceGrantPage, error) {
	panic("unexpected ListSourceGrants")
}
func (*baselineMCPStub) RevokeSource(context.Context, baselineapp.RevokeSourceCommand) (baselinedomain.SourceGrant, error) {
	panic("unexpected RevokeSource")
}

func TestBaselineMCPUsesCanonicalAuthorizedService(t *testing.T) {
	authority := &testAuthority{}
	service := &baselineMCPStub{now: time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)}
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithBaseline(service))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "spyglass-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 20 {
		t.Fatalf("tools=%d err=%v", len(tools.Tools), err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_baseline_start", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, `"state":"interview"`) || service.start.AssessmentID != ids.BaselineAssessmentID(mcpOperation) {
		t.Fatalf("result=%+v start=%+v err=%v", result, service.start, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_baseline_mutate", Arguments: map[string]any{"account_id": mcpAccount, "operation_id": mcpOperation, "assessment_id": mcpWork, "expected_version": 1, "action": "answer", "question_key": "organization.legal_name", "answer_kind": "fact", "fact": map[string]any{"fact_id": mcpOperation, "revision": 2}}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, `"fact_id"`) || service.answer.Fact == nil || service.answer.Fact.Revision != 2 || len(authority.requirements) != 2 || authority.requirements[0].Package != "knowledge" || authority.requirements[1].Package != "knowledge" {
		t.Fatalf("result=%+v answer=%+v requirements=%+v err=%v", result, service.answer, authority.requirements, err)
	}
}

var _ BaselineService = (*baselineMCPStub)(nil)
