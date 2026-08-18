package browserapp

import (
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

//go:embed assets/*.css
var assets embed.FS

type VerificationTokenSource interface {
	Latest() (registration.VerificationMessage, bool)
}
type InvitationTokenSource interface {
	Latest() (invitations.Message, bool)
}
type RecoveryTokenSource interface {
	Latest() (recovery.Message, bool)
}

type Config struct {
	SessionCookieName       string
	AccountCookieName       string
	SecureCookies           bool
	TrustedOrigins          []string
	ExposeDevelopmentTokens bool
}

type Server struct {
	registrations      *registration.Service
	authentication     *authentication.Service
	sessions           *sessions.Service
	accounts           *accountaccess.Service
	invitations        *invitations.Service
	catalog            func() catalog.PublishedCatalog
	verificationTokens VerificationTokenSource
	invitationTokens   InvitationTokenSource
	config             Config
	logger             *slog.Logger
	templates          *template.Template
	commercial         *commercialaccess.Service
	recovery           *recovery.Service
	recoveryTokens     RecoveryTokenSource
}

type Option func(*Server)

func WithCommercialAccess(service *commercialaccess.Service) Option {
	return func(server *Server) { server.commercial = service }
}

func WithRecovery(service *recovery.Service, tokens RecoveryTokenSource) Option {
	return func(server *Server) {
		server.recovery = service
		server.recoveryTokens = tokens
	}
}

func New(registrations *registration.Service, authenticationService *authentication.Service, sessionService *sessions.Service, accountService *accountaccess.Service, invitationService *invitations.Service, catalogSource func() catalog.PublishedCatalog, verificationTokens VerificationTokenSource, invitationTokens InvitationTokenSource, config Config, logger *slog.Logger, options ...Option) (*Server, error) {
	if registrations == nil || authenticationService == nil || sessionService == nil || accountService == nil || invitationService == nil || catalogSource == nil || logger == nil {
		return nil, errors.New("browser application dependencies are required")
	}
	if config.SessionCookieName == "" {
		config.SessionCookieName = "__Host-spyglass_session"
	}
	if config.AccountCookieName == "" {
		config.AccountCookieName = "__Host-spyglass_account"
	}
	if len(config.TrustedOrigins) == 0 {
		return nil, errors.New("at least one trusted browser origin is required")
	}
	for _, origin := range config.TrustedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("trusted browser origins must be absolute origins without paths")
		}
		if config.SecureCookies && parsed.Scheme != "https" {
			return nil, errors.New("secure browser origins must use HTTPS")
		}
	}
	parsed, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		return nil, err
	}
	server := &Server{registrations: registrations, authentication: authenticationService, sessions: sessionService, accounts: accountService, invitations: invitationService, catalog: catalogSource, verificationTokens: verificationTokens, invitationTokens: invitationTokens, config: config, logger: logger, templates: parsed}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

func (s *Server) Handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/app", http.StatusSeeOther) })
	mux.HandleFunc("GET /assets/spyglass.css", s.styles)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /forgot-password", s.forgotPasswordPage)
	mux.HandleFunc("POST /forgot-password", s.forgotPassword)
	mux.HandleFunc("GET /reset-password", s.resetPasswordPage)
	mux.HandleFunc("POST /reset-password", s.resetPassword)
	mux.HandleFunc("GET /signup", s.signupPage)
	mux.HandleFunc("POST /signup", s.signup)
	mux.HandleFunc("GET /verify", s.verifyPage)
	mux.HandleFunc("POST /verify", s.verify)
	mux.HandleFunc("GET /app", s.app)
	mux.HandleFunc("GET /app/security", s.securityPage)
	mux.HandleFunc("POST /app/security/reauthenticate", s.reauthenticate)
	mux.HandleFunc("POST /app/security/sessions/revoke", s.revokeSession)
	mux.HandleFunc("POST /app/security/sessions/revoke-all", s.revokeAllSessions)
	mux.HandleFunc("POST /app/account", s.selectAccount)
	mux.HandleFunc("POST /app/invitations", s.createInvitation)
	mux.HandleFunc("POST /app/billing/checkout", s.startCheckout)
	mux.HandleFunc("POST /app/billing/portal", s.openBillingPortal)
	mux.HandleFunc("GET /invitations/accept", s.acceptInvitationPage)
	mux.HandleFunc("POST /invitations/accept", s.acceptInvitation)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("/", fallback)
	return s.securityHeaders(s.recover(mux))
}

