package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
)

type documentMemory struct {
	document rag.Document
	content  string
}

func (memory *documentMemory) CreateAgentDocument(_ context.Context, name, _ string, content string, _ rag.DocumentProvenance) (rag.Document, error) {
	memory.document = rag.Document{ID: "251b403a-1fca-4d6c-aef9-cd625c32358d", Name: name, Status: "ready", Revision: 1}
	memory.content = content
	return memory.document, nil
}

func (memory *documentMemory) UpdateAgentDocument(_ context.Context, documentID, name, _ string, content string, _ rag.DocumentProvenance) (rag.Document, error) {
	memory.document = rag.Document{ID: documentID, Name: name, Status: "ready", Revision: memory.document.Revision + 1}
	memory.content = content
	return memory.document, nil
}

func (memory *documentMemory) Search(_ context.Context, query string, _ int, _ []string) ([]rag.SearchResult, error) {
	if !strings.Contains(strings.ToLower(memory.content), strings.ToLower(query)) {
		return []rag.SearchResult{}, nil
	}
	return []rag.SearchResult{{DocumentID: memory.document.ID, DocumentName: memory.document.Name, Content: memory.content, ChunkIndex: 0, Rank: 1}}, nil
}

type documentSearcherStub struct {
	query       string
	limit       int
	documentIDs []string
}

type documentCatalogStub struct {
	documentSearcherStub
	documents []rag.Document
}

type documentWriterStub struct {
	created    rag.DocumentProvenance
	updated    rag.DocumentProvenance
	documentID string
}

func (stub *documentWriterStub) CreateAgentDocument(_ context.Context, name, _ string, _ string, provenance rag.DocumentProvenance) (rag.Document, error) {
	stub.created = provenance
	return rag.Document{ID: "251b403a-1fca-4d6c-aef9-cd625c32358d", Name: name, Status: "ready", Revision: 1}, nil
}

func (stub *documentWriterStub) UpdateAgentDocument(_ context.Context, documentID, name, _ string, _ string, provenance rag.DocumentProvenance) (rag.Document, error) {
	stub.documentID = documentID
	stub.updated = provenance
	return rag.Document{ID: documentID, Name: name, Status: "ready", Revision: 2}, nil
}

func TestDocumentWriteCreatesAndRevisesDurableAgentKnowledge(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	writer := &documentWriterStub{}
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentWrite(broker, writer); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	personaID, runID, invocationID := domain.NewPersonaID(), domain.NewRunID(), domain.NewInvocationID()
	token, err := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: runID, PersonaID: personaID, InvocationID: invocationID,
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsWrite}}, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsCreateTool, json.RawMessage(`{"name":"Launch plan","media_type":"text/markdown","content":"# Launch plan\n\nStart with three customers.","change_summary":"Captured the approved launch approach."}`))
	if err != nil {
		t.Fatal(err)
	}
	var createResult map[string]any
	if json.Unmarshal(created, &createResult) != nil || createResult["revision"] != float64(1) {
		t.Fatalf("unexpected create result: %s", created)
	}
	if writer.created.PersonaID != personaID.String() || writer.created.RunID != runID.String() || writer.created.InvocationID != invocationID.String() {
		t.Fatalf("agent provenance was not bound to signed claims: %#v", writer.created)
	}
	updated, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsUpdateTool, json.RawMessage(`{"document_id":"251b403a-1fca-4d6c-aef9-cd625c32358d","name":"Launch plan","media_type":"text/markdown","content":"# Launch plan\n\nStart with five customers.","change_summary":"Raised the cohort after review."}`))
	if err != nil {
		t.Fatal(err)
	}
	var updateResult map[string]any
	if json.Unmarshal(updated, &updateResult) != nil || updateResult["revision"] != float64(2) || writer.updated.ChangeSummary == "" {
		t.Fatalf("unexpected update result or provenance: %s %#v", updated, writer.updated)
	}
}

func TestDocumentWriteIsUnavailableWithoutSignedGrant(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentWrite(broker, &documentWriterStub{}); err != nil {
		t.Fatal(err)
	}
	if definitions := broker.Definitions([]domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}}); len(definitions) != 0 {
		t.Fatalf("write tools escaped their capability boundary: %#v", definitions)
	}
}

func TestDocumentRecallFindsKnowledgePublishedByEarlierAgentRun(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	memory := &documentMemory{}
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentSearch(broker, memory); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDocumentWrite(broker, memory); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	writerToken, _ := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsWrite}}, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if _, err := broker.InvokeNamed(context.Background(), writerToken, tenantID, DocumentsCreateTool, json.RawMessage(`{"name":"Customer launch baseline","media_type":"text/markdown","content":"The founding cohort is limited to three businesses.","change_summary":"Captured cohort decision."}`)); err != nil {
		t.Fatal(err)
	}
	readerToken, _ := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead}}, ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	output, err := broker.InvokeNamed(context.Background(), readerToken, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"founding cohort","limit":5,"document_ids":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "limited to three businesses") || !strings.Contains(string(output), "doc:251b403a") {
		t.Fatalf("later agent run could not recall earlier knowledge: %s", output)
	}
}

func (stub *documentCatalogStub) ListDocuments(context.Context) ([]rag.Document, error) {
	return stub.documents, nil
}

func (stub *documentSearcherStub) Search(_ context.Context, query string, limit int, documentIDs []string) ([]rag.SearchResult, error) {
	stub.query = query
	stub.limit = limit
	stub.documentIDs = documentIDs
	return []rag.SearchResult{{DocumentID: "251b403a-1fca-4d6c-aef9-cd625c32358d", DocumentName: "Policy.txt", ChunkIndex: 2, Content: query, Rank: 0.9}}, nil
}

func TestDocumentSearchWildcardRetrievesAuthorizedDocumentContent(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	searcher := &documentCatalogStub{documents: []rag.Document{{ID: "251b403a-1fca-4d6c-aef9-cd625c32358d", Name: "Checklist", Status: "ready"}}}
	broker := NewBroker(issuer, nil)
	if err := RegisterDocumentSearch(broker, searcher); err != nil {
		t.Fatal(err)
	}
	tenantID := domain.NewTenantID()
	token, err := issuer.Mint(domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(), PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants:    []domain.ToolGrant{{Capability: domain.CapabilityDocumentsRead, Conditions: map[string]string{"document_ids": "251b403a-1fca-4d6c-aef9-cd625c32358d"}}},
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"*","limit":5,"document_ids":["251b403a-1fca-4d6c-aef9-cd625c32358d"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Results []DocumentSearchResult `json:"results"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if searcher.query != "*" || len(decoded.Results) != 1 || decoded.Results[0].CitationID == "" || decoded.Results[0].Excerpt != "*" {
		t.Fatalf("filtered wildcard should retrieve citable content: query=%q results=%#v", searcher.query, decoded.Results)
	}
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

func TestDocumentSearchCatalogListsAvailableDocuments(t *testing.T) {
	issuer, _ := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	searcher := &documentCatalogStub{documents: []rag.Document{{
		ID: "251b403a-1fca-4d6c-aef9-cd625c32358d", Name: "Weekly sales report.pdf", Status: "ready", ChunkCount: 4,
	}}}
	broker := NewBroker(issuer, nil)
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
	output, err := broker.InvokeNamed(context.Background(), token, tenantID, DocumentsSearchTool, json.RawMessage(`{"query":"*","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Results []DocumentSearchResult `json:"results"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 || decoded.Results[0].DocumentName != "Weekly sales report.pdf" || decoded.Results[0].CitationID != "" {
		t.Fatalf("unexpected catalog results: %#v", decoded.Results)
	}
}
