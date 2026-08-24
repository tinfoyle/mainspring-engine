package mcpapi

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type webResearchSearchInput struct {
	AccountID    ids.AccountID               `json:"account_id"`
	OperationID  string                      `json:"operation_id"`
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
	Query        string                      `json:"query"`
	Limit        int                         `json:"limit,omitempty"`
}

type webResearchReadInput struct {
	AccountID    ids.AccountID               `json:"account_id"`
	OperationID  string                      `json:"operation_id"`
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
	URL          string                      `json:"url"`
}

func (s *Server) registerWebResearch(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageIntegrations}
	mutation := access.Requirement{Package: catalog.PackageIntegrations, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{
		Name: "spyglass_integrations_web_search", Title: "Search the public web",
		Description: "Search within one exact HTTPS origin and path authorized by an active Web Research connection. Results are bounded, validated citations and do not create Knowledge records.",
		Annotations: toolAnnotations(true, false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input webResearchSearchInput) (*mcp.CallToolResult, webresearchapp.SearchResult, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, webresearchapp.SearchResult{}, err
		}
		op, err := operationID(input.OperationID)
		if err != nil {
			return nil, webresearchapp.SearchResult{}, err
		}
		value, err := s.webResearch.Search(ctx, webresearchapp.SearchCommand{Actor: actor, AccountID: input.AccountID,
			OperationID: op, ConnectionID: input.ConnectionID, Query: input.Query, Limit: input.Limit})
		return nil, value, webResearchError(err)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "spyglass_integrations_web_read", Title: "Capture a public web page",
		Description: "Fetch one URL within an exact active Web Research scope and admit the validated, source-attributed content through the ordinary immutable Knowledge document lifecycle. Requires both Integrations and Knowledge mutation authority.",
		Annotations: toolAnnotations(false, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input webResearchReadInput) (*mcp.CallToolResult, webresearchapp.ReadResult, error) {
		ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, 0, true)
		if err != nil {
			return nil, webresearchapp.ReadResult{}, err
		}
		value, err := s.webResearch.Read(ctx, webresearchapp.ReadCommand{Actor: actor, AccountID: input.AccountID,
			OperationID: op, ConnectionID: input.ConnectionID, URL: input.URL})
		return nil, value, webResearchError(err)
	})
}

func webResearchError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, webresearchapp.ErrInvalid):
		return safeError("invalid_web_research_request")
	case errors.Is(err, webresearchapp.ErrNotFound):
		return safeError("web_research_connection_not_found")
	case errors.Is(err, webresearchapp.ErrConflict):
		return safeError("web_research_conflict")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("web_research_unavailable")
	}
}

var _ WebResearchService = (*webresearchapp.Service)(nil)