func (s *Server) forgotPasswordPage(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, "forgot", pageData{Title: "Recover your identity"})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if s.recovery == nil {
		s.render(w, http.StatusServiceUnavailable, "forgot", pageData{Title: "Recover your identity", Error: "Credential recovery is temporarily unavailable."})
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		s.render(w, http.StatusForbidden, "forgot", pageData{Title: "Recover your identity", Error: "This recovery request could not be verified."})
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	result, err := s.recovery.Begin(r.Context(), recovery.BeginCommand{Email: r.FormValue("email"), NetworkActor: actor})
	if err != nil {
		s.logger.Error("begin credential recovery", "error", err)
	}
	data := pageData{Title: "Check your email", Notice: "If that email belongs to an Infinite Ocean identity, a recovery link is on its way.", Email: r.FormValue("email")}
	if s.config.ExposeDevelopmentTokens && result.Delivered && s.recoveryTokens != nil {
		if message, ok := s.recoveryTokens.Latest(); ok && message.RecoveryID == result.RecoveryID {
			data.DevelopmentToken = message.Token
		}
	}
	s.render(w, http.StatusAccepted, "forgot", data)
}

func (s *Server) resetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		s.render(w, http.StatusBadRequest, "reset", pageData{Title: "Set a new password", Error: "The recovery link is incomplete."})
		return
	}
	s.render(w, http.StatusOK, "reset", pageData{Title: "Set a new password", Token: token})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if s.recovery == nil {
		s.render(w, http.StatusServiceUnavailable, "reset", pageData{Title: "Set a new password", Error: "Credential recovery is temporarily unavailable."})
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		s.render(w, http.StatusForbidden, "reset", pageData{Title: "Set a new password", Error: "This password reset request could not be verified."})
		return
	}
	token := r.FormValue("token")
	err := s.recovery.Complete(r.Context(), recovery.CompleteCommand{Token: token, Password: r.FormValue("password")})
	if err != nil {
		s.render(w, http.StatusBadRequest, "reset", pageData{Title: "Set a new password", Token: token, Error: "The recovery link is invalid or expired, or the password does not meet the 12-character minimum."})
		return
	}
	s.clearCookies(w)
	http.Redirect(w, r, "/login?status=password_reset", http.StatusSeeOther)
}

func (s *Server) styles(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/app.css")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(raw)
	if extra, err := assets.ReadFile("assets/shell.css"); err == nil {
		_, _ = w.Write(extra)
	}
}

type pageData struct {
	Title, Page, Error, Notice, Email, Name, AccountName, Token, ReturnTo, DevelopmentToken string
	Choices                                                                                 []accountaccess.Choice
	Selected                                                                                *accountaccess.Choice
	Catalog                                                                                 catalog.PublishedCatalog
	PackageModes                                                                            map[catalog.PackageCode]catalog.PackageMode
	CanInvite                                                                               bool
	BillingConfigured, CanManageBilling, CanStartCheckout, HasBillingCustomer               bool
	BillingState, BillingPeriod, BillingSynced                                              string
	BillingPlans                                                                            []billingPlan
	ActiveSessions                                                                          []sessions.ActiveSession
	SecurityEvents                                                                          []securityEventView
}

type billingPlan struct {
	OfferCode, Name, Description, Price, Interval string
	PackageCount                                  int
	Current                                       bool
}

