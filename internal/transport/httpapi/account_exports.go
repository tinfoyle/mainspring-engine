package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/exportcapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Server) listAccountExports(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportRequest(w, r, false)
	if !ok {
		return
	}
	limit := uint64(25)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	statuses, err := s.accountExports.List(r.Context(), accountID, authenticated.Session.UserID, limit)
	if err != nil {
		s.writeAccountExportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exports": statuses})
}

func (s *Server) createAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportRequest(w, r, true)
	if !ok {
		return
	}
	status, err := s.accountExports.Create(r.Context(), accountexport.CreateCommand{AccountID: accountID, Actor: authenticated.Session.UserID, Session: authenticated.Session})
	if err != nil {
		s.writeAccountExportError(w, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/exports/%s", accountID, status.ID))
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) getAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportRequest(w, r, false)
	if !ok {
		return
	}
	status, err := s.accountExports.Get(r.Context(), accountID, authenticated.Session.UserID, r.PathValue("exportID"))
	if err != nil {
		s.writeAccountExportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) cancelAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status, err := s.accountExports.Cancel(r.Context(), accountexport.CancelCommand{AccountID: accountID, Actor: authenticated.Session.UserID, ID: r.PathValue("exportID"), ExpectedVersion: input.ExpectedVersion, Session: authenticated.Session})
	if err != nil {
		s.writeAccountExportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) createAccountExportDownloadCapability(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportRequest(w, r, true)
	if !ok {
		return
	}
	capability, err := s.exportDownloads.Issue(r.Context(), accountexport.CapabilityCommand{AccountID: accountID, Actor: authenticated.Session.UserID, ExportID: r.PathValue("exportID"), Session: authenticated.Session})
	if err != nil {
		s.writeAccountExportError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, capability)
}

func (s *Server) downloadAccountExport(w http.ResponseWriter, r *http.Request) {
	if s.exportDownloads == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_exports_unconfigured", "Account exports are not configured")
		return
	}
	if r.URL.RawQuery != "" || len(r.Header.Values("Cookie")) != 0 || r.PathValue("exportID") == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_download_request", "download request is invalid")
		return
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		writeProblem(w, http.StatusUnauthorized, "download_capability_required", "a download capability is required")
		return
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || scheme != exportcapability.TokenType || token == "" || strings.TrimSpace(token) != token {
		writeProblem(w, http.StatusUnauthorized, "download_capability_required", "a download capability is required")
		return
	}
	download, err := s.exportDownloads.Open(r.Context(), token)
	if err != nil || download.ExportID != r.PathValue("exportID") {
		if err != nil && !errors.Is(err, accountexport.ErrCapabilityInvalid) && !errors.Is(err, exportcapability.ErrInvalid) {
			s.logger.Error("open Account export artifact", "export_id", r.PathValue("exportID"), "error", err)
		}
		writeProblem(w, http.StatusUnauthorized, "download_capability_invalid", "the download capability is invalid or expired")
		return
	}
	defer download.Body.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="spyglass-account-export-%s.zip"`, download.ExportID))
	w.Header().Set("Content-Length", strconv.FormatInt(download.Bytes, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, download.Body, download.Bytes); err != nil {
		s.logger.Warn("stream Account export artifact", "export_id", download.ExportID, "error", err)
	}
}

func (s *Server) accountExportRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, ids.AccountID, bool) {
	if s.accountExports == nil || s.exportDownloads == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_exports_unconfigured", "Account exports are not configured")
		return sessions.Authenticated{}, "", false
	}
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, "", false
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	raw := r.PathValue("accountID")
	if ids.Validate(raw) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "account ID is invalid")
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(raw), true
}

func (s *Server) writeAccountExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_authentication_required", "recent passkey authentication is required")
	case errors.Is(err, accountexport.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "account_export_not_found", "Account export request was not found")
	case errors.Is(err, accountexport.ErrStateConflict):
		writeProblem(w, http.StatusConflict, "account_export_state_conflict", "Account export request state changed")
	case errors.Is(err, accountexport.ErrArtifactUnavailable):
		writeProblem(w, http.StatusConflict, "account_export_unavailable", "Account export artifact is not available")
	case access.IsDenied(err, access.DenialRole), access.IsDenied(err, access.DenialAccountUnavailable), access.IsDenied(err, access.DenialMembership), access.IsDenied(err, access.DenialOwnerEnrollment):
		writeProblem(w, http.StatusForbidden, "account_export_access_denied", "Account export access is denied")
	case errors.Is(err, accountexport.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_account_export", err.Error())
	default:
		s.logger.Error("Account export request failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "account_export_failed", "Account export request could not be completed")
	}
}
