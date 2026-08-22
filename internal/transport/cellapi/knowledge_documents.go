package cellapi

import (
	"encoding/hex"
	"fmt"
	"mime/multipart"
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

const maximumKnowledgeDocumentUploadEnvelope = knowledgedomain.MaximumDocumentBytes + int64(1<<20)

type knowledgeDocumentPublishRequest struct {
	RevisionID ids.KnowledgeDocumentRevisionID `json:"revision_id"`
}

func (s *Server) knowledgeDocumentUpload(w http.ResponseWriter, r *http.Request) {
	claims, captured, ok := s.acceptCaptured(w, r, maximumKnowledgeDocumentUploadEnvelope)
	if !ok {
		return
	}
	defer captured.Close()
	claims, actor, accountID, operationID, ok := s.knowledgeDocumentCommandClaims(w, r, claims, false)
	if !ok {
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data;") {
		writeProblem(w, http.StatusUnsupportedMediaType, "multipart_required", "Knowledge document upload requires multipart/form-data")
		return
	}
	body, err := captured.Open()
	if err != nil {
		s.writeKnowledgeError(w, "open captured document upload", err)
		return
	}
	defer body.Close()
	r.Body = body
	if err := r.ParseMultipartForm(64 << 10); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "the Knowledge document upload is invalid")
		return
	}
	defer r.MultipartForm.RemoveAll()
	fileHeader, fields, ok := knowledgeDocumentMultipart(w, r.MultipartForm)
	if !ok {
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "the Knowledge document file could not be read")
		return
	}
	defer file.Close()
	documentID, documentErr := ids.Derive(operationID, "knowledge-document")
	revisionID, revisionErr := ids.Derive(operationID, "knowledge-document-revision-1")
	if documentErr != nil || revisionErr != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key cannot identify a Knowledge document")
		return
	}
	var retainUntil *time.Time
	if fields["retain_until"] != "" {
		value, err := time.Parse(time.RFC3339, fields["retain_until"])
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "retain_until must be an RFC3339 timestamp")
			return
		}
		value = value.UTC()
		retainUntil = &value
	}
	document, revision, err := s.documents.Upload(routecontext.WithClaims(r.Context(), claims), knowledgeapp.UploadDocumentCommand{
		Actor: actor, AccountID: accountID, DocumentID: ids.KnowledgeDocumentID(documentID), RevisionID: ids.KnowledgeDocumentRevisionID(revisionID),
		Title: fields["title"], Sensitivity: knowledgedomain.Sensitivity(fields["sensitivity"]), RetainUntil: retainUntil,
		Filename: fileHeader.Filename, DeclaredType: fileHeader.Header.Get("Content-Type"), Body: file,
		ChangeSummary: fields["change_summary"], CorrelationID: operationID,
	})
	if err != nil {
		s.writeKnowledgeError(w, "upload document", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/knowledge/documents/%s", accountID, document.ID))
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, document.Version))
	writeJSON(w, http.StatusCreated, knowledgeDocumentDetailView(knowledgeapp.DocumentDetail{Document: document, LatestRevision: revision}))
}