type securityEventView struct {
	Label      string
	Detail     string
	OccurredAt time.Time
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data pageData) {
	data.Page = name
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render browser page", "page", name, "error", err)
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(w, r); ok {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login", pageData{Title: "Sign in", Notice: loginNotice(r.URL.Query().Get("status")), Email: r.URL.Query().Get("email"), ReturnTo: safeReturnTo(r.URL.Query().Get("return_to"))})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		s.render(w, http.StatusForbidden, "login", pageData{Title: "Sign in", Error: "This sign-in request could not be verified."})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "login", pageData{Title: "Sign in", Error: "The sign-in form could not be read."})
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	issued, err := s.authentication.Login(r.Context(), authentication.LoginCommand{Email: r.FormValue("email"), Password: r.FormValue("password"), ClientLabel: r.UserAgent(), NetworkActor: actor})
	if err != nil {
		s.render(w, http.StatusUnauthorized, "login", pageData{Title: "Sign in", Error: "The email or password is incorrect.", Email: r.FormValue("email"), ReturnTo: safeReturnTo(r.FormValue("return_to"))})
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	target := safeReturnTo(r.FormValue("return_to"))
	if target == "" {
		target = "/app"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) signupPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "signup", pageData{Title: "Create your Account", Notice: signupNotice(r.URL.Query().Get("status")), Email: r.URL.Query().Get("email")})
}
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, true) {
		s.render(w, http.StatusForbidden, "signup", pageData{Title: "Create your Account", Error: "This signup request could not be verified."})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "signup", pageData{Title: "Create your Account", Error: "The signup form could not be read."})
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = r.FormValue("display_name")
	}
	result, err := s.registrations.Begin(r.Context(), registration.BeginCommand{Email: r.FormValue("email"), DisplayName: name, AccountName: r.FormValue("account_name"), Region: r.FormValue("region")})
	if err != nil {
		s.render(w, http.StatusBadRequest, "signup", pageData{Title: "Create your Account", Error: "We could not start this registration. Check the details or sign in if the email is already registered.", Email: r.FormValue("email"), Name: name, AccountName: r.FormValue("account_name")})
		return
	}
	data := pageData{Title: "Check your email", Notice: "Your verification link is on its way.", Email: r.FormValue("email")}
	if s.config.ExposeDevelopmentTokens && s.verificationTokens != nil {
		if message, ok := s.verificationTokens.Latest(); ok && message.RegistrationID == result.RegistrationID {
			data.DevelopmentToken = message.Token
		}
	}
	s.render(w, http.StatusAccepted, "signup", data)
}

func (s *Server) verifyPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Verify identity", Error: "The verification link is incomplete."})
		return
	}
	s.render(w, http.StatusOK, "verify", pageData{Title: "Secure your identity", Token: token})
}
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		s.render(w, http.StatusForbidden, "verify", pageData{Title: "Secure your identity", Error: "This verification request could not be verified."})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Secure your identity", Error: "The verification form could not be read."})
		return
	}
	_, err := s.registrations.Complete(r.Context(), registration.CompleteCommand{Token: r.FormValue("token"), Password: r.FormValue("password")})
	if err != nil {
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Secure your identity", Token: r.FormValue("token"), Error: "The link is invalid or expired, or the password does not meet the 12-character minimum."})
		return
	}
	http.Redirect(w, r, "/login?status=verified", http.StatusSeeOther)
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	choices, err := s.accounts.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		http.Error(w, "Accounts could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	selected := s.selectedChoice(r, choices)
	modes := map[catalog.PackageCode]catalog.PackageMode{}
	if selected != nil {
		for _, item := range selected.Entitlements.Packages {
			modes[item.Code] = item.Mode
		}
	}
	canInvite := selected != nil && (selected.Role == accounts.RoleOwner || selected.Role == accounts.RoleAdministrator)
	data := pageData{Title: "Spyglass", Choices: choices, Selected: selected, Catalog: s.catalog(), PackageModes: modes, CanInvite: canInvite, Notice: appNotice(r.URL.Query().Get("status")), DevelopmentToken: r.URL.Query().Get("development_token"), BillingConfigured: s.commercial != nil}
	if selected != nil {
		var status commercialaccess.Status
		if s.commercial != nil {
			status, err = s.commercial.Status(r.Context(), authenticated.Session.UserID, selected.AccountID)
			if err != nil {
				s.logger.Error("load billing status", "account_id", selected.AccountID, "error", err)
			}
			data.CanManageBilling, data.CanStartCheckout, data.HasBillingCustomer = status.CanManage, status.CanStartCheckout, status.HasCustomer
		}
		data.BillingPlans, data.BillingState, data.BillingPeriod, data.BillingSynced = billingView(data.Catalog, selected.AccountType, status, time.Now().UTC())
	}
	s.render(w, http.StatusOK, "app", data)
}

