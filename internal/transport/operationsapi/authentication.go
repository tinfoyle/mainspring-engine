package operationsapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

func (s *Server) WithAuthenticator(auth *operationsauth.Service, guard *abuse.Guard, appOrigin string) error {
	parsed, err := url.Parse(appOrigin)
	if auth == nil || guard == nil || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || appOrigin == s.origin {
		return errors.New("admin authenticator requires its service, rate limiter and exact HTTPS app origin")
	}
	s.auth, s.guard, s.appOrigin = auth, guard, appOrigin
	return nil
}
func (s *Server) flowCookieName() string {
	if s.cookie.Secure {
		return "__Host-spyglass_admin_login"
	}
	return "spyglass_development_admin_login"
}
func (s *Server) flowToken(r *http.Request) string {
	c, err := r.Cookie(s.flowCookieName())
	if err != nil {
		return ""
	}
	return c.Value
}
func (s *Server) clearFlow(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.flowCookieName(), Path: "/", HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
func (s *Server) authError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	code := "admin_login_failed"
	detail := "Sign-in could not be completed. Check your code or start again."
	if errors.Is(err, operationsauth.ErrLimited) {
		status = http.StatusTooManyRequests
		code = "admin_login_limited"
		detail = err.Error()
	} else if !errors.Is(err, operationsauth.ErrDenied) {
		status = http.StatusServiceUnavailable
		code = "admin_login_unavailable"
		detail = "Admin sign-in is temporarily unavailable."
		s.logger.Error("admin authentication failed", "error", err)
	}
	writeProblem(w, status, code, detail)
}
func (s *Server) beginGoogle(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	allowed, err := s.guard.Allow(r.Context(), abuse.ScopeLogin, actor, time.Now().UTC(), abuse.Policy{Limit: 20, Window: 15 * time.Minute})
	if err != nil {
		s.authError(w, err)
		return
	}
	if !allowed {
		s.authError(w, operationsauth.ErrLimited)
		return
	}
	token, err := s.auth.Begin(r.Context())
	if err != nil {
		s.authError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.flowCookieName(), Value: token, Path: "/", HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	writeJSON(w, http.StatusCreated, map[string]string{"url": s.appOrigin + "/auth/google/operations?state=" + url.QueryEscape(token)})
}
func (s *Server) acceptGoogle(w http.ResponseWriter, r *http.Request) {
	// This is the only cross-origin POST. The encrypted one-minute ticket must
	// also match a single-use challenge in this browser's host-only cookie.
	if r.Header.Get("Origin") != s.appOrigin {
		writeProblem(w, 403, "origin_denied", "Google handoff origin was not accepted")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || len(r.PostForm["ticket"]) != 1 {
		writeProblem(w, 400, "invalid_request", "Invalid Google handoff")
		return
	}
	if err := s.auth.Google(r.Context(), s.flowToken(r), r.PostForm.Get("ticket")); err != nil {
		s.clearFlow(w)
		http.Redirect(w, r, s.origin+"/?login=failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.origin+"/#admin-login", http.StatusSeeOther)
}
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.auth.Status(r.Context(), s.flowToken(r))
	if err != nil {
		s.authError(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) enrollAuthenticator(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	setup, err := s.auth.Setup(r.Context(), s.flowToken(r))
	if err != nil {
		s.authError(w, err)
		return
	}
	writeJSON(w, 200, setup)
}
func (s *Server) verifyAuthenticator(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Recovery bool   `json:"recovery"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	result, err := s.auth.Verify(r.Context(), s.flowToken(r), input.Code, input.Recovery)
	if err != nil {
		s.authError(w, err)
		return
	}
	if result.RecoveryOnly {
		writeJSON(w, 200, map[string]string{"stage": "enroll"})
		return
	}
	issued := result.Issued
	staff, err := s.console.RecordAuthentication(r.Context(), issued.Session.UserID, issued.Session.ID)
	if err != nil {
		_, _ = s.sessions.RevokeOwned(r.Context(), issued.Session.UserID, issued.Session.ID)
		s.authError(w, operationsauth.ErrDenied)
		return
	}
	s.clearFlow(w)
	s.setCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, 200, map[string]any{"session": map[string]any{"staff": staff, "expires_at": issued.Session.ExpiresAt, "authentication_method": issued.Session.AuthenticationMethod}, "recovery_codes": result.RecoveryCodes})
}
func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	authenticated, _, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.auth.Reauthenticate(r.Context(), authenticated.Session.UserID, authenticated.Session.ID, input.Code); err != nil {
		s.authError(w, err)
		return
	}
	w.WriteHeader(204)
}
func sensitiveAction(r *http.Request) bool {
	if r.Method != "POST" {
		return false
	}
	p := r.URL.Path
	return p == "/api/operations/v1/support-grants" || strings.HasSuffix(p, "/replays") || strings.HasSuffix(p, "/refreshes") || strings.HasSuffix(p, "/review-starts") || strings.HasSuffix(p, "/resolutions") || strings.HasSuffix(p, "/transitions")
}
