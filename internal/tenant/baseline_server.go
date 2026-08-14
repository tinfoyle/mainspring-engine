package tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tinfoyle/mainspring-engine/internal/auth"
	"github.com/tinfoyle/mainspring-engine/internal/documentextract"
	mailbox "github.com/tinfoyle/mainspring-engine/internal/email"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
	"github.com/tinfoyle/mainspring-engine/internal/webresearch"
	"github.com/tinfoyle/mainspring-engine/web/components"
)

func (s *Server) baselinePage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Business baseline")
		return
	}
	s.renderBaselinePage(w, r, http.StatusOK, "")
}

func (s *Server) renderBaselinePage(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	assessment, err := s.store.EnsureBaseline(r.Context(), s.config.TenantID)
	if err != nil {
		s.logger.Error("load business baseline", "error", err)
		s.renderError(w, http.StatusInternalServerError, "The business baseline could not be loaded.")
		return
	}
	// Source selection is no longer a standalone onboarding step. Existing
	// assessments that stopped there continue directly into the evidence
	// interview, where uploads and public research appear in context.
	if assessment.Phase == BaselinePhaseSourceAccess {
		if err := s.store.AdvanceBaseline(r.Context(), s.config.TenantID, BaselinePhaseInventory); err != nil {
			s.logger.Error("advance legacy baseline source step", "error", err)
			s.renderError(w, http.StatusInternalServerError, "The evidence interview could not be prepared.")
			return
		}
		s.inventoryBaselineSources(r.Context())
		assessment, err = s.store.GetBaseline(r.Context(), s.config.TenantID)
		if err != nil {
			s.renderError(w, http.StatusInternalServerError, "The evidence interview could not be loaded.")
			return
		}
	}
	if assessment.Phase == BaselinePhaseInventory {
		rescoped, scopeErr := s.store.ReconcileBaselineEvidenceScope(r.Context(), s.config.TenantID)
		if scopeErr != nil {
			s.logger.Error("scope baseline evidence", "error", scopeErr)
			s.renderError(w, http.StatusInternalServerError, "The evidence interview could not be tailored to the business.")
			return
		}
		if rescoped {
			s.inventoryBaselineSources(r.Context())
			assessment, err = s.store.GetBaseline(r.Context(), s.config.TenantID)
			if err != nil {
				s.renderError(w, http.StatusInternalServerError, "The tailored evidence interview could not be loaded.")
				return
			}
		}
	}
	if _, err := s.email.Integration(r.Context()); err == nil {
		_ = s.store.SetBaselineSourceConnected(r.Context(), s.config.TenantID, "email")
		assessment, _ = s.store.GetBaseline(r.Context(), s.config.TenantID)
	} else if !errors.Is(err, mailbox.ErrNotConfigured) {
		s.logger.Warn("inspect baseline email connection", "error", err)
	}
	documents, err := s.documentOptions(r.Context(), nil)
	if err != nil {
		s.logger.Warn("load baseline documents", "error", err)
		documents = nil
	}
	notice := ""
	switch {
	case r.URL.Query().Get("saved") == "fact":
		notice = "Business fact updated."
	case r.URL.Query().Get("saved") == "evidence":
		notice = "Evidence linked to the baseline."
	case r.URL.Query().Get("saved") == "resolution":
		notice = "Gap resolution saved."
	case r.URL.Query().Get("plan_created") == "1":
		notice = fmt.Sprintf("Baseline work plan created with %s child tickets.", r.URL.Query().Get("tasks"))
	case r.URL.Query().Get("reassessment") == "1":
		notice = "A new baseline reassessment has started with confirmed facts carried forward."
	case r.URL.Query().Get("email_indexed") != "":
		notice = fmt.Sprintf("Inbox scope inspected: %s records indexed and %s evidence matches proposed.", r.URL.Query().Get("email_indexed"), r.URL.Query().Get("email_linked"))
	case r.URL.Query().Get("drive_indexed") != "":
		notice = fmt.Sprintf("Drive scope inspected: %s files indexed and %s evidence matches proposed.", r.URL.Query().Get("drive_indexed"), r.URL.Query().Get("drive_linked"))
	case r.URL.Query().Get("research") == "1":
		notice = "Public search completed. Review exactly what was searched and choose any result that should count as evidence."
	case r.URL.Query().Get("email") == "configured":
		notice = "Mailbox verified and connected to the baseline. Choose the folders and date range Mainspring may inspect."
	}
	view := baselineView(assessment, documents)
	if folders, folderErr := s.email.Folders(r.Context()); folderErr == nil {
		view.EmailFolders = folders
	}
	s.render(w, status, components.BaselinePage(
		s.tenantName(r.Context()), s.userView(session.User), view, s.csrfToken(session), notice, formError,
	))
}