func (s *Server) selectAccount(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	if err := s.parseForm(w, r); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	accountID := r.FormValue("account_id")
	if ids.Validate(accountID) != nil {
		http.Error(w, "Invalid Account.", http.StatusBadRequest)
		return
	}
	if _, err := s.accounts.Select(r.Context(), authenticated.Session.UserID, ids.AccountID(accountID)); err != nil {
		http.Error(w, "Account access denied.", http.StatusForbidden)
		return
	}
	s.setAccountCookie(w, accountID)
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.sessions.RecentlyReauthenticated(authenticated.Session, 10*time.Minute) {
		http.Redirect(w, r, "/app/security?status=reauth_required", http.StatusSeeOther)
		return
	}
	if !s.validOrigin(r, false) {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	if err := s.parseForm(w, r); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	accountID := r.FormValue("account_id")
	if ids.Validate(accountID) != nil {
		http.Error(w, "Invalid Account.", http.StatusBadRequest)
		return
	}
	created, err := s.invitations.Create(r.Context(), invitations.CreateCommand{ActorUserID: authenticated.Session.UserID, AccountID: ids.AccountID(accountID), Email: r.FormValue("email"), Role: accounts.MembershipRole(r.FormValue("role"))})
	if err != nil {
		http.Redirect(w, r, "/app?status=invite_failed", http.StatusSeeOther)
		return
	}
	target := "/app?status=invited"
	if s.config.ExposeDevelopmentTokens && s.invitationTokens != nil {
		if message, ok := s.invitationTokens.Latest(); ok && message.InvitationID == created.InvitationID {
			target += "&development_token=" + url.QueryEscape(message.Token)
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) securityPage(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	active, err := s.sessions.Active(r.Context(), authenticated.Session.UserID, authenticated.Session.ID)
	if err != nil {
		http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	events, err := s.sessions.SecurityEvents(r.Context(), authenticated.Session.UserID, 50)
	if err != nil {
		http.Error(w, "Security history could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	notice := ""
	switch r.URL.Query().Get("status") {
	case "confirmed":
		notice = "Password confirmed. Sensitive actions are unlocked for 10 minutes."
	case "revoked":
		notice = "The selected session has been signed out."
	case "reauth_required":
		notice = "Confirm your password before continuing with a sensitive action."
	}
	s.render(w, http.StatusOK, "security", pageData{Title: "Identity security", Notice: notice, ActiveSessions: active, SecurityEvents: securityEventViews(events)})
}

func securityEventViews(events []sessions.SecurityEvent) []securityEventView {
	result := make([]securityEventView, 0, len(events))
	for _, event := range events {
		view := securityEventView{OccurredAt: event.OccurredAt, Detail: "Infinite Ocean identity"}
		switch event.Type {
		case sessions.EventSessionCreated:
			view.Label = "Signed in"
		case sessions.EventSessionReauthenticated:
			view.Label = "Password confirmed"
		case sessions.EventSessionRevoked:
			view.Label = "Session signed out"
		case sessions.EventSessionsRevoked:
			view.Label = "All sessions signed out"
		case sessions.EventCredentialRecovered:
			view.Label = "Password recovered"
			view.Detail = "Credential replaced and all sessions revoked"
		default:
			view.Label = "Security setting changed"
		}
		result = append(result, view)
	}
	return result
}

func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		s.render(w, http.StatusForbidden, "security", pageData{Title: "Identity security", Error: "This password confirmation request could not be verified."})
		return
	}
	err := s.authentication.Reauthenticate(r.Context(), authentication.ReauthenticateCommand{UserID: authenticated.Session.UserID, SessionID: authenticated.Session.ID, Password: r.FormValue("password")})
	if err != nil {
		active, _ := s.sessions.Active(r.Context(), authenticated.Session.UserID, authenticated.Session.ID)
		s.render(w, http.StatusUnauthorized, "security", pageData{Title: "Identity security", Error: "The password is incorrect.", ActiveSessions: active})
		return
	}
	http.Redirect(w, r, "/app/security?status=confirmed", http.StatusSeeOther)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	raw := r.FormValue("session_id")
	if ids.Validate(raw) != nil {
		http.Error(w, "Invalid session.", http.StatusBadRequest)
		return
	}
	revoked, err := s.sessions.RevokeOwned(r.Context(), authenticated.Session.UserID, ids.SessionID(raw))
	if err != nil || !revoked {
		http.Error(w, "Session could not be revoked.", http.StatusNotFound)
		return
	}
	if ids.SessionID(raw) == authenticated.Session.ID {
		s.clearCookies(w)
		http.Redirect(w, r, "/login?status=signed_out", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/app/security?status=revoked", http.StatusSeeOther)
}

func (s *Server) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	if err := s.sessions.RevokeAll(r.Context(), authenticated.Session.UserID); err != nil {
		http.Error(w, "Sessions could not be revoked.", http.StatusServiceUnavailable)
		return
	}
	s.clearCookies(w)
	http.Redirect(w, r, "/login?status=signed_out", http.StatusSeeOther)
}

func (s *Server) acceptInvitationPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(w, r); !ok {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape("/invitations/accept?token="+r.URL.Query().Get("token")), http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "accept", pageData{Title: "Join Account", Token: r.URL.Query().Get("token")})
}
func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	if err := s.parseForm(w, r); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return
	}
	membership, err := s.invitations.Accept(r.Context(), invitations.AcceptCommand{UserID: authenticated.Session.UserID, Token: r.FormValue("token")})
	if err != nil {
		s.render(w, http.StatusBadRequest, "accept", pageData{Title: "Join Account", Token: r.FormValue("token"), Error: "This invitation is invalid, expired, or belongs to another email address."})
		return
	}
	s.setAccountCookie(w, string(membership.AccountID))
	http.Redirect(w, r, "/app?status=joined", http.StatusSeeOther)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return
	}
	if authenticated, ok := s.currentSession(w, r); ok {
		_ = s.sessions.Revoke(r.Context(), authenticated.Session.ID)
	}
	s.clearCookies(w)
	http.Redirect(w, r, "/login?status=signed_out", http.StatusSeeOther)
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, bool) {
	cookie, err := r.Cookie(s.config.SessionCookieName)
	if err != nil {
		return sessions.Authenticated{}, false
	}
	authenticated, err := s.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		s.clearCookies(w)
		return sessions.Authenticated{}, false
	}
	if authenticated.RotatedToken != "" {
		s.setSessionCookie(w, authenticated.RotatedToken, authenticated.Session.ExpiresAt)
	}
	return authenticated, true
}
func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, bool) {
	authenticated, ok := s.currentSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
	return authenticated, ok
}
func (s *Server) selectedChoice(r *http.Request, choices []accountaccess.Choice) *accountaccess.Choice {
	selectedID := ""
	if cookie, err := r.Cookie(s.config.AccountCookieName); err == nil {
		selectedID = cookie.Value
	}
	for index := range choices {
		if string(choices[index].AccountID) == selectedID {
			return &choices[index]
		}
	}
	if len(choices) > 0 {
		return &choices[0]
	}
	return nil
}
func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: s.config.SessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}
func (s *Server) setAccountCookie(w http.ResponseWriter, accountID string) {
	http.SetCookie(w, &http.Cookie{Name: s.config.AccountCookieName, Value: accountID, Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode})
}
func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{s.config.SessionCookieName, s.config.AccountCookieName} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
	}
}
func (s *Server) validOrigin(r *http.Request, allowPublic bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for index, candidate := range s.config.TrustedOrigins {
		if origin == candidate && (allowPublic || index == 0) {
			return true
		}
	}
	return false
}
func safeReturnTo(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return ""
	}
	return value
}

func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	return r.ParseForm()
}
func loginNotice(status string) string {
	switch status {
	case "verified":
		return "Identity verified. Sign in to open Spyglass."
	case "signed_out":
		return "You have been signed out."
	case "password_reset":
		return "Password updated. Sign in again on every device."
	}
	return ""
}
func signupNotice(status string) string {
	if status == "sent" {
		return "Your verification link is on its way."
	}
	return ""
}
func appNotice(status string) string {
	switch status {
	case "invited":
		return "Invitation sent."
	case "joined":
		return "Account joined. Your workspace has been updated."
	case "invite_failed":
		return "The invitation could not be created."
	case "billing":
		return "Checkout returned. Spyglass is waiting for verified billing state."
	case "billing_cancelled":
		return "Checkout was cancelled. Your current access is unchanged."
	case "billing_failed":
		return "Billing could not be opened. Your current access is unchanged."
	case "billing_unavailable":
		return "Billing is not configured in this environment."
	}
	return ""
}
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("browser panic", "recovered", recovered)
				http.Error(w, "Spyglass could not complete this request.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
