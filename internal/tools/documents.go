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

const DocumentsSearchTool = "documents.search"

var DocumentsSearchSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["query"],
  "properties":{
    "query":{"type":"string","minLength":1,"maxLength":500},
    "limit":{"type":"integer","minimum":1,"maximum":10},
    "document_ids":{"type":"array","maxItems":20,"items":{"type":"string","format":"uuid"}}
  }
}`)

type DocumentSearcher interface {
	Search(context.Context, string, int, []string) ([]rag.SearchResult, error)
}

func RegisterDocumentSearch(broker *Broker, searcher DocumentSearcher) error {
	if broker == nil || searcher == nil {
		return errors.New("document broker and searcher are required")
	}
	return broker.RegisterDefinition(Definition{
		Name: DocumentsSearchTool, Capability: domain.CapabilityDocumentsRead,
		Description: "Search the tenant's uploaded documents and return bounded excerpts with citation IDs.",
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
