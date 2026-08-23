package browserapp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Server) accountExportsPage(w http.ResponseWriter, r *http.Request) {
	data, authenticated, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Account exports"
	data.Notice = accountExportNotice(r.URL.Query().Get("status"))
	if data.Selected != nil && !data.OwnerEnrollmentRequired && data.Selected.Role == accounts.RoleOwner && s.accountExports != nil && s.exportDownloads != nil {
		exports, err := s.accountExports.List(r.Context(), data.Selected.AccountID, authenticated.Session.UserID, 100)
		if err != nil {
			s.logger.Error("load Account exports", "account_id", data.Selected.AccountID, "error", err)
			data.Error = "Account export history could not be loaded."
		} else {
			data.CanManageExports, data.Exports = true, exports
		}
	}
	s.render(w, http.StatusOK, "account-exports", data)
}

func (s *Server) requestAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportForm(w, r)
	if !ok {
		return
	}
	if r.FormValue("confirmation") != "EXPORT" {
		http.Error(w, "Account export confirmation was invalid.", http.StatusBadRequest)
		return
	}
	_, err := s.accountExports.Create(r.Context(), accountexport.CreateCommand{AccountID: accountID, Actor: authenticated.Session.UserID, Session: authenticated.Session})
	if err != nil {
		s.redirectAccountExportError(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/account-exports?status=export_requested", http.StatusSeeOther)
}

func (s *Server) cancelAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportForm(w, r)
	if !ok {
		return
	}
	exportID := r.FormValue("export_id")
	version, err := strconv.ParseUint(r.FormValue("version"), 10, 64)
	if ids.Validate(exportID) != nil || err != nil || version == 0 {
		http.Error(w, "Account export cancellation was invalid.", http.StatusBadRequest)
		return
	}
	_, err = s.accountExports.Cancel(r.Context(), accountexport.CancelCommand{AccountID: accountID, Actor: authenticated.Session.UserID, ID: exportID, ExpectedVersion: version, Session: authenticated.Session})
	if err != nil {
		s.redirectAccountExportError(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/account-exports?status=export_canceled", http.StatusSeeOther)
}

func (s *Server) downloadAccountExport(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountExportForm(w, r)
	if !ok {
		return
	}
	exportID := r.FormValue("export_id")
	if ids.Validate(exportID) != nil {
		http.Error(w, "Account export download was invalid.", http.StatusBadRequest)
		return
	}
	capability, err := s.exportDownloads.Issue(r.Context(), accountexport.CapabilityCommand{AccountID: accountID, Actor: authenticated.Session.UserID, ExportID: exportID, Session: authenticated.Session})
	if err != nil {
		s.redirectAccountExportError(w, r, err)
		return
	}
	download, err := s.exportDownloads.Open(r.Context(), capability.Token)
	if err != nil || download.ExportID != exportID {
		s.logger.Error("open browser Account export", "export_id", exportID, "error", err)
		http.Error(w, "Account export could not be downloaded.", http.StatusServiceUnavailable)
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
		s.logger.Warn("stream browser Account export", "export_id", exportID, "error", err)
	}
}

func (s *Server) accountExportForm(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, ids.AccountID, bool) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	if s.accountExports == nil || s.exportDownloads == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Account export request was not accepted.", http.StatusForbidden)
		return sessions.Authenticated{}, "", false
	}
	accountID := r.FormValue("account_id")
	if ids.Validate(accountID) != nil {
		http.Error(w, "Account export request was invalid.", http.StatusBadRequest)
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(accountID), true
}

func (s *Server) redirectAccountExportError(w http.ResponseWriter, r *http.Request, err error) {
	if access.IsDenied(err, access.DenialOwnerEnrollment) {
		http.Redirect(w, r, "/app/security?status=owner_enrollment_required", http.StatusSeeOther)
		return
	}
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	status := "export_failed"
	if errors.Is(err, accountexport.ErrStateConflict) {
		status = "export_conflict"
	} else if errors.Is(err, accountexport.ErrArtifactUnavailable) {
		status = "export_unavailable"
	} else if access.IsDenied(err, access.DenialRole) || access.IsDenied(err, access.DenialMembership) || access.IsDenied(err, access.DenialAccountUnavailable) {
		status = "export_denied"
	}
	http.Redirect(w, r, "/app/account-exports?status="+status, http.StatusSeeOther)
}

func accountExportNotice(status string) string {
	switch status {
	case "export_requested":
		return "The Account export is queued. This page will show when its encrypted artifact is ready."
	case "export_canceled":
		return "The queued Account export was canceled."
	case "export_conflict":
		return "The export changed before the action completed. Refresh and try again."
	case "export_unavailable":
		return "The export artifact is not available or has expired."
	case "export_denied":
		return "Only an active Account owner can manage exports."
	case "export_failed":
		return "The Account export action could not be completed."
	default:
		return ""
	}
}
