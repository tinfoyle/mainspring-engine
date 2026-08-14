package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/webresearch"
)

type webProviderStub struct {
	search webresearch.SearchRequest
	read   webresearch.ReadRequest
}

func (stub *webProviderStub) Search(_ context.Context, request webresearch.SearchRequest) ([]webresearch.SearchResult, error) {
	stub.search = request
	return []webresearch.SearchResult{{CitationID: webresearch.CitationID("https://agency.example/rule"), Title: "Rule", URL: "https://agency.example/rule"}}, nil
}

func (stub *webProviderStub) Read(_ context.Context, request webresearch.ReadRequest) (webresearch.ReadResult, error) {
	stub.read = request
	return webresearch.ReadResult{CitationID: webresearch.CitationID(request.URL), URL: request.URL, Content: "evidence"}, nil
}

func webToolToken(t *testing.T, issuer *TokenIssuer, tenantID domain.TenantID, grants []domain.ToolGrant) string {
	t.Helper()
	token, err := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: grants, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestWebSearchEnforcesGrantBoundsAndReturnsCitations(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	provider := &webProviderStub{}
	broker := NewBroker(issuer, nil)
	if err := RegisterWebResearch(broker, provider); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	token := webToolToken(t, issuer, tenantID, []domain.ToolGrant{{Capability: domain.CapabilityWebSearch, Conditions: map[string]string{
		"max_results": "2", "allowed_domains": "agency.example",
	}}})
	output, err := broker.InvokeNamed(context.Background(), token, tenantID, WebSearchTool, json.RawMessage(`{"query":"compliance rule","limit":8}`))
	if err != nil {
		t.Fatal(err)
	}
	if provider.search.Limit != 2 || len(provider.search.IncludeDomains) != 1 || provider.search.IncludeDomains[0] != "agency.example" {
		t.Fatalf("grant bounds were not applied: %#v", provider.search)
	}
	var decoded struct {
		Results []webresearch.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil || len(decoded.Results) != 1 || decoded.Results[0].CitationID == "" {
		t.Fatalf("unexpected output %s: %v", output, err)
	}
}

func TestWebReadRequiresSeparateGrantAndBoundsContent(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	provider := &webProviderStub{}
	broker := NewBroker(issuer, nil)
	if err := RegisterWebResearch(broker, provider); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	searchOnly := webToolToken(t, issuer, tenantID, []domain.ToolGrant{{Capability: domain.CapabilityWebSearch}})
	if _, err := broker.InvokeNamed(context.Background(), searchOnly, tenantID, WebReadTool, json.RawMessage(`{"url":"https://agency.example/rule"}`)); err != ErrCapabilityDenied {
		t.Fatalf("web.read without grant error = %v", err)
	}
	readToken := webToolToken(t, issuer, tenantID, []domain.ToolGrant{{Capability: domain.CapabilityWebRead, Conditions: map[string]string{
		"max_characters": "750", "allowed_domains": "agency.example",
	}}})
	if _, err := broker.InvokeNamed(context.Background(), readToken, tenantID, WebReadTool, json.RawMessage(`{"url":"https://agency.example/rule","max_characters":12000}`)); err != nil {
		t.Fatal(err)
	}
	if provider.read.MaxCharacters != 750 {
		t.Fatalf("max characters = %d; want 750", provider.read.MaxCharacters)
	}
}

func TestWebToolsRejectUnexpectedFieldsAndDomainEscapes(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	broker := NewBroker(issuer, nil)
	_ = RegisterWebResearch(broker, &webProviderStub{})
	tenantID := domain.NewTenantID()
	token := webToolToken(t, issuer, tenantID, []domain.ToolGrant{{Capability: domain.CapabilityWebSearch, Conditions: map[string]string{"allowed_domains": "agency.example"}}})
	for _, input := range []string{
		`{"query":"rule","include_domains":["evil.example"]}`,
		`{"query":"rule","headers":{"Authorization":"secret"}}`,
	} {
		if _, err := broker.InvokeNamed(context.Background(), token, tenantID, WebSearchTool, json.RawMessage(input)); err == nil {
			t.Fatalf("expected input to be rejected: %s", input)
		}
	}
}