func (s *Server) knowledgeDocumentList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.knowledgeDocumentRequestContext(w, r, false)
	if !ok {
		return
	}
	query, err := parseKnowledgeDocumentQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, "list documents", err)
		return
	}
	page, err := s.documents.List(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeKnowledgeError(w, "list documents", err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, knowledgeDocumentSummaryView(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("knowledge_document", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) knowledgeDocumentGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.knowledgeDocumentRequestContext(w, r, true)
	if !ok {
		return
	}
	documentID, ok := knowledgeDocumentID(w, r)
	if !ok {
		return
	}
	detail, err := s.documents.GetDetail(routecontext.WithClaims(r.Context(), claims), actor, accountID, documentID)
	if err != nil {
		s.writeKnowledgeError(w, "get document", err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, detail.Document.Version))
	writeJSON(w, http.StatusOK, knowledgeDocumentDetailView(detail))
}

func (s *Server) knowledgeDocumentPublish(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.knowledgeDocumentCommandContext(w, r, false)
	if !ok {
		return
	}
	documentID, version, ok := knowledgeDocumentTarget(w, r)
	if !ok {
		return
	}
	var body knowledgeDocumentPublishRequest
	if !decodeKnowledgeJSON(w, r, &body) {
		return
	}
	if ids.Validate(string(body.RevisionID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_document_revision", "the Knowledge document revision identifier is invalid")
		return
	}
	document, err := s.documents.Publish(routecontext.WithClaims(r.Context(), claims), knowledgeapp.PublishDocumentCommand{Actor: actor, AccountID: accountID, DocumentID: documentID, RevisionID: body.RevisionID, ExpectedVersion: version, CorrelationID: operationID})
	if err != nil {
		s.writeKnowledgeError(w, "publish document", err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, document.Version))
	writeJSON(w, http.StatusOK, knowledgeDocumentView(document))
}

func (s *Server) knowledgeDocumentDelete(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.knowledgeDocumentCommandContext(w, r, true)
	if !ok {
		return
	}
	documentID, version, ok := knowledgeDocumentTarget(w, r)
	if !ok {
		return
	}
	document, err := s.documents.Delete(routecontext.WithClaims(r.Context(), claims), knowledgeapp.DeleteDocumentCommand{Actor: actor, AccountID: accountID, DocumentID: documentID, ExpectedVersion: version, CorrelationID: operationID})
	if err != nil {
		s.writeKnowledgeError(w, "delete document", err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, document.Version))
	writeJSON(w, http.StatusAccepted, knowledgeDocumentView(document))
}

func (s *Server) knowledgeDocumentRequestContext(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	claims, actor, accountID, _, ok := s.knowledgeDocumentClaims(w, r, claims, detail, false)
	return claims, actor, accountID, ok
}

func (s *Server) knowledgeDocumentCommandContext(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return s.knowledgeDocumentCommandClaims(w, r, claims, detail)
}

func (s *Server) knowledgeDocumentCommandClaims(w http.ResponseWriter, r *http.Request, claims routecontext.Claims, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, actor, accountID, operationID, ok := s.knowledgeDocumentClaims(w, r, claims, detail, true)
	return claims, actor, accountID, operationID, ok
}

func (s *Server) knowledgeDocumentClaims(w http.ResponseWriter, r *http.Request, claims routecontext.Claims, detail, command bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	if s.documents == nil {
		writeProblem(w, http.StatusServiceUnavailable, "knowledge_documents_unavailable", "Knowledge documents are unavailable in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if detail && len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_query", "Knowledge document detail does not accept query parameters")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	operationID := ""
	if command {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
			return routecontext.Claims{}, access.Actor{}, "", "", false
		}
		operationID = claims.Authority.OperationID
	}
	return claims, attentionActor(claims), accountID, operationID, true
}

func knowledgeDocumentMultipart(w http.ResponseWriter, form *multipart.Form) (*multipart.FileHeader, map[string]string, bool) {
	if form == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "the Knowledge document upload is invalid")
		return nil, nil, false
	}
	allowed := map[string]bool{"title": true, "sensitivity": true, "retain_until": false, "change_summary": false}
	fields := make(map[string]string, len(allowed))
	for name, values := range form.Value {
		required, exists := allowed[name]
		if !exists || len(values) != 1 || required && strings.TrimSpace(values[0]) == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "document metadata fields are invalid")
			return nil, nil, false
		}
		fields[name] = values[0]
	}
	for name, required := range allowed {
		if required && strings.TrimSpace(fields[name]) == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "title and sensitivity are required")
			return nil, nil, false
		}
	}
	if len(form.File) != 1 || len(form.File["file"]) != 1 || form.File["file"][0].Filename == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_document_upload", "exactly one document file is required")
		return nil, nil, false
	}
	return form.File["file"][0], fields, true
}