func (s *Server) answerBaselineInterview(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AnswerBaselineQuestion(r.Context(), s.config.TenantID, r.FormValue("answer")); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	assessment, err := s.store.GetBaseline(r.Context(), s.config.TenantID)
	if err == nil && assessment.Phase == BaselinePhaseInventory {
		s.inventoryBaselineSources(r.Context())
	}
	http.Redirect(w, r, "/baseline", http.StatusSeeOther)
}

func (s *Server) saveBaselineFact(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SaveBaselineFact(r.Context(), s.config.TenantID, r.FormValue("key"), r.FormValue("value")); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/baseline?saved=fact", http.StatusSeeOther)
}

func (s *Server) configureBaselineSource(w http.ResponseWriter, r *http.Request) {
	sourceType := strings.TrimSpace(r.FormValue("source_type"))
	status := strings.TrimSpace(r.FormValue("status"))
	scope := map[string]any{"read_only": true}
	if sourceType == "email" {
		scope["folders"] = cleanFormValues(r.Form["folders"], 20, 200)
		scope["since"] = strings.TrimSpace(r.FormValue("since"))
		scope["until"] = strings.TrimSpace(r.FormValue("until"))
		scope["max_items"] = 100
	}
	if sourceType == "google_drive" {
		scope["folders"] = cleanFormValues(r.Form["folders"], 50, 500)
	}
	if err := s.store.ConfigureBaselineSource(r.Context(), s.config.TenantID, sourceType, status, scope); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/baseline", http.StatusSeeOther)
}

