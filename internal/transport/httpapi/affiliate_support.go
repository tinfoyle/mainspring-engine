package httpapi

import (
	"errors"
	"net/http"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Server) listAffiliateSupportRequests(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateSupportRequest(w, r, false)
	if !ok {
		return
	}
	requests, err := s.affiliateSupport.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_support_unavailable", "Affiliate support requests are temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": requests})
}

func (s *Server) submitAffiliateSupportRequest(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateSupportRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Kind              affiliates.SupportKind `json:"kind"`
		CommissionEntryID ids.CommissionEntryID  `json:"commission_entry_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request, err := s.affiliateSupport.Submit(r.Context(), affiliatesupport.SubmitCommand{UserID: authenticated.Session.UserID,
		Kind: input.Kind, CommissionEntryID: input.CommissionEntryID})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, request)
	case errors.Is(err, affiliateprogram.ErrEnrollmentNotFound):
		writeProblem(w, http.StatusNotFound, "affiliate_enrollment_not_found", "Affiliate enrollment was not found")
	case errors.Is(err, affiliatesupport.ErrAlreadyOpen):
		writeProblem(w, http.StatusConflict, "affiliate_support_request_already_open", "an equivalent Affiliate support request is already open")
	case errors.Is(err, affiliatesupport.ErrEnrollmentState):
		writeProblem(w, http.StatusConflict, "affiliate_enrollment_not_appealable", "only a suspended or closed Affiliate enrollment can be appealed")
	case errors.Is(err, affiliatesupport.ErrCommissionNotOwned):
		writeProblem(w, http.StatusNotFound, "affiliate_commission_not_found", "the Affiliate commission entry was not found")
	case errors.Is(err, affiliates.ErrInvalidSupportRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_affiliate_support_request", "choose a supported Affiliate review type and subject")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_support_submission_failed", "the Affiliate support request could not be submitted")
	}
}

func (s *Server) cancelAffiliateSupportRequest(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateSupportRequest(w, r, true)
	if !ok {
		return
	}
	request, err := s.affiliateSupport.Cancel(r.Context(), ids.AffiliateSupportRequestID(r.PathValue("requestID")), authenticated.Session.UserID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, request)
	case errors.Is(err, affiliatesupport.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "affiliate_support_request_not_found", "the Affiliate support request was not found")
	case errors.Is(err, affiliatesupport.ErrNotCancelable):
		writeProblem(w, http.StatusConflict, "affiliate_support_request_not_cancelable", "the Affiliate support request can no longer be canceled")
	case errors.Is(err, affiliates.ErrInvalidSupportRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_affiliate_support_request", "the Affiliate support request identifier is invalid")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_support_cancellation_failed", "the Affiliate support request could not be canceled")
	}
}

func (s *Server) authenticateAffiliateSupportRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, bool) {
	if s.affiliateSupport == nil {
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_support_unconfigured", "Affiliate support requests are not configured")
		return sessions.Authenticated{}, false
	}
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, false
	}
	return s.authenticateSession(w, r)
}
