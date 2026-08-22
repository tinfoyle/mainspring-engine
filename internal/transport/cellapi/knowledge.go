package cellapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type knowledgeScopeRequest struct {
	Kind knowledgedomain.ScopeKind `json:"kind"`
	ID   string                    `json:"id,omitempty"`
}

type knowledgeEvidenceRequest struct {
	SourceKind      knowledgedomain.SourceKind `json:"source_kind"`
	SourceReference string                     `json:"source_reference"`
	SourceRevision  string                     `json:"source_revision"`
	ContentSHA256   string                     `json:"content_sha256"`
	CapturedAt      time.Time                  `json:"captured_at"`
}

type knowledgeCitationRequest struct {
	EvidenceID   ids.KnowledgeEvidenceID          `json:"evidence_id"`
	EvidenceKind knowledgedomain.SourceKind       `json:"evidence_kind"`
	Relation     knowledgedomain.EvidenceRelation `json:"relation"`
	Locator      string                           `json:"locator"`
}

type knowledgeClaimRequest struct {
	Scope       knowledgeScopeRequest       `json:"scope"`
	Key         string                      `json:"key"`
	Value       json.RawMessage             `json:"value"`
	Confidence  uint16                      `json:"confidence"`
	Sensitivity knowledgedomain.Sensitivity `json:"sensitivity"`
	Citations   []knowledgeCitationRequest  `json:"citations"`
}

type knowledgeDecisionRequest struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason"`
}

func (s *Server) knowledgeEvidenceRegister(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.knowledgeCommandContext(w, r)
	if !ok {
		return
	}
	var body knowledgeEvidenceRequest
	if !decodeKnowledgeJSON(w, r, &body) {
		return
	}
	digest, err := hex.DecodeString(body.ContentSHA256)
	if err != nil || len(digest) != sha256.Size {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_evidence", "content_sha256 must be exactly 64 hexadecimal characters")
		return
	}
	var contentSHA256 [sha256.Size]byte
	copy(contentSHA256[:], digest)
	evidence, err := s.knowledge.RegisterEvidence(routecontext.WithClaims(r.Context(), claims), knowledgeapp.RegisterEvidenceCommand{
		Actor: actor, AccountID: accountID, EvidenceID: ids.KnowledgeEvidenceID(operationID), Kind: body.SourceKind,
		SourceReference: body.SourceReference, SourceRevision: body.SourceRevision, ContentSHA256: contentSHA256,
		CapturedAt: body.CapturedAt, CorrelationID: operationID,
	})
	if err != nil {
		s.writeKnowledgeError(w, "register evidence", err)
		return
	}
	writeJSON(w, http.StatusCreated, knowledgeEvidenceView(evidence))
}