func (s *Server) syncBaselineEmail(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	parseDate := func(value string) (*time.Time, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, errors.New("email evidence dates must use YYYY-MM-DD")
		}
		return &parsed, nil
	}
	since, err := parseDate(r.FormValue("since"))
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	until, err := parseDate(r.FormValue("until"))
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	folders := cleanFormValues(r.Form["folders"], 20, 200)
	if len(folders) == 0 {
		folders = []string{"INBOX"}
	}
	scopeMap := map[string]any{"read_only": true, "folders": folders, "max_items": 100}
	if since != nil {
		scopeMap["since"] = since.Format("2006-01-02")
	}
	if until != nil {
		scopeMap["until"] = until.Format("2006-01-02")
	}
	if err := s.store.ConfigureBaselineSource(r.Context(), s.config.TenantID, "email", "connected", scopeMap); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.email.Evidence(r.Context(), mailbox.EvidenceScope{Folders: folders, Since: since, Until: until, MaxItems: 100})
	if err != nil {
		s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "email", err)
		s.renderBaselinePage(w, r, http.StatusBadGateway, "The selected mailbox scope could not be inspected: "+err.Error())
		return
	}
	assessment, err := s.store.GetBaseline(r.Context(), s.config.TenantID)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	indexed, linked := 0, 0
	for _, item := range items {
		externalID := item.MessageID
		if externalID == "" {
			externalID = fmt.Sprintf("%s:%d", item.Folder, item.UID)
		}
		bodyText := strings.TrimSpace(fmt.Sprintf("From: %s\nTo: %s\nDate: %s\nSubject: %s\n\n%s", item.From, strings.Join(item.To, ", "), item.Date.Format(time.RFC3339), item.Subject, item.Body))
		var bodyDocumentID string
		if bodyText != "" {
			name := safeEvidenceName("Email - "+item.Subject, ".txt")
			if document, ingestErr := s.documents.IngestText(r.Context(), name, "text/plain", bodyText, session.User.ID); ingestErr == nil {
				bodyDocumentID = document.ID
				indexed++
			}
		}
		digest := sha256.Sum256([]byte(bodyText))
		modified := item.Date
		sourceItemID, recordErr := s.store.RecordBaselineSourceItem(r.Context(), s.config.TenantID, RecordSourceItemInput{
			SourceType: "email", ExternalID: externalID, Name: fallback(item.Subject, "Email message"), MediaType: "message/rfc822",
			SourceURI: fmt.Sprintf("imap://%s/%d", item.Folder, item.UID), ModifiedAt: &modified, SHA256: hex.EncodeToString(digest[:]),
			Metadata: map[string]any{"from": item.From, "to": item.To, "folder": item.Folder, "uid": item.UID}, DocumentID: bodyDocumentID,
		})
		if recordErr == nil {
			for _, requirementID := range MatchBaselineEvidence(assessment.Requirements, item.Subject+"\n"+item.Body) {
				if s.store.LinkSourceItemEvidence(r.Context(), s.config.TenantID, requirementID, sourceItemID, "Email: "+fallback(item.Subject, "untitled message"), 0.72) == nil {
					linked++
				}
			}
		}
		for index, attachment := range item.Attachments {
			extracted, mediaType, extractErr := documentextract.Extract(attachment.Filename, attachment.Content)
			if extractErr != nil {
				continue
			}
			document, ingestErr := s.documents.IngestText(r.Context(), attachment.Filename, mediaType, extracted, session.User.ID)
			if ingestErr != nil {
				continue
			}
			indexed++
			attachmentDigest := sha256.Sum256(attachment.Content)
			attachmentID, recordErr := s.store.RecordBaselineSourceItem(r.Context(), s.config.TenantID, RecordSourceItemInput{
				SourceType: "email", ExternalID: fmt.Sprintf("%s:attachment:%d:%s", externalID, index, attachment.Filename),
				Name: attachment.Filename, MediaType: mediaType, SourceURI: fmt.Sprintf("imap://%s/%d/attachment/%d", item.Folder, item.UID, index),
				ModifiedAt: &modified, SHA256: hex.EncodeToString(attachmentDigest[:]), Metadata: map[string]any{"message_subject": item.Subject}, DocumentID: document.ID,
			})
			if recordErr != nil {
				continue
			}
			for _, requirementID := range MatchBaselineEvidence(assessment.Requirements, attachment.Filename+"\n"+extracted) {
				if s.store.LinkSourceItemEvidence(r.Context(), s.config.TenantID, requirementID, attachmentID, "Email attachment: "+attachment.Filename, 0.82) == nil {
					linked++
				}
			}
		}
	}
	s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "email", nil)
	http.Redirect(w, r, fmt.Sprintf("/baseline?email_indexed=%d&email_linked=%d", indexed, linked), http.StatusSeeOther)
}

func (s *Server) connectGoogleDrive(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if s.drive == nil || !s.drive.Enabled() {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, "Google Drive is available after an administrator configures the read-only OAuth client.")
		return
	}
	authorizationURL, err := s.drive.AuthorizationURL(s.csrfToken(session))
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	http.Redirect(w, r, authorizationURL, http.StatusSeeOther)
}

func (s *Server) googleDriveCallback(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !auth.CheckCSRF(session.RawToken, r.URL.Query().Get("state"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_oauth_state", "The Google Drive authorization request expired or could not be verified.")
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "Google Drive access was not granted: "+providerError)
		return
	}
	if s.drive == nil {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, "Google Drive is not configured.")
		return
	}
	if err := s.drive.Exchange(r.Context(), r.URL.Query().Get("code")); err != nil {
		s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "google_drive", err)
		s.renderBaselinePage(w, r, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.store.ConfigureBaselineSource(r.Context(), s.config.TenantID, "google_drive", "connected", map[string]any{"read_only": true, "folders": []string{}})
	http.Redirect(w, r, "/baseline/google-drive/folders", http.StatusSeeOther)
}

