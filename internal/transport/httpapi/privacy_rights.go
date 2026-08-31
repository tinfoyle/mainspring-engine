package httpapi

import (
	"errors"
	"net/http"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrights"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Server) listPrivacyRightsRequests(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticatePrivacyRightsRequest(w, r, false)
	if !ok {
		return
	}
	requests, err := s.privacyRights.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_rights_unavailable", "privacy rights requests are temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
}

func (s *Server) submitPrivacyRightsRequest(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticatePrivacyRightsRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Kind  privacy.RightsKind  `json:"kind"`
		Scope privacy.RightsScope `json:"scope"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request, err := s.privacyRights.Submit(r.Context(), privacyrights.SubmitCommand{UserID: authenticated.Session.UserID,
		Session: authenticated.Session, Kind: input.Kind, Scope: input.Scope})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, request)
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm your identity before submitting a privacy rights request")
	case errors.Is(err, privacyrights.ErrAlreadyOpen):
		writeProblem(w, http.StatusConflict, "privacy_rights_request_already_open", "an equivalent privacy rights request is already open")
	case errors.Is(err, privacy.ErrInvalidRightsRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_privacy_rights_request", "choose a supported privacy right and scope")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "privacy_rights_submission_failed", "the privacy rights request could not be submitted")
	}
}

func (s *Server) cancelPrivacyRightsRequest(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticatePrivacyRightsRequest(w, r, true)
	if !ok {
		return
	}
	requestID := ids.PrivacyRightsRequestID(r.PathValue("requestID"))
	request, err := s.privacyRights.Cancel(r.Context(), privacyrights.CancelCommand{RequestID: requestID,
		UserID: authenticated.Session.UserID, Session: authenticated.Session})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, request)
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm your identity before canceling a privacy rights request")
	case errors.Is(err, privacyrights.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "privacy_rights_request_not_found", "the privacy rights request was not found")
	case errors.Is(err, privacyrights.ErrNotCancelable):
		writeProblem(w, http.StatusConflict, "privacy_rights_request_not_cancelable", "the privacy rights request can no longer be canceled")
	case errors.Is(err, privacy.ErrInvalidRightsRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_privacy_rights_request", "the privacy rights request identifier is invalid")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "privacy_rights_cancellation_failed", "the privacy rights request could not be canceled")
	}
}

func (s *Server) authenticatePrivacyRightsRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, bool) {
	if s.privacyRights == nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_rights_unconfigured", "privacy rights requests are not configured")
		return sessions.Authenticated{}, false
	}
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, false
	}
	return s.authenticateSession(w, r)
}
