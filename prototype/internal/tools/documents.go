package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
)

const (
	DocumentsSearchTool = "documents.search"
	DocumentsCreateTool = "documents.create"
	DocumentsUpdateTool = "documents.update"
)

const documentsCatalogQuery = "*"

var DocumentsSearchSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["query","limit","document_ids"],
  "properties":{
    "query":{"type":"string","minLength":1,"maxLength":500,"description":"Use * to list the available tenant documents before searching a specific document."},
    "limit":{"type":"integer","minimum":1,"maximum":10},
    "document_ids":{"type":"array","maxItems":20,"items":{"type":"string","format":"uuid"}}
  }
}`)

var DocumentsCreateSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["name","media_type","content","change_summary"],
  "properties":{
    "name":{"type":"string","minLength":1,"maxLength":255},
    "media_type":{"type":"string","enum":["text/plain","text/markdown","application/json"]},
    "content":{"type":"string","minLength":1,"maxLength":100000},
    "change_summary":{"type":"string","minLength":1,"maxLength":500}
  }
}`)

var DocumentsUpdateSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["document_id","name","media_type","content","change_summary"],
  "properties":{
    "document_id":{"type":"string","format":"uuid"},
    "name":{"type":"string","minLength":1,"maxLength":255},
    "media_type":{"type":"string","enum":["text/plain","text/markdown","application/json"]},
    "content":{"type":"string","minLength":1,"maxLength":100000},
    "change_summary":{"type":"string","minLength":1,"maxLength":500}
  }
}`)

type DocumentSearcher interface {
	Search(context.Context, string, int, []string) ([]rag.SearchResult, error)
}

type DocumentLister interface {
	ListDocuments(context.Context) ([]rag.Document, error)
}

type DocumentWriter interface {
	CreateAgentDocument(context.Context, string, string, string, rag.DocumentProvenance) (rag.Document, error)
	UpdateAgentDocument(context.Context, string, string, string, string, rag.DocumentProvenance) (rag.Document, error)
}

func RegisterDocumentWrite(broker *Broker, writer DocumentWriter) error {
	if broker == nil || writer == nil {
		return errors.New("document broker and writer are required")
	}
	if err := broker.RegisterDefinition(Definition{
		Name: DocumentsCreateTool, Capability: domain.CapabilityDocumentsWrite,
		Description: "Create a durable internal knowledge document that future agents can search. Use for reusable conclusions, plans, procedures, decisions, checklists, and completed ticket deliverables.",
		InputSchema: DocumentsCreateSchema,
	}, func(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
		var input struct {
			Name          string `json:"name"`
			MediaType     string `json:"media_type"`
			Content       string `json:"content"`
			ChangeSummary string `json:"change_summary"`
		}
		if err := decodeDocumentMutation(call.Input, &input); err != nil {
			return nil, fmt.Errorf("decode documents.create input: %w", err)
		}
		document, err := writer.CreateAgentDocument(ctx, input.Name, input.MediaType, input.Content, rag.DocumentProvenance{
			PersonaID: call.Claims.PersonaID, RunID: call.Claims.RunID, InvocationID: call.Claims.InvocationID, ChangeSummary: input.ChangeSummary,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(documentMutationResult(document))
	}); err != nil {
		return err
	}
	return broker.RegisterDefinition(Definition{
		Name: DocumentsUpdateTool, Capability: domain.CapabilityDocumentsWrite,
		Description: "Publish a complete new revision of an existing internal knowledge document. Search and read the existing document first, preserve still-valid content, and describe the change.",
		InputSchema: DocumentsUpdateSchema,
	}, func(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
		var input struct {
			DocumentID    string `json:"document_id"`
			Name          string `json:"name"`
			MediaType     string `json:"media_type"`
			Content       string `json:"content"`
			ChangeSummary string `json:"change_summary"`
		}
		if err := decodeDocumentMutation(call.Input, &input); err != nil {
			return nil, fmt.Errorf("decode documents.update input: %w", err)
		}
		if _, err := uuid.Parse(input.DocumentID); err != nil {
			return nil, errors.New("documents.update document_id must be a UUID")
		}
		document, err := writer.UpdateAgentDocument(ctx, input.DocumentID, input.Name, input.MediaType, input.Content, rag.DocumentProvenance{
			PersonaID: call.Claims.PersonaID, RunID: call.Claims.RunID, InvocationID: call.Claims.InvocationID, ChangeSummary: input.ChangeSummary,
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(documentMutationResult(document))
	})
}

func decodeDocumentMutation(input json.RawMessage, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func documentMutationResult(document rag.Document) map[string]any {
	return map[string]any{
		"document_id": document.ID, "document_name": document.Name, "revision": document.Revision,
		"status": document.Status, "citation_id": fmt.Sprintf("doc:%s", document.ID),
	}
}

func RegisterDocumentSearch(broker *Broker, searcher DocumentSearcher) error {
	if broker == nil || searcher == nil {
		return errors.New("document broker and searcher are required")
	}
	return broker.RegisterDefinition(Definition{
		Name: DocumentsSearchTool, Capability: domain.CapabilityDocumentsRead,
		Description: "Search the tenant's uploaded documents and return bounded excerpts with citation IDs. Use query '*' to list available document names when the user has not identified one.",
		InputSchema: DocumentsSearchSchema,
	}, func(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
		var input struct {
			Query       string   `json:"query"`
			Limit       int      `json:"limit"`
			DocumentIDs []string `json:"document_ids"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(call.Input)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("decode documents.search input: %w", err)
		}
		input.Query = strings.TrimSpace(input.Query)
		if input.Query == "" || len(input.Query) > 500 {
			return nil, errors.New("documents.search query must contain between 1 and 500 characters")
		}
		if input.Limit == 0 {
			input.Limit = 5
		}
		if input.Limit < 1 || input.Limit > 10 {
			return nil, errors.New("documents.search limit must be between 1 and 10")
		}
		conditions := call.Claims.Conditions[string(domain.CapabilityDocumentsRead)]
		if value := conditions["max_results"]; value != "" {
			var maximum int
			if _, err := fmt.Sscan(value, &maximum); err != nil || maximum < 1 || maximum > 10 {
				return nil, errors.New("documents.search grant condition max_results must be between 1 and 10")
			}
			if input.Limit > maximum {
				input.Limit = maximum
			}
		}
		if value := strings.TrimSpace(conditions["document_ids"]); value != "" {
			allowed := make(map[string]bool)
			for _, id := range strings.Split(value, ",") {
				allowed[strings.TrimSpace(id)] = true
			}
			if len(input.DocumentIDs) == 0 {
				for id := range allowed {
					input.DocumentIDs = append(input.DocumentIDs, id)
				}
			} else {
				for _, id := range input.DocumentIDs {
					if !allowed[id] {
						return nil, errors.New("documents.search requested a document outside the agent grant")
					}
				}
			}
		}
		if len(input.DocumentIDs) > 20 {
			return nil, errors.New("documents.search accepts at most 20 document filters")
		}
		for _, documentID := range input.DocumentIDs {
			if _, err := uuid.Parse(documentID); err != nil {
				return nil, errors.New("documents.search document IDs must be UUIDs")
			}
		}
		if input.Query == documentsCatalogQuery && len(input.DocumentIDs) == 0 {
			lister, ok := searcher.(DocumentLister)
			if !ok {
				return nil, errors.New("documents.search catalog mode is unavailable")
			}
			documents, err := lister.ListDocuments(ctx)
			if err != nil {
				return nil, err
			}
			allowed := make(map[string]bool, len(input.DocumentIDs))
			for _, documentID := range input.DocumentIDs {
				allowed[documentID] = true
			}
			output := struct {
				Results []DocumentSearchResult `json:"results"`
			}{Results: make([]DocumentSearchResult, 0, min(input.Limit, len(documents)))}
			for _, document := range documents {
				if len(allowed) > 0 && !allowed[document.ID] {
					continue
				}
				output.Results = append(output.Results, DocumentSearchResult{
					DocumentID: document.ID, DocumentName: document.Name,
					Excerpt: fmt.Sprintf("Available document (%s; %d indexed chunks).", document.Status, document.ChunkCount),
				})
				if len(output.Results) == input.Limit {
					break
				}
			}
			return json.Marshal(output)
		}
		results, err := searcher.Search(ctx, input.Query, input.Limit, input.DocumentIDs)
		if err != nil {
			return nil, err
		}
		output := struct {
			Results []DocumentSearchResult `json:"results"`
		}{Results: make([]DocumentSearchResult, 0, len(results))}
		for _, result := range results {
			output.Results = append(output.Results, DocumentSearchResult{
				CitationID: fmt.Sprintf("doc:%s:chunk:%d", result.DocumentID, result.ChunkIndex),
				DocumentID: result.DocumentID, DocumentName: result.DocumentName,
				ChunkID: fmt.Sprintf("%s:%d", result.DocumentID, result.ChunkIndex), Excerpt: result.Content, Rank: result.Rank,
			})
		}
		return json.Marshal(output)
	})
}

type DocumentSearchResult struct {
	CitationID   string  `json:"citation_id"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkID      string  `json:"chunk_id"`
	Excerpt      string  `json:"excerpt"`
	Rank         float32 `json:"rank"`
}