func (s *Server) knowledgeClaimPropose(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.knowledgeCommandContext(w, r)
	if !ok {
		return
	}
	var body knowledgeClaimRequest
	if !decodeKnowledgeJSON(w, r, &body) {
		return
	}
	citations := make([]knowledgedomain.Citation, 0, len(body.Citations))
	for _, citation := range body.Citations {
		citations = append(citations, knowledgedomain.Citation{EvidenceID: citation.EvidenceID, EvidenceKind: citation.EvidenceKind, Relation: citation.Relation, Locator: citation.Locator})
	}
	claim, err := s.knowledge.ProposeClaim(routecontext.WithClaims(r.Context(), claims), knowledgeapp.ProposeClaimCommand{
		Actor: actor, AccountID: accountID, ClaimID: ids.KnowledgeClaimID(operationID),
		Scope: knowledgedomain.Scope{Kind: body.Scope.Kind, ID: body.Scope.ID}, Key: body.Key,
		CanonicalValue: body.Value, Confidence: body.Confidence, Sensitivity: body.Sensitivity,
		Citations: citations, CorrelationID: operationID,
	})
	if err != nil {
		s.writeKnowledgeError(w, "propose claim", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/knowledge/claims/%s", accountID, claim.ID))
	writeKnowledgeClaim(w, http.StatusCreated, claim)
}

func (s *Server) knowledgeClaimGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.knowledgeRequestContext(w, r, true)
	if !ok {
		return
	}
	claimID := ids.KnowledgeClaimID(r.PathValue("claimID"))
	if ids.Validate(string(claimID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_claim", "the Knowledge claim identifier is invalid")
		return
	}
	claim, err := s.knowledge.GetClaim(routecontext.WithClaims(r.Context(), claims), actor, accountID, claimID)
	if err != nil {
		s.writeKnowledgeError(w, "get claim", err)
		return
	}
	writeKnowledgeClaim(w, http.StatusOK, claim)
}

func (s *Server) knowledgeClaimDecide(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.knowledgeCommandContext(w, r)
	if !ok {
		return
	}
	claimID, version, ok := knowledgeClaimTarget(w, r)
	if !ok {
		return
	}
	var body knowledgeDecisionRequest
	if !decodeKnowledgeJSON(w, r, &body) {
		return
	}
	claim, fact, err := s.knowledge.DecideClaim(routecontext.WithClaims(r.Context(), claims), knowledgeapp.DecideClaimCommand{Actor: actor, AccountID: accountID, ClaimID: claimID, Accept: body.Accept, Reason: body.Reason, ExpectedVersion: version, CorrelationID: operationID})
	if err != nil {
		s.writeKnowledgeError(w, "decide claim", err)
		return
	}
	response := map[string]any{"claim": knowledgeClaimView(claim)}
	if fact != nil {
		response["fact"] = knowledgeFactView(*fact, claim.Sensitivity)
	}
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, claim.Version))
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) knowledgeFactList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.knowledgeRequestContext(w, r, false)
	if !ok {
		return
	}
	query, err := parseKnowledgeFactQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, "list facts", err)
		return
	}
	page, err := s.knowledge.ListFacts(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeKnowledgeError(w, "list facts", err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, knowledgeFactSummaryView(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("knowledge_fact", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) knowledgeRequestContext(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.knowledge == nil {
		writeProblem(w, http.StatusServiceUnavailable, "knowledge_unavailable", "Knowledge is unavailable in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if detail && len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_query", "Knowledge detail does not accept query parameters")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	return claims, attentionActor(claims), accountID, true
}

func (s *Server) knowledgeCommandContext(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, actor, accountID, ok := s.knowledgeRequestContext(w, r, false)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func knowledgeClaimTarget(w http.ResponseWriter, r *http.Request) (ids.KnowledgeClaimID, uint64, bool) {
	claimID := ids.KnowledgeClaimID(r.PathValue("claimID"))
	if ids.Validate(string(claimID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_claim", "the Knowledge claim identifier is invalid")
		return "", 0, false
	}
	values := r.Header.Values("If-Match")
	version, err := parseAttentionVersion(values)
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "knowledge_version_required", "If-Match with the current claim version is required")
		return "", 0, false
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_version", "If-Match must contain exactly one weak claim version ETag")
		return "", 0, false
	}
	return claimID, version, true
}

func decodeKnowledgeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_command", "Knowledge commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Knowledge commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_command", "the Knowledge command body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_command", "the Knowledge command body is invalid")
		return false
	}
	return true
}

func parseKnowledgeFactQuery(r *http.Request) (knowledgeapp.FactListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "scope_kind", "scope_id", "key_prefix", "cursor", "limit") {
		return knowledgeapp.FactListQuery{}, knowledgeapp.ErrInvalid
	}
	query := knowledgeapp.FactListQuery{KeyPrefix: values.Get("key_prefix")}
	kind, scopeID := knowledgedomain.ScopeKind(values.Get("scope_kind")), values.Get("scope_id")
	if kind != "" || scopeID != "" {
		scope := knowledgedomain.Scope{Kind: kind, ID: scopeID}
		if !scope.Valid() {
			return query, knowledgeapp.ErrInvalid
		}
		query.Scope = &scope
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return query, knowledgeapp.ErrInvalid
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "knowledge_fact")
		if err != nil {
			return query, knowledgeapp.ErrInvalid
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.KnowledgeFactID(cursor.ID)
	}
	return query, nil
}

func knowledgeEvidenceView(value knowledgedomain.Evidence) map[string]any {
	return map[string]any{"id": value.ID, "account_id": value.AccountID, "source_kind": value.Kind, "source_reference": value.SourceReference, "source_revision": value.SourceRevision, "content_sha256": hex.EncodeToString(value.ContentSHA256[:]), "captured_at": value.CapturedAt, "created_by": map[string]any{"kind": value.CreatedBy.Kind, "id": value.CreatedBy.ID}, "created_at": value.CreatedAt}
}

func knowledgeClaimView(value knowledgedomain.Claim) map[string]any {
	citations := make([]map[string]any, 0, len(value.Citations))
	for _, citation := range value.Citations {
		citations = append(citations, map[string]any{"evidence_id": citation.EvidenceID, "evidence_kind": citation.EvidenceKind, "relation": citation.Relation, "locator": citation.Locator})
	}
	result := map[string]any{"id": value.ID, "account_id": value.AccountID, "scope": knowledgeScopeView(value.Scope), "key": value.Key, "value": value.CanonicalValue, "value_sha256": hex.EncodeToString(value.ValueSHA256[:]), "hash_version": value.HashVersion, "confidence": value.Confidence, "sensitivity": value.Sensitivity, "citations": citations, "proposed_by": map[string]any{"kind": value.ProposedBy.Kind, "id": value.ProposedBy.ID}, "state": value.State, "version": value.Version, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt}
	if value.Decision != nil {
		result["decision"] = map[string]any{"reason": value.Decision.Reason, "decided_by_user_id": value.Decision.DecidedBy, "decided_at": value.Decision.DecidedAt}
	}
	return result
}

func knowledgeFactSummaryView(value knowledgeapp.FactSummary) map[string]any {
	return map[string]any{"id": value.ID, "current_claim_id": value.CurrentClaimID, "scope": knowledgeScopeView(value.Scope), "key": value.Key, "sensitivity": value.Sensitivity, "state": value.State, "revision": value.Revision, "accepted_at": value.AcceptedAt, "updated_at": value.UpdatedAt}
}

func knowledgeFactView(value knowledgedomain.Fact, sensitivity knowledgedomain.Sensitivity) map[string]any {
	return map[string]any{"id": value.ID, "account_id": value.AccountID, "current_claim_id": value.CurrentClaimID, "scope": knowledgeScopeView(value.Scope), "key": value.Key, "sensitivity": sensitivity, "state": value.State, "revision": value.Revision, "accepted_by_user_id": value.AcceptedBy, "accepted_at": value.AcceptedAt, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt}
}

func knowledgeScopeView(value knowledgedomain.Scope) map[string]any {
	result := map[string]any{"kind": value.Kind}
	if value.ID != "" {
		result["id"] = value.ID
	}
	return result
}

func writeKnowledgeClaim(w http.ResponseWriter, status int, claim knowledgedomain.Claim) {
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, claim.Version))
	writeJSON(w, status, knowledgeClaimView(claim))
}

func (s *Server) writeKnowledgeError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, knowledgeapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_request", "the Knowledge request is invalid")
	case errors.Is(err, knowledgeapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "knowledge_not_found", "the Knowledge resource was not found")
	case errors.Is(err, knowledgeapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "knowledge_conflict", "Knowledge changed; reload before retrying")
	case errors.Is(err, knowledgeapp.ErrConstraint):
		writeProblem(w, http.StatusUnprocessableEntity, "knowledge_rejected", "the Knowledge command violates an evidence or state constraint")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Knowledge operation")
	default:
		s.logger.Error("Knowledge operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "knowledge_unavailable", "the Knowledge operation could not be completed")
	}
}

var _ KnowledgeService = (*knowledgeapp.Service)(nil)