func (s *Server) googleDriveFolders(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if s.drive == nil || !s.drive.Enabled() {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, "Google Drive is not configured.")
		return
	}
	folders, err := s.drive.Folders(r.Context())
	if err != nil {
		s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "google_drive", err)
		s.renderBaselinePage(w, r, http.StatusBadGateway, "Google Drive folders could not be listed: "+err.Error())
		return
	}
	views := make([]components.GoogleDriveFolderView, 0, len(folders))
	for _, folder := range folders {
		views = append(views, components.GoogleDriveFolderView{ID: folder.ID, Name: folder.Name})
	}
	s.render(w, http.StatusOK, components.GoogleDriveFoldersPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session), ""))
}

func (s *Server) syncGoogleDrive(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if s.drive == nil || !s.drive.Enabled() {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, "Google Drive is not configured.")
		return
	}
	folderIDs := cleanFormValues(r.Form["folder_ids"], 50, 500)
	if len(folderIDs) == 0 {
		s.googleDriveFolders(w, r)
		return
	}
	if err := s.store.ConfigureBaselineSource(r.Context(), s.config.TenantID, "google_drive", "connected", map[string]any{"read_only": true, "folders": folderIDs}); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	files, err := s.drive.Files(r.Context(), folderIDs)
	if err != nil {
		s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "google_drive", err)
		s.renderBaselinePage(w, r, http.StatusBadGateway, "The selected Drive folders could not be inspected: "+err.Error())
		return
	}
	assessment, err := s.store.GetBaseline(r.Context(), s.config.TenantID)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	indexed, linked := 0, 0
	for _, file := range files {
		download, downloadErr := s.drive.Download(r.Context(), file)
		if downloadErr != nil {
			continue
		}
		extracted, mediaType, extractErr := documentextract.Extract(download.Filename, download.Content)
		if extractErr != nil {
			continue
		}
		document, ingestErr := s.documents.IngestText(r.Context(), download.Filename, mediaType, extracted, session.User.ID)
		if ingestErr != nil {
			continue
		}
		indexed++
		digest := sha256.Sum256(download.Content)
		modified := file.ModifiedTime
		sourceItemID, recordErr := s.store.RecordBaselineSourceItem(r.Context(), s.config.TenantID, RecordSourceItemInput{
			SourceType: "google_drive", ExternalID: file.ID, Name: file.Name, MediaType: mediaType, SourceURI: file.WebViewLink,
			ModifiedAt: &modified, SHA256: hex.EncodeToString(digest[:]), Metadata: map[string]any{"folder_ids": folderIDs, "drive_mime_type": file.MimeType}, DocumentID: document.ID,
		})
		if recordErr != nil {
			continue
		}
		for _, requirementID := range MatchBaselineEvidence(assessment.Requirements, file.Name+"\n"+extracted) {
			if s.store.LinkSourceItemEvidence(r.Context(), s.config.TenantID, requirementID, sourceItemID, "Google Drive: "+file.Name, 0.85) == nil {
				linked++
			}
		}
	}
	s.store.MarkBaselineSourceSync(r.Context(), s.config.TenantID, "google_drive", nil)
	http.Redirect(w, r, fmt.Sprintf("/baseline?drive_indexed=%d&drive_linked=%d", indexed, linked), http.StatusSeeOther)
}

func (s *Server) disconnectGoogleDrive(w http.ResponseWriter, r *http.Request) {
	if s.drive != nil {
		if err := s.drive.Disconnect(r.Context()); err != nil {
			s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
			return
		}
	}
	http.Redirect(w, r, "/baseline", http.StatusSeeOther)
}

func safeEvidenceName(value, extension string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character == '/' || character == '\\' || character < 32 {
			return '-'
		}
		return character
	}, value))
	if value == "" {
		value = "Evidence"
	}
	if len(value) > 240-len(extension) {
		value = value[:240-len(extension)]
	}
	return value + extension
}

