package httpapi

import (
	"errors"
	"net/http"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AffiliateHTTPConfig struct {
	EnrollmentOpen     bool
	AttributionEnabled bool
	SettlementMode     string
}

type affiliateProgramResponse struct {
	EnrollmentOpen     bool   `json:"enrollment_open"`
	AttributionEnabled bool   `json:"attribution_enabled"`
	TermsVersion       uint64 `json:"terms_version"`
	RuleVersion        uint64 `json:"rule_version"`
	SettlementMode     string `json:"settlement_mode"`
	Enrollment         any    `json:"enrollment,omitempty"`
}

var (
	errAffiliateSettlementUnconfigured = errors.New("Affiliate settlement is not configured")
	errAffiliateSettlementRequired     = errors.New("an owned settlement Account is required for Account credit")
	errAffiliateSettlementNotAllowed   = errors.New("a settlement Account is not accepted for cash settlement")
)

func validateAffiliateSettlement(mode string, accountID ids.AccountID) error {
	switch mode {
	case "account_credit", "account_credit_with_support_check":
		if accountID == "" {
			return errAffiliateSettlementRequired
		}
	case "cash":
		if accountID != "" {
			return errAffiliateSettlementNotAllowed
		}
	default:
		return errAffiliateSettlementUnconfigured
	}
	return nil
}

func (s *Server) getAffiliateProgram(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateRequest(w, r, false)
	if !ok {
		return
	}
	response := s.affiliateProgramStatus()
	enrollment, err := s.affiliateProgram.Current(r.Context(), authenticated.Session.UserID)
	if errors.Is(err, affiliateprogram.ErrEnrollmentNotFound) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_unavailable", "the Affiliate program is temporarily unavailable")
		return
	}
	response.Enrollment = enrollment
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) enrollAffiliate(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateRequest(w, r, true)
	if !ok {
		return
	}
	if !s.affiliateHTTP.EnrollmentOpen {
		writeProblem(w, http.StatusConflict, "affiliate_enrollment_closed", "Affiliate enrollment is not open")
		return
	}
	var input struct {
		SettlementAccountID  ids.AccountID `json:"settlement_account_id"`
		AcceptedTermsVersion uint64        `json:"accepted_terms_version"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAffiliateSettlement(s.affiliateHTTP.SettlementMode, input.SettlementAccountID); err != nil {
		switch {
		case errors.Is(err, errAffiliateSettlementRequired):
			writeProblem(w, http.StatusBadRequest, "affiliate_settlement_account_required", err.Error())
		case errors.Is(err, errAffiliateSettlementNotAllowed):
			writeProblem(w, http.StatusBadRequest, "affiliate_settlement_account_not_allowed", err.Error())
		default:
			writeProblem(w, http.StatusConflict, "affiliate_settlement_unconfigured", err.Error())
		}
		return
	}
	enrollment, err := s.affiliateProgram.Enroll(r.Context(), affiliateprogram.EnrollCommand{UserID: authenticated.Session.UserID, Session: authenticated.Session,
		SettlementAccountID: input.SettlementAccountID, AcceptedTermsVersion: input.AcceptedTermsVersion})
	switch {
	case err == nil:
		response := s.affiliateProgramStatus()
		response.Enrollment = enrollment
		writeJSON(w, http.StatusCreated, response)
	case errors.Is(err, affiliateprogram.ErrTermsRequired):
		writeProblem(w, http.StatusBadRequest, "affiliate_terms_required", "the current Affiliate terms must be accepted")
	case errors.Is(err, affiliateprogram.ErrSettlementAccountDenied):
		writeProblem(w, http.StatusForbidden, "affiliate_settlement_account_denied", "the settlement Account must be owned by the Affiliate")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before accepting Affiliate terms")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_enrollment_failed", "Affiliate enrollment could not be completed")
	}
}

func (s *Server) replaceAffiliateCode(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateRequest(w, r, true)
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
	enrollment, err := s.affiliateProgram.ReplaceCode(r.Context(), affiliateprogram.ReplaceCodeCommand{
		UserID: authenticated.Session.UserID, Session: authenticated.Session, ExpectedVersion: input.ExpectedVersion,
	})
	switch {
	case err == nil:
		response := s.affiliateProgramStatus()
		response.Enrollment = enrollment
		writeJSON(w, http.StatusOK, response)
	case errors.Is(err, affiliateprogram.ErrEnrollmentNotFound):
		writeProblem(w, http.StatusNotFound, "affiliate_enrollment_not_found", "Affiliate enrollment was not found")
	case errors.Is(err, affiliateprogram.ErrEnrollmentState):
		writeProblem(w, http.StatusConflict, "affiliate_code_replacement_unavailable", "only an active Affiliate enrollment can replace its public code")
	case errors.Is(err, affiliateprogram.ErrEnrollmentConflict):
		writeProblem(w, http.StatusConflict, "affiliate_enrollment_conflict", "the Affiliate enrollment changed; reload before replacing its public code")
	case errors.Is(err, affiliateprogram.ErrCodeUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_code_unavailable", "a new Affiliate code could not be reserved; try again")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before replacing the Affiliate code")
	case errors.Is(err, affiliates.ErrInvalidEnrollment):
		writeProblem(w, http.StatusBadRequest, "invalid_affiliate_code_replacement", "the Affiliate code replacement request is invalid")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_code_replacement_failed", "the Affiliate code could not be replaced")
	}
}

func (s *Server) getAffiliateStatement(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateRequest(w, r, false)
	if !ok {
		return
	}
	statement, err := s.affiliateProgram.Statement(r.Context(), authenticated.Session.UserID)
	if errors.Is(err, affiliateprogram.ErrEnrollmentNotFound) {
		writeProblem(w, http.StatusNotFound, "affiliate_enrollment_not_found", "Affiliate enrollment was not found")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_unavailable", "the Affiliate statement is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, statement)
}

func (s *Server) getAffiliateDataExport(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateAffiliateRequest(w, r, false)
	if !ok {
		return
	}
	value, err := s.affiliateProgram.Export(r.Context(), affiliateprogram.ExportCommand{
		UserID: authenticated.Session.UserID, Session: authenticated.Session,
	})
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="spyglass-affiliate-data.json"`)
		writeJSON(w, http.StatusOK, value)
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before exporting Affiliate data")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_export_unavailable", "the Affiliate data export is temporarily unavailable")
	}
}

func (s *Server) authenticateAffiliateRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, bool) {
	if s.affiliateProgram == nil {
		writeProblem(w, http.StatusServiceUnavailable, "affiliate_unconfigured", "the Affiliate program is not configured")
		return sessions.Authenticated{}, false
	}
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, false
	}
	return s.authenticateSession(w, r)
}

func (s *Server) affiliateProgramStatus() affiliateProgramResponse {
	mode := s.affiliateHTTP.SettlementMode
	if mode == "" {
		mode = "unconfigured"
	}
	return affiliateProgramResponse{EnrollmentOpen: s.affiliateHTTP.EnrollmentOpen,
		AttributionEnabled: s.affiliateHTTP.AttributionEnabled, TermsVersion: s.affiliateProgram.TermsVersion(),
		RuleVersion: s.affiliateProgram.RuleVersion(), SettlementMode: mode}
}