func parseKnowledgeDocumentQuery(r *http.Request) (knowledgeapp.DocumentListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "state", "title_prefix", "cursor", "limit") {
		return knowledgeapp.DocumentListQuery{}, knowledgeapp.ErrInvalid
	}
	query := knowledgeapp.DocumentListQuery{State: knowledgedomain.DocumentState(values.Get("state")), TitlePrefix: values.Get("title_prefix")}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return query, knowledgeapp.ErrInvalid
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "knowledge_document")
		if err != nil {
			return query, knowledgeapp.ErrInvalid
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.KnowledgeDocumentID(cursor.ID)
	}
	return query, nil
}

func knowledgeDocumentID(w http.ResponseWriter, r *http.Request) (ids.KnowledgeDocumentID, bool) {
	documentID := ids.KnowledgeDocumentID(r.PathValue("documentID"))
	if ids.Validate(string(documentID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_document", "the Knowledge document identifier is invalid")
		return "", false
	}
	return documentID, true
}

func knowledgeDocumentTarget(w http.ResponseWriter, r *http.Request) (ids.KnowledgeDocumentID, uint64, bool) {
	documentID, ok := knowledgeDocumentID(w, r)
	if !ok {
		return "", 0, false
	}
	values := r.Header.Values("If-Match")
	version, err := parseAttentionVersion(values)
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "knowledge_document_version_required", "If-Match with the current document version is required")
		return "", 0, false
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_knowledge_document_version", "If-Match must contain exactly one weak document version ETag")
		return "", 0, false
	}
	return documentID, version, true
}

func knowledgeDocumentSummaryView(value knowledgeapp.DocumentSummary) map[string]any {
	result := map[string]any{"id": value.ID, "title": value.Title, "sensitivity": value.Sensitivity, "state": value.State, "current_revision": value.CurrentRevision, "version": value.Version, "legal_hold": value.LegalHold, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt}
	if value.CurrentRevisionID != "" {
		result["current_revision_id"] = value.CurrentRevisionID
	}
	if value.RetainUntil != nil {
		result["retain_until"] = value.RetainUntil
	}
	return result
}

func knowledgeDocumentView(value knowledgedomain.Document) map[string]any {
	result := knowledgeDocumentSummaryView(knowledgeapp.DocumentSummary{ID: value.ID, Title: value.Title, Sensitivity: value.Sensitivity, CurrentRevisionID: value.CurrentRevisionID, CurrentRevision: value.CurrentRevision, State: value.State, RetainUntil: value.RetainUntil, LegalHold: value.LegalHold, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	result["account_id"] = value.AccountID
	result["created_by"] = map[string]any{"kind": value.CreatedBy.Kind, "id": value.CreatedBy.ID}
	if value.DeletionRequested != nil {
		result["deletion_requested_at"] = value.DeletionRequested
	}
	if value.DeletedAt != nil {
		result["deleted_at"] = value.DeletedAt
	}
	return result
}

func knowledgeDocumentDetailView(value knowledgeapp.DocumentDetail) map[string]any {
	revision := value.LatestRevision
	return map[string]any{"document": knowledgeDocumentView(value.Document), "latest_revision": map[string]any{
		"id": revision.ID, "number": revision.Number, "filename": revision.Filename, "declared_media_type": revision.DeclaredType,
		"verified_media_type": revision.VerifiedType, "byte_size": revision.ByteSize, "content_sha256": hex.EncodeToString(revision.ContentSHA256[:]),
		"change_summary": revision.ChangeSummary, "state": revision.State, "scan_state": revision.ScanState,
		"extraction_state": revision.Extraction, "index_state": revision.Index, "chunk_count": revision.ChunkCount,
		"failure_code": revision.FailureCode, "created_at": revision.CreatedAt, "updated_at": revision.UpdatedAt,
	}}
}