func (s *Server) advanceBaseline(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.FormValue("phase"))
	if err := s.store.AdvanceBaseline(r.Context(), s.config.TenantID, target); err != nil {
		s.renderBaselinePage(w, r, http.StatusConflict, err.Error())
		return
	}
	if target == BaselinePhaseInventory {
		s.inventoryBaselineSources(r.Context())
	}
	http.Redirect(w, r, "/baseline", http.StatusSeeOther)
}

func (s *Server) inventoryBaselineSources(ctx context.Context) {
	assessment, err := s.store.GetBaseline(ctx, s.config.TenantID)
	if err != nil {
		return
	}
	for _, requirement := range assessment.Requirements {
		query := strings.TrimSpace(requirement.Label + " " + strings.Join(requirement.ExpectedArtifactTypes, " "))
		results, searchErr := s.documents.Search(ctx, query, 3, nil)
		if searchErr != nil {
			continue
		}
		seen := map[string]bool{}
		for _, result := range results {
			if seen[result.DocumentID] {
				continue
			}
			seen[result.DocumentID] = true
			_ = s.store.LinkDocumentCandidate(ctx, s.config.TenantID, requirement.ID, result.DocumentID, result.Content, 0.68)
		}
	}
	if s.research == nil {
		return
	}
	facts := map[string]string{}
	for _, fact := range assessment.Facts {
		facts[fact.Key] = fact.Value
	}
	queries := map[string]string{
		"identity_registration": fmt.Sprintf("%q %q official business registration government", facts["business_name"], facts["primary_location"]),
		"licenses_permits":      fmt.Sprintf("%q business license permit requirements %q official government", facts["industry"], facts["primary_location"]),
	}
	for _, requirement := range assessment.Requirements {
		query := strings.TrimSpace(queries[requirement.Key])
		if query == "" || len(requirement.Research) > 0 {
			continue
		}
		runID, beginErr := s.store.BeginBaselineResearch(ctx, s.config.TenantID, requirement.ID, query)
		if beginErr != nil {
			continue
		}
		results, searchErr := s.research.Search(ctx, webresearch.SearchRequest{Query: query, Limit: 5})
		stored := make([]BaselineResearchResult, 0, len(results))
		for _, result := range results {
			stored = append(stored, BaselineResearchResult{Title: result.Title, URL: result.URL, Description: result.Description, CitationID: result.CitationID, RetrievedAt: result.RetrievedAt})
		}
		_ = s.store.CompleteBaselineResearch(ctx, runID, stored, searchErr)
	}
}

func (s *Server) resolveBaselineEvidence(w http.ResponseWriter, r *http.Request) {
	var renewalDueAt *time.Time
	if value := strings.TrimSpace(r.FormValue("renewal_due")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			s.renderBaselinePage(w, r, http.StatusBadRequest, "Renewal date must use YYYY-MM-DD.")
			return
		}
		renewalDueAt = &parsed
	}
	if err := s.store.SetEvidenceDisposition(
		r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("disposition"), r.FormValue("responsibility"), renewalDueAt,
	); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/baseline?saved=resolution", http.StatusSeeOther)
}

func (s *Server) answerBaselineEvidenceInterview(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AnswerEvidenceInterview(
		r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("answer"), r.FormValue("choice"),
	); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, s.baselineEvidenceInterviewLocation(r.Context(), ""), http.StatusSeeOther)
}

func (s *Server) baselineEvidenceInterviewLocation(ctx context.Context, notice string) string {
	location := "/baseline"
	if notice != "" {
		location += "?saved=" + notice
	}
	anchor := "#evidence-interview-current"
	if assessment, err := s.store.GetBaseline(ctx, s.config.TenantID); err == nil {
		complete := true
		for _, requirement := range assessment.Requirements {
			if requirement.InterviewedAt == nil {
				complete = false
				break
			}
		}
		if complete {
			anchor = "#evidence-interview-complete"
		}
	}
	return location + anchor
}

func (s *Server) linkBaselineDocument(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.LinkDocumentEvidence(
		r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("document_id"), session.User.ID,
	); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, s.baselineEvidenceInterviewLocation(r.Context(), "evidence"), http.StatusSeeOther)
}

