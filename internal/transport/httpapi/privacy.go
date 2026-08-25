package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const defaultPrivacyCookieName = "__Host-spyglass_privacy"

type PrivacyHTTPConfig struct {
	PublicOrigin string
	AppOrigin    string
	CookieName   string
	Secure       bool
}

type privacyConsentResponse struct {
	PolicyVersion   uint64          `json:"policy_version"`
	Surface         privacy.Surface `json:"surface"`
	Analytics       bool            `json:"analytics"`
	Marketing       bool            `json:"marketing"`
	Decided         bool            `json:"decided"`
	RenewalRequired bool            `json:"renewal_required"`
	EffectiveAt     *time.Time      `json:"effective_at,omitempty"`
}

func (s *Server) getPrivacyConsent(w http.ResponseWriter, r *http.Request) {
	surface, _, ok := s.privacyRequestContext(w, r, false)
	if !ok {
		return
	}
	response := privacyConsentResponse{PolicyVersion: s.privacyConsent.PolicyVersion(), Surface: surface}
	claims, ok := s.readPrivacyClaims(r, surface)
	if !ok {
		writeJSON(w, http.StatusOK, response)
		return
	}
	decision, err := s.privacyConsent.Current(r.Context(), claims.SubjectID, surface)
	if errors.Is(err, privacyconsent.ErrNotFound) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy preferences are temporarily unavailable")
		return
	}
	decision, replaced, ok := s.linkPrivatePrivacySubject(w, r, decision)
	if !ok {
		return
	}
	if replaced && !s.setPrivacyReferenceCookie(w, decision) {
		return
	}
	response.Decided = true
	response.Analytics = decision.Analytics
	response.Marketing = decision.Marketing
	response.RenewalRequired = decision.PolicyVersion != s.privacyConsent.PolicyVersion()
	response.EffectiveAt = &decision.EffectiveAt
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) setPrivacyConsent(w http.ResponseWriter, r *http.Request) {
	surface, _, ok := s.privacyRequestContext(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Analytics bool `json:"analytics"`
		Marketing bool `json:"marketing"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var subjectID ids.ConsentSubjectID
	if claims, valid := s.readPrivacyClaims(r, surface); valid {
		subjectID = claims.SubjectID
	}
	decision, err := s.privacyConsent.Set(r.Context(), privacyconsent.SetCommand{
		SubjectID: subjectID, Surface: surface, Analytics: input.Analytics, Marketing: input.Marketing,
	})
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy preferences could not be saved")
		return
	}
	decision, _, ok = s.linkPrivatePrivacySubject(w, r, decision)
	if !ok || !s.setPrivacyReferenceCookie(w, decision) {
		return
	}
	effective := decision.EffectiveAt
	writeJSON(w, http.StatusOK, privacyConsentResponse{PolicyVersion: decision.PolicyVersion, Surface: surface,
		Analytics: decision.Analytics, Marketing: decision.Marketing, Decided: true, EffectiveAt: &effective})
}

func (s *Server) linkPrivatePrivacySubject(w http.ResponseWriter, r *http.Request, decision privacy.Decision) (privacy.Decision, bool, bool) {
	if decision.Surface != privacy.SurfacePrivate || s.sessions == nil {
		return decision, false, true
	}
	sessionCookie, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return decision, false, true
	}
	authenticated, err := s.sessions.Authenticate(r.Context(), sessionCookie.Value)
	if err != nil {
		return decision, false, true
	}
	if authenticated.RotatedToken != "" {
		s.setSessionCookie(w, authenticated.RotatedToken, authenticated.Session.ExpiresAt)
	}
	err = s.privacyConsent.Link(r.Context(), decision.SubjectID, authenticated.Session.UserID)
	if err == nil {
		return decision, false, true
	}
	if !errors.Is(err, privacyconsent.ErrSubjectOwned) {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy preferences could not be associated with this identity")
		return privacy.Decision{}, false, false
	}
	replacement, err := s.privacyConsent.Set(r.Context(), privacyconsent.SetCommand{Surface: decision.Surface,
		Analytics: decision.Analytics, Marketing: decision.Marketing})
	if err == nil {
		err = s.privacyConsent.Link(r.Context(), replacement.SubjectID, authenticated.Session.UserID)
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy preferences could not be associated with this identity")
		return privacy.Decision{}, false, false
	}
	return replacement, true, true
}

func (s *Server) setPrivacyReferenceCookie(w http.ResponseWriter, decision privacy.Decision) bool {
	token, err := s.privacyTokens.Sign(privacy.PreferenceReference{SubjectID: decision.SubjectID,
		PolicyVersion: decision.PolicyVersion, Surface: decision.Surface})
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy preferences could not be saved")
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: s.privacyCookieName(), Value: token, Path: "/", HttpOnly: true,
		Secure: s.privacyHTTP.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60})
	return true
}

func (s *Server) getPrivacyConsentHistory(w http.ResponseWriter, r *http.Request) {
	surface, _, ok := s.privacyRequestContext(w, r, false)
	if !ok {
		return
	}
	claims, valid := s.readPrivacyClaims(r, surface)
	if !valid {
		writeJSON(w, http.StatusOK, map[string]any{"decisions": []privacy.Decision{}})
		return
	}
	decisions, err := s.privacyConsent.History(r.Context(), claims.SubjectID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unavailable", "privacy history is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

func (s *Server) erasePrivacyData(w http.ResponseWriter, r *http.Request) {
	surface, _, ok := s.privacyRequestContext(w, r, true)
	if !ok {
		return
	}
	if claims, valid := s.readPrivacyClaims(r, surface); valid {
		if err := s.privacyConsent.Erase(r.Context(), claims.SubjectID); err != nil {
			writeProblem(w, http.StatusServiceUnavailable, "privacy_erasure_failed", "privacy data could not be erased")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: s.privacyCookieName(), Value: "", Path: "/", HttpOnly: true,
		Secure: s.privacyHTTP.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ingestAnalyticsEvent(w http.ResponseWriter, r *http.Request) {
	surface, _, ok := s.privacyRequestContext(w, r, true)
	if !ok {
		return
	}
	claims, ok := s.readPrivacyClaims(r, surface)
	if !ok {
		writeProblem(w, http.StatusForbidden, "analytics_consent_required", "analytics consent is required")
		return
	}
	var input struct {
		EventID    ids.AnalyticsEventID `json:"event_id"`
		Name       analytics.EventName  `json:"name"`
		OccurredAt time.Time            `json:"occurred_at"`
		Fields     map[string]string    `json:"fields"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := s.analyticsIngest.Ingest(r.Context(), analytics.Envelope{ID: input.EventID, SubjectID: claims.SubjectID,
		Name: input.Name, Surface: surface, OccurredAt: input.OccurredAt, Fields: input.Fields})
	switch {
	case errors.Is(err, analytics.ErrConsentRequired):
		writeProblem(w, http.StatusForbidden, "analytics_consent_required", "analytics consent is required")
	case errors.Is(err, analytics.ErrUnknownEvent), errors.Is(err, analytics.ErrInvalidEvent),
		errors.Is(err, analytics.ErrProhibitedField), errors.Is(err, analytics.ErrUnsupportedField):
		writeProblem(w, http.StatusBadRequest, "invalid_analytics_event", "analytics event is not accepted")
	case err != nil:
		writeProblem(w, http.StatusServiceUnavailable, "analytics_unavailable", "analytics ingestion is temporarily unavailable")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) privacyRequestContext(w http.ResponseWriter, r *http.Request, mutation bool) (privacy.Surface, string, bool) {
	if s.privacyConsent == nil || s.analyticsIngest == nil || s.privacyTokens == nil {
		writeProblem(w, http.StatusServiceUnavailable, "privacy_unconfigured", "privacy preferences are not configured")
		return "", "", false
	}
	configured := []struct {
		origin  string
		surface privacy.Surface
	}{{s.privacyHTTP.PublicOrigin, privacy.SurfacePublic}, {s.privacyHTTP.AppOrigin, privacy.SurfacePrivate}}
	for _, candidate := range configured {
		parsed, err := url.Parse(candidate.origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		if strings.EqualFold(r.Host, parsed.Host) {
			if mutation && r.Header.Get("Origin") != strings.TrimSuffix(candidate.origin, "/") {
				writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
				return "", "", false
			}
			return candidate.surface, strings.TrimSuffix(candidate.origin, "/"), true
		}
	}
	writeProblem(w, http.StatusBadRequest, "privacy_surface_unknown", "privacy surface could not be determined")
	return "", "", false
}

func (s *Server) readPrivacyClaims(r *http.Request, surface privacy.Surface) (privacy.PreferenceReference, bool) {
	cookie, err := r.Cookie(s.privacyCookieName())
	if err != nil {
		return privacy.PreferenceReference{}, false
	}
	claims, err := s.privacyTokens.Verify(cookie.Value, surface)
	return claims, err == nil
}

func (s *Server) privacyCookieName() string {
	if s.privacyHTTP.CookieName != "" {
		return s.privacyHTTP.CookieName
	}
	return defaultPrivacyCookieName
}
