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
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type knowledgeMCPStub struct{ query knowledgeapp.FactListQuery }

func (*knowledgeMCPStub) RegisterEvidence(context.Context, knowledgeapp.RegisterEvidenceCommand) (knowledgedomain.Evidence, error) {
	return knowledgedomain.Evidence{}, nil
}
func (*knowledgeMCPStub) ProposeClaim(context.Context, knowledgeapp.ProposeClaimCommand) (knowledgedomain.Claim, error) {
	return knowledgedomain.Claim{}, nil
}
func (*knowledgeMCPStub) DecideClaim(context.Context, knowledgeapp.DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
	return knowledgedomain.Claim{}, nil, nil
}
func (*knowledgeMCPStub) GetClaim(context.Context, access.Actor, ids.AccountID, ids.KnowledgeClaimID) (knowledgedomain.Claim, error) {
	return knowledgedomain.Claim{}, nil
}
func (stub *knowledgeMCPStub) ListFacts(_ context.Context, _ access.Actor, _ ids.AccountID, query knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error) {
	stub.query = query
	now := time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC)
	return knowledgeapp.FactPage{Items: []knowledgeapp.FactSummary{{ID: mcpWork, CurrentClaimID: mcpOperation, Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: "organization.name", Sensitivity: knowledgedomain.SensitivityInternal, State: knowledgedomain.FactActive, Revision: 1, AcceptedAt: now, UpdatedAt: now}}}, nil
}

func TestKnowledgeMCPPublishesBoundedSensitivityAwareSurface(t *testing.T) {
	authority := &testAuthority{}
	knowledge := &knowledgeMCPStub{}
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test", MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"}, ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithKnowledge(knowledge))
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
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_knowledge_fact_list", Arguments: map[string]any{"account_id": mcpAccount, "scope": map[string]any{"kind": "account"}, "key_prefix": "organization.", "limit": 5}})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "organization.name") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if knowledge.query.Scope == nil || knowledge.query.Scope.Kind != knowledgedomain.ScopeAccount || knowledge.query.Limit != 5 || len(authority.requirements) != 1 || authority.requirements[0].Package != "knowledge" {
		t.Fatalf("query=%+v requirements=%+v", knowledge.query, authority.requirements)
	}
}

var _ KnowledgeService = (*knowledgeMCPStub)(nil)