func (s *Server) addBaselinePublicEvidence(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AddPublicEvidence(
		r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("public_url"), r.FormValue("citation"), 0.9,
	); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, s.baselineEvidenceInterviewLocation(r.Context(), "evidence"), http.StatusSeeOther)
}

func (s *Server) researchBaselineEvidence(w http.ResponseWriter, r *http.Request) {
	if s.research == nil {
		s.renderBaselinePage(w, r, http.StatusServiceUnavailable, "Public research is not configured for this tenant.")
		return
	}
	requirementID := chi.URLParam(r, "requirementID")
	query := strings.TrimSpace(r.FormValue("query"))
	runID, err := s.store.BeginBaselineResearch(r.Context(), s.config.TenantID, requirementID, query)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	results, searchErr := s.research.Search(r.Context(), webresearch.SearchRequest{Query: query, Limit: 5})
	stored := make([]BaselineResearchResult, 0, len(results))
	for _, result := range results {
		stored = append(stored, BaselineResearchResult{
			Title: result.Title, URL: result.URL, Description: result.Description, CitationID: result.CitationID, RetrievedAt: result.RetrievedAt,
		})
	}
	if err := s.store.CompleteBaselineResearch(r.Context(), runID, stored, searchErr); err != nil {
		s.logger.Error("record baseline public research", "error", err, "run_id", runID)
	}
	if searchErr != nil {
		s.renderBaselinePage(w, r, http.StatusBadGateway, "Public research failed: "+searchErr.Error())
		return
	}
	http.Redirect(w, r, "/baseline?research=1", http.StatusSeeOther)
}

func (s *Server) uploadBaselineDocument(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadLimit+(256<<10))
	if err := r.ParseMultipartForm(documentUploadLimit); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "Choose a supported document no larger than 2 MB.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
		return
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "Choose a document to upload.")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, documentUploadLimit+1))
	if err != nil || len(content) == 0 || len(content) > documentUploadLimit {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "The document must be no larger than 15 MB.")
		return
	}
	extracted, mediaType, err := documentextract.Extract(header.Filename, content)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	name := filepath.Base(header.Filename)
	document, err := s.documents.IngestText(r.Context(), name, mediaType, extracted, session.User.ID)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "The document could not be indexed.")
		return
	}
	if err := s.store.LinkDocumentEvidence(r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), document.ID, session.User.ID); err != nil {
		s.renderBaselinePage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, s.baselineEvidenceInterviewLocation(r.Context(), "evidence"), http.StatusSeeOther)
}

func (s *Server) createBaselinePlan(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if r.FormValue("approve") != "true" {
		s.renderBaselinePage(w, r, http.StatusBadRequest, "Confirm approval before creating the baseline plan.")
		return
	}
	state, err := s.store.GetOnboarding(r.Context(), s.config.TenantID)
	if err != nil {
		s.renderBaselinePage(w, r, http.StatusInternalServerError, "The onboarding state could not be loaded.")
		return
	}
	if state.Status != OnboardingCompleted {
		state, err = s.store.ApplyBaselineFactsToOnboarding(r.Context(), s.config.TenantID, state)
		if err == nil {
			err = s.store.CompleteOnboarding(r.Context(), s.config.TenantID, state)
		}
		if err != nil {
			s.logger.Error("complete baseline onboarding", "error", err)
			s.renderBaselinePage(w, r, http.StatusInternalServerError, "The boardroom could not be prepared for the baseline plan.")
			return
		}
	}
	parent, taskCount, err := s.store.CreateBaselinePlan(r.Context(), s.config.TenantID, session.User.ID)
	if err != nil {
		s.logger.Error("create baseline plan", "error", err)
		s.renderBaselinePage(w, r, http.StatusInternalServerError, "The approved baseline plan could not be created.")
		return
	}
	http.Redirect(w, r, "/baseline?plan_created=1&tasks="+strconv.Itoa(taskCount)+"&work_item="+parent.ID, http.StatusSeeOther)
}

func (s *Server) reassessBaseline(w http.ResponseWriter, r *http.Request) {
	if err := s.store.StartBaselineReassessment(r.Context(), s.config.TenantID); err != nil {
		s.renderBaselinePage(w, r, http.StatusConflict, err.Error())
		return
	}
	http.Redirect(w, r, "/baseline?reassessment=1", http.StatusSeeOther)
}

