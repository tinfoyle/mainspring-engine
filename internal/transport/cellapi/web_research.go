package cellapi

import (
	"errors"
	"net/http"
	"strings"

	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type webResearchSearchRequest struct {
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
	Query        string                      `json:"query"`
	Limit        int                         `json:"limit,omitempty"`
}

type webResearchReadRequest struct {
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
	URL          string                      `json:"url"`
}

func (s *Server) webResearchSearch(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.webResearchRequest(w, r, false)
	if !ok {
		return
	}
	var body webResearchSearchRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	result, err := s.webResearch.Search(routecontext.WithClaims(r.Context(), claims), webresearchapp.SearchCommand{
		Actor: actor, AccountID: accountID, OperationID: operationID, ConnectionID: body.ConnectionID, Query: body.Query, Limit: body.Limit,
	})
	if err != nil {
		s.writeWebResearchError(w, "search public web", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) webResearchRead(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.webResearchRequest(w, r, true)
	if !ok {
		return
	}
	var body webResearchReadRequest
	if !decodeIntegrationsJSON(w, r, &body) {
		return
	}
	result, err := s.webResearch.Read(routecontext.WithClaims(r.Context(), claims), webresearchapp.ReadCommand{
		Actor: actor, AccountID: accountID, OperationID: operationID, ConnectionID: body.ConnectionID, URL: body.URL,
	})
	if err != nil {
		s.writeWebResearchError(w, "read public web", err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) webResearchRequest(w http.ResponseWriter, r *http.Request, mutation bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.webResearch == nil {
		writeProblem(w, http.StatusServiceUnavailable, "web_research_unavailable", "Public web research is not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	actor := access.Actor{WorkloadID: claims.Authority.ActorID}
	if claims.Authority.ActorKind == "user" {
		actor = access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}
	}
	if !actor.Valid() {
		writeProblem(w, http.StatusForbidden, "web_research_denied", "Public web research requires current Account authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	operationID := claims.Authority.RequestID
	if !mutation && ids.Validate(claims.Authority.OperationID) == nil {
		operationID = claims.Authority.OperationID
	}
	if mutation {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
			return routecontext.Claims{}, access.Actor{}, "", "", false
		}
		operationID = claims.Authority.OperationID
	}
	if ids.Validate(operationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_web_research_operation", "the public web research operation authority is invalid")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, operationID, true
}

func (s *Server) writeWebResearchError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, webresearchapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_web_research_request", "the public web research request is invalid")
	case errors.Is(err, webresearchapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "web_research_connection_not_found", "the active public web research connection was not found")
	case errors.Is(err, webresearchapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "web_research_conflict", "the request conflicts with a durable public web capture")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow public web research")
	default:
		s.logger.Error("Public web research operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "web_research_unavailable", "the public web research operation could not be completed")
	}
}

var _ WebResearchService = (*webresearchapp.Service)(nil)
