package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
)

type documentSearcherStub struct {
	limit       int
	documentIDs []string
}

func (stub *documentSearcherStub) Search(_ context.Context, query string, limit int, documentIDs []string) ([]rag.SearchResult, error) {
	stub.limit = limit
	stub.documentIDs = documentIDs
	return []rag.SearchResult{{DocumentID: "251b403a-1fca-4d6c-aef9-cd625c32358d", DocumentName: "Policy.txt", ChunkIndex: 2, Content: query, Rank: 0.9}}, nil
}

func TestDocumentSearchUsesReadGrantAndReturnsCitation(t *testing.T) {
	issuer, err := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(issuer, nil)
	searcher := &documentSearcherStub{}
	if err := RegisterDocumentSearch(broker, searcher); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	token, err := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}}, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"retention policy","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Results []DocumentSearchResult `json:"results"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 || decoded.Results[0].CitationID != "doc:251b403a-1fca-4d6c-aef9-cd625c32358d:chunk:2" {
		t.Fatalf("unexpected search results: %#v", decoded.Results)
	}
}

func TestDocumentSearchIsNotExposedWithoutGrant(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentSearch(broker, &documentSearcherStub{}); err != nil {
		t.Fatal(err)
	}
	if definitions := broker.Definitions([]domain.ToolGrant{{Capability: domain.CapabilityEmailDraft}}); len(definitions) != 0 {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
}

func TestDocumentSearchEnforcesSignedGrantConditions(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	searcher := &documentSearcherStub{}
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentSearch(broker, searcher); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	allowedDocument := "251b403a-1fca-4d6c-aef9-cd625c32358d"
	token, err := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{
			"max_results": "2", "document_ids": allowedDocument,
		}}}, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"policy","limit":8}`)); err != nil {
		t.Fatal(err)
	}
	if searcher.limit != 2 || len(searcher.documentIDs) != 1 || searcher.documentIDs[0] != allowedDocument {
		t.Fatalf("grant conditions were not enforced: limit=%d document_ids=%v", searcher.limit, searcher.documentIDs)
	}
	_, err = broker.InvokeNamed(context.Background(), token, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"policy","document_ids":["38649fa6-2052-4388-8b9f-1d64f6f7a11f"]}`))
	if err == nil {
		t.Fatal("expected out-of-scope document to be rejected")
	}
}