func baselineView(assessment BaselineAssessment, documents []components.DocumentOptionView) components.BaselineView {
	evidenceScope := BaselineEvidenceScopeForFacts(assessment.Facts)
	view := components.BaselineView{
		ID: assessment.ID, Status: assessment.Status, Phase: assessment.Phase,
		CurrentQuestion: assessment.CurrentQuestion, QuestionCount: len(baselineQuestions),
		Documents: documents, PlanParentWorkItemID: assessment.PlanParentWorkItemID,
		EvidenceScopeTitle: evidenceScope.Title, EvidenceScopeSummary: evidenceScope.Explanation,
	}
	if question, ok := assessment.CurrentPrompt(); ok {
		view.CurrentPrompt = question.Prompt
		view.CurrentExplanation = question.Explanation
	}
	if assessment.NextReassessmentAt != nil {
		view.NextReassessment = assessment.NextReassessmentAt.Format("January 2, 2006")
	}
	for _, message := range assessment.Messages {
		view.Messages = append(view.Messages, components.BaselineMessageView{Role: message.Role, Body: message.Body})
	}
	labels := map[string]string{
		"business_name": "Business name", "website_url": "Website", "industry": "Industry",
		"primary_location": "Primary location", "services": "Products and services", "team_size": "Team size",
		"immediate_concern": "First priority",
	}
	for _, fact := range assessment.Facts {
		sourceLabel := "Confirmed by owner"
		if fact.SourceType != "owner" {
			sourceLabel = "From " + strings.ReplaceAll(fact.SourceType, "_", " ")
		}
		view.Facts = append(view.Facts, components.BusinessFactView{Key: fact.Key, Label: labels[fact.Key], Value: fact.Value, SourceLabel: sourceLabel})
	}
	for _, source := range assessment.Sources {
		scopeLabel := "Read-only"
		if folders, ok := source.Scope["folders"].([]any); ok && len(folders) > 0 {
			scopeLabel += fmt.Sprintf(" · %d selected folders", len(folders))
		}
		view.Sources = append(view.Sources, components.BaselineSourceView{Type: source.Type, DisplayName: source.DisplayName, Status: source.Status, ScopeLabel: scopeLabel})
	}
	for _, requirement := range assessment.Requirements {
		requirementView := components.EvidenceRequirementView{
			ID: requirement.ID, Domain: requirement.Domain, Label: requirement.Label, Rationale: requirement.Rationale,
			Status: requirement.Status, Disposition: requirement.Disposition, Responsibility: requirement.Responsibility,
			OwnerAnswer: requirement.OwnerAnswer, Interviewed: requirement.InterviewedAt != nil,
		}
		if requirement.RenewalDueAt != nil {
			requirementView.RenewalDue = requirement.RenewalDueAt.Format("2006-01-02")
		}
		for _, link := range requirement.Evidence {
			label, linkType, url := link.Citation, "Public source", link.PublicURL
			if link.DocumentID != "" {
				label, linkType, url = link.DocumentName, "Document", "/documents/"+link.DocumentID
			}
			requirementView.Evidence = append(requirementView.Evidence, components.EvidenceLinkView{Label: label, Type: linkType, URL: url})
		}
		for _, run := range requirement.Research {
			runView := components.BaselineResearchView{Query: run.Query, Status: run.Status, LastError: run.LastError}
			for _, result := range run.Results {
				runView.Results = append(runView.Results, components.BaselineResearchResultView{
					Title: result.Title, URL: result.URL, Description: result.Description, CitationID: result.CitationID,
				})
			}
			requirementView.Research = append(requirementView.Research, runView)
		}
		view.Requirements = append(view.Requirements, requirementView)
		view.InventoryTotal++
		if requirement.InterviewedAt != nil {
			view.InventoryAnswered++
		} else if view.InventoryCurrent == nil {
			current := requirementView
			view.InventoryCurrent = &current
		}
	}
	return view
}
