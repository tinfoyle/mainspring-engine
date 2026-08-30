package browserapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

//go:embed assets/*.css assets/*.js
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
type ContactChangeTokenSource interface {
	LatestVerification(ids.UserID) (contactchange.Message, bool)
}

type GoogleIdentityProvider interface {
	AuthorizationURL(state, nonce, challenge, redirectURI string) (string, error)
	Exchange(context.Context, string, string, string, string) (oidcauth.Assertion, error)
}

type Config struct {
	SessionCookieName       string
	AccountCookieName       string
	SecureCookies           bool
	TrustedOrigins          []string
	ExposeDevelopmentTokens bool
}

type Server struct {
	registrations        *registration.Service
	authentication       *authentication.Service
	sessions             *sessions.Service
	accounts             *accountaccess.Service
	accountLifecycle     *accountlifecycle.Service
	members              *accountmembers.Service
	invitations          *invitations.Service
	catalog              func() catalog.PublishedCatalog
	verificationTokens   VerificationTokenSource
	invitationTokens     InvitationTokenSource
	config               Config
	logger               *slog.Logger
	templates            *template.Template
	commercial           *commercialaccess.Service
	recovery             *recovery.Service
	recoveryTokens       RecoveryTokenSource
	passkeys             *passkeys.Service
	recoveryCodes        *recoverycodes.Service
	contactChanges       *contactchange.Service
	contactChangeTokens  ContactChangeTokenSource
	mcpGrants            *mcpauth.Service
	accountExports       *accountexport.Service
	exportDownloads      *accountexport.DownloadService
	googleAuthentication *oidcauth.Service
	googleProvider       GoogleIdentityProvider
	googleIssuer         string
	googleRedirectURI    string
}

type Option func(*Server)

func WithCommercialAccess(service *commercialaccess.Service) Option {
	return func(server *Server) { server.commercial = service }
}

func WithAccountMembers(service *accountmembers.Service) Option {
	return func(server *Server) { server.members = service }
}

func WithAccountLifecycle(service *accountlifecycle.Service) Option {
	return func(server *Server) { server.accountLifecycle = service }
}

func WithRecovery(service *recovery.Service, tokens RecoveryTokenSource) Option {
	return func(server *Server) {
		server.recovery = service
		server.recoveryTokens = tokens
	}
}

func WithPasskeys(service *passkeys.Service) Option {
	return func(server *Server) { server.passkeys = service }
}

func WithRecoveryCodes(service *recoverycodes.Service) Option {
	return func(server *Server) { server.recoveryCodes = service }
}

func WithContactChanges(service *contactchange.Service, tokens ContactChangeTokenSource) Option {
	return func(server *Server) {
		server.contactChanges = service
		server.contactChangeTokens = tokens
	}
}

func WithMCPGrants(service *mcpauth.Service) Option {
	return func(server *Server) { server.mcpGrants = service }
}

func WithAccountExports(service *accountexport.Service, downloads *accountexport.DownloadService) Option {
	return func(server *Server) { server.accountExports, server.exportDownloads = service, downloads }
}

func WithGoogleLogin(service *oidcauth.Service, provider GoogleIdentityProvider, issuer, redirectURI string) Option {
	return func(server *Server) {
		server.googleAuthentication = service
		server.googleProvider = provider
		server.googleIssuer = strings.TrimSpace(issuer)
		server.googleRedirectURI = strings.TrimSpace(redirectURI)
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
	googleParts := 0
	for _, configured := range []bool{server.googleAuthentication != nil, server.googleProvider != nil, server.googleIssuer != "", server.googleRedirectURI != ""} {
		if configured {
			googleParts++
		}
	}
	if googleParts != 0 && googleParts != 4 {
		return nil, errors.New("Google login dependencies must be configured together")
	}
	if googleParts == 4 {
		redirect, redirectErr := url.Parse(server.googleRedirectURI)
		origin := server.config.TrustedOrigins[0]
		if redirectErr != nil || redirect.Scheme+"://"+redirect.Host != origin || redirect.Path != "/auth/google/callback" || redirect.RawQuery != "" || redirect.Fragment != "" {
			return nil, errors.New("Google login redirect must be the exact application callback")
		}
	}
	return server, nil
}

func (s *Server) Handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/app", http.StatusSeeOther) })
	mux.HandleFunc("GET /assets/spyglass.css", s.styles)
	mux.HandleFunc("GET /assets/work.js", s.workScript)
	mux.HandleFunc("GET /assets/attention.js", s.attentionScript)
	mux.HandleFunc("GET /assets/agents.js", s.agentScript)
	mux.HandleFunc("GET /assets/schedules.js", s.scheduleScript)
	mux.HandleFunc("GET /assets/knowledge.js", s.knowledgeScript)
	mux.HandleFunc("GET /assets/finance.js", s.financeScript)
	mux.HandleFunc("GET /assets/marketing.js", s.marketingScript)
	mux.HandleFunc("GET /assets/integrations.js", s.integrationsScript)
	mux.HandleFunc("GET /assets/passkeys.js", s.passkeyScript)
	mux.HandleFunc("GET /assets/privacy-analytics.js", s.privacyAnalyticsScript)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /auth/google", s.beginGoogleLogin)
	mux.HandleFunc("GET /auth/google/callback", s.completeGoogleLogin)
	mux.HandleFunc("GET /forgot-password", s.forgotPasswordPage)
	mux.HandleFunc("POST /forgot-password", s.forgotPassword)
	mux.HandleFunc("GET /reset-password", s.resetPasswordPage)
	mux.HandleFunc("POST /reset-password", s.resetPassword)
	mux.HandleFunc("GET /signup", s.signupPage)
	mux.HandleFunc("POST /signup", s.signup)
	mux.HandleFunc("GET /verify", s.verifyPage)
	mux.HandleFunc("POST /verify", s.verify)
	mux.HandleFunc("GET /contact-change/verify", s.contactChangeVerificationPage)
	mux.HandleFunc("POST /contact-change/verify", s.completeContactChange)
	mux.HandleFunc("GET /app", s.app)
	mux.HandleFunc("GET /app/work", s.workPage)
	mux.HandleFunc("GET /app/your-turn", s.yourTurnPage)
	mux.HandleFunc("GET /app/agents", s.agentsPage)
	mux.HandleFunc("GET /app/schedules", s.schedulesPage)
	mux.HandleFunc("GET /app/knowledge", s.knowledgePage)
	mux.HandleFunc("GET /app/finance", s.financePage)
	mux.HandleFunc("GET /app/marketing", s.marketingPage)
	mux.HandleFunc("GET /app/integrations", s.integrationsPage)
	mux.HandleFunc("GET /app/security", s.securityPage)
	mux.HandleFunc("POST /app/security/reauthenticate", s.reauthenticate)
	mux.HandleFunc("POST /app/security/contact-change", s.beginContactChange)
	mux.HandleFunc("POST /app/security/recovery-codes", s.rotateRecoveryCodes)
	mux.HandleFunc("POST /app/security/recovery-codes/consume", s.consumeRecoveryCode)
	mux.HandleFunc("POST /app/security/sessions/revoke", s.revokeSession)
	mux.HandleFunc("POST /app/security/sessions/revoke-all", s.revokeAllSessions)
	mux.HandleFunc("POST /app/security/mcp-grants/revoke", s.revokeMCPGrant)
	mux.HandleFunc("POST /app/security/google/connect", s.beginGoogleConnection)
	mux.HandleFunc("POST /app/security/google/disconnect", s.disconnectGoogle)
	mux.HandleFunc("POST /app/account", s.selectAccount)
	mux.HandleFunc("GET /app/account-closures", s.accountClosuresPage)
	mux.HandleFunc("GET /app/account-exports", s.accountExportsPage)
	mux.HandleFunc("POST /app/account-exports/request", s.requestAccountExport)
	mux.HandleFunc("POST /app/account-exports/cancel", s.cancelAccountExport)
	mux.HandleFunc("POST /app/account-exports/download", s.downloadAccountExport)
	mux.HandleFunc("POST /app/account-closures/request", s.requestAccountClosure)
	mux.HandleFunc("POST /app/account-closures/cancel", s.cancelAccountClosure)
	mux.HandleFunc("POST /app/invitations", s.createInvitation)
	mux.HandleFunc("POST /app/memberships/role", s.changeMembershipRole)
	mux.HandleFunc("POST /app/memberships/remove", s.removeMembership)
	mux.HandleFunc("POST /app/memberships/suspend", s.suspendMembership)
	mux.HandleFunc("POST /app/memberships/reactivate", s.reactivateMembership)
	mux.HandleFunc("POST /app/memberships/leave", s.leaveAccount)
	mux.HandleFunc("POST /app/ownership-transfer", s.transferOwnership)
	mux.HandleFunc("POST /app/billing/checkout", s.startCheckout)
	mux.HandleFunc("POST /app/billing/portal", s.openBillingPortal)
	mux.HandleFunc("GET /invitations/accept", s.acceptInvitationPage)
	mux.HandleFunc("POST /invitations/accept", s.acceptInvitation)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("/", fallback)
	return s.securityHeaders(s.recover(mux))
}

func (s *Server) forgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "forgot", pageData{Title: "Recover your identity", ReturnTo: safeReturnTo(r.URL.Query().Get("return_to"))})
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
	returnTo := safeReturnTo(r.FormValue("return_to"))
	result, err := s.recovery.Begin(r.Context(), recovery.BeginCommand{Email: r.FormValue("email"), NetworkActor: actor, ReturnTo: returnTo})
	if err != nil {
		s.logger.Error("begin credential recovery", "error", err)
	}
	data := pageData{Title: "Check your email", Notice: "If that email belongs to an Infinite Ocean identity, a recovery link is on its way.", Email: r.FormValue("email"), ReturnTo: returnTo}
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
	s.render(w, http.StatusOK, "reset", pageData{Title: "Set a new password", Token: token, ReturnTo: safeReturnTo(r.URL.Query().Get("return_to"))})
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
	returnTo := safeReturnTo(r.FormValue("return_to"))
	err := s.recovery.Complete(r.Context(), recovery.CompleteCommand{Token: token, Password: r.FormValue("password")})
	if err != nil {
		s.render(w, http.StatusBadRequest, "reset", pageData{Title: "Set a new password", Token: token, ReturnTo: returnTo, Error: "The recovery link is invalid or expired, or the password does not meet the 12-character minimum."})
		return
	}
	s.clearCookies(w)
	loginQuery := url.Values{"status": {"password_reset"}}
	if returnTo != "" {
		loginQuery.Set("return_to", returnTo)
	}
	http.Redirect(w, r, "/login?"+loginQuery.Encode(), http.StatusSeeOther)
}

func (s *Server) styles(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/app.css")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
	if extra, err := assets.ReadFile("assets/shell.css"); err == nil {
		_, _ = w.Write(extra)
	}
	if privacyStyles, err := assets.ReadFile("assets/privacy.css"); err == nil {
		_, _ = w.Write(privacyStyles)
	}
	if schedules, err := assets.ReadFile("assets/schedules.css"); err == nil {
		_, _ = w.Write(schedules)
	}
	if finance, err := assets.ReadFile("assets/finance.css"); err == nil {
		_, _ = w.Write(finance)
	}
	if marketing, err := assets.ReadFile("assets/marketing.css"); err == nil {
		_, _ = w.Write(marketing)
	}
	if integrations, err := assets.ReadFile("assets/integrations.css"); err == nil {
		_, _ = w.Write(integrations)
	}
}

func (s *Server) workScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/work.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) attentionScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/attention.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) agentScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/agents.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) scheduleScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/schedules.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) knowledgeScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/knowledge.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) financeScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/finance.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) marketingScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/marketing.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) integrationsScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/integrations.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) passkeyScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/passkeys.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

func (s *Server) privacyAnalyticsScript(w http.ResponseWriter, _ *http.Request) {
	raw, err := assets.ReadFile("assets/privacy-analytics.js")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(raw)
}

type pageData struct {
	Title, Page, Error, Notice, Email, Name, AccountName, Token, ReturnTo, DevelopmentToken, OfferCode string
	CurrentEmail, NewEmail                                                                             string
	ActorUserID                                                                                        ids.UserID
	Choices                                                                                            []accountaccess.Choice
	Selected                                                                                           *accountaccess.Choice
	Catalog                                                                                            catalog.PublishedCatalog
	PackageModes                                                                                       map[catalog.PackageCode]catalog.PackageMode
	CanInvite                                                                                          bool
	CanManageMembers, CanTransferOwnership, CanLeaveAccount                                            bool
	CanCloseAccount                                                                                    bool
	Closures                                                                                           []accountlifecycle.Status
	Exports                                                                                            []accountexport.Status
	CanManageExports                                                                                   bool
	Members                                                                                            []memberView
	ActorMembershipVersion                                                                             uint64
	BillingConfigured, CanManageBilling, CanStartCheckout, HasBillingCustomer                          bool
	BillingState, BillingPeriod, BillingSynced                                                         string
	BillingPlans                                                                                       []billingPlan
	ActiveSessions                                                                                     []sessions.ActiveSession
	SecurityEvents                                                                                     []securityEventView
	Passkeys                                                                                           []passkeys.CredentialSummary
	PasskeysConfigured                                                                                 bool
	GoogleConfigured, GoogleConnected                                                                  bool
	RecoveryCodeStatus                                                                                 recoverycodes.Status
	RecoveryCodes                                                                                      []string
	RecoveryCodesConfigured                                                                            bool
	ContactChangesConfigured                                                                           bool
	MCPGrantsConfigured                                                                                bool
	MCPGrants                                                                                          []mcpauth.GrantSummary
	OwnerEnrollmentRequired                                                                            bool
	WorkMode                                                                                           catalog.PackageMode
	WorkAvailable, WorkReadOnly                                                                        bool
	AgentsMode                                                                                         catalog.PackageMode
	AgentsAvailable, AgentsReadOnly                                                                    bool
	KnowledgeAvailable, KnowledgeReadOnly                                                              bool
	FinanceAvailable, FinanceReadOnly                                                                  bool
	MarketingAvailable, MarketingReadOnly                                                              bool
	IntegrationsAvailable, IntegrationsReadOnly                                                        bool
	AttentionAvailable, ApprovalsAvailable                                                             bool
	Script                                                                                             string
	PrivacyControls                                                                                    bool
	AnalyticsEvent, AnalyticsEventSecond, AnalyticsEventID, AnalyticsEventSecondID                     string
	AnalyticsOccurredAt, AnalyticsMethod                                                               string
}

type billingPlan struct {
	OfferCode, Name, Description, Price, Interval string
	PackageCount                                  int
	Current, Selected                             bool
}

type securityEventView struct {
	Label      string
	Detail     string
	OccurredAt time.Time
}

type memberView struct {
	accountmembers.Member
	CanChangeRole, CanRemove, CanTransfer, CanSuspend, CanReactivate, IsSelf bool
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data pageData) {
	data.Page = name
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render browser page", "page", name, "error", err)
	}
}

func setAnalyticsMarkers(data *pageData, deliveryID string, occurredAt time.Time, names ...string) {
	if data == nil || ids.Validate(deliveryID) != nil || len(names) == 0 || names[0] == "" {
		return
	}
	firstID, err := ids.Derive(deliveryID, "analytics/"+names[0])
	if err != nil {
		return
	}
	data.AnalyticsEvent = names[0]
	data.AnalyticsEventID = firstID
	data.AnalyticsOccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
	if len(names) < 2 || names[1] == "" {
		return
	}
	secondID, err := ids.Derive(deliveryID, "analytics/"+names[1])
	if err != nil {
		return
	}
	data.AnalyticsEventSecond = names[1]
	data.AnalyticsEventSecondID = secondID
}

func newAnalyticsMarkers(data *pageData, occurredAt time.Time, names ...string) {
	setAnalyticsMarkers(data, ids.RandomGenerator{}.New(), occurredAt, names...)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(w, r); ok {
		target := safeReturnTo(r.URL.Query().Get("return_to"))
		if target == "" {
			target = "/app"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	data := pageData{Title: "Sign in", Notice: loginNotice(r.URL.Query().Get("status")), Email: r.URL.Query().Get("email"), ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")), PasskeysConfigured: s.passkeys != nil, GoogleConfigured: s.googleProvider != nil, PrivacyControls: true}
	if r.URL.Query().Get("status") == "verified" {
		seconds, parseErr := strconv.ParseInt(r.URL.Query().Get("analytics_at"), 10, 64)
		occurredAt := time.Unix(seconds, 0).UTC()
		if parseErr == nil && occurredAt.After(time.Now().UTC().Add(-24*time.Hour)) && occurredAt.Before(time.Now().UTC().Add(5*time.Minute)) {
			setAnalyticsMarkers(&data, r.URL.Query().Get("analytics_delivery"), occurredAt, "verification_completed", "account_created")
		}
	}
	if data.PasskeysConfigured {
		data.Script = "/assets/passkeys.js"
	}
	s.render(w, http.StatusOK, "login", data)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		s.render(w, http.StatusForbidden, "login", pageData{Title: "Sign in", Error: "This sign-in request could not be verified.", PrivacyControls: true})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "login", pageData{Title: "Sign in", Error: "The sign-in form could not be read.", PrivacyControls: true})
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	issued, err := s.authentication.Login(r.Context(), authentication.LoginCommand{Email: r.FormValue("email"), Password: r.FormValue("password"), ClientLabel: r.UserAgent(), NetworkActor: actor})
	if err != nil {
		s.render(w, http.StatusUnauthorized, "login", pageData{Title: "Sign in", Error: "The email or password is incorrect.", Email: r.FormValue("email"), ReturnTo: safeReturnTo(r.FormValue("return_to")), PrivacyControls: true})
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	target := safeReturnTo(r.FormValue("return_to"))
	if target == "" {
		target = "/app"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

const googleFlowCookie = "__Host-spyglass_google_flow"

type googleFlow struct {
	State, Nonce, Verifier, Mode, ReturnTo string
}

func (s *Server) beginGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if s.googleProvider == nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.currentSession(w, r); ok {
		target := safeReturnTo(r.URL.Query().Get("return_to"))
		if target == "" {
			target = "/app"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	s.beginGoogleFlow(w, r, "login", safeReturnTo(r.URL.Query().Get("return_to")))
}

func (s *Server) beginGoogleConnection(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.googleProvider == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Google connection request was not accepted.", http.StatusForbidden)
		return
	}
	if err := strongauth.Require(authenticated.Session, authenticated.Session.UserID, time.Now().UTC()); err != nil {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	s.beginGoogleFlow(w, r, "connect", "/app/security")
}

func (s *Server) beginGoogleFlow(w http.ResponseWriter, r *http.Request, mode, returnTo string) {
	state, err := googleRandom()
	if err != nil {
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	nonce, err := googleRandom()
	if err != nil {
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	verifier, err := googleRandom()
	if err != nil {
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	flow := googleFlow{State: state, Nonce: nonce, Verifier: verifier, Mode: mode, ReturnTo: safeReturnTo(returnTo)}
	encoded, err := json.Marshal(flow)
	if err != nil {
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: googleFlowCookie, Value: base64.RawURLEncoding.EncodeToString(encoded), Path: "/", HttpOnly: true,
		Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600, Expires: time.Now().UTC().Add(10 * time.Minute)})
	destination, err := s.googleProvider.AuthorizationURL(state, nonce, challenge, s.googleRedirectURI)
	if err != nil {
		s.clearGoogleFlow(w)
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func (s *Server) completeGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if s.googleProvider == nil {
		http.NotFound(w, r)
		return
	}
	flow, ok := s.googleFlow(r)
	s.clearGoogleFlow(w)
	state := r.URL.Query().Get("state")
	if !ok || len(state) != len(flow.State) || subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 || r.URL.Query().Get("error") != "" {
		s.googleFailure(w, r, flow.Mode)
		return
	}
	assertion, err := s.googleProvider.Exchange(r.Context(), r.URL.Query().Get("code"), flow.Verifier, s.googleRedirectURI, flow.Nonce)
	if err != nil {
		s.logger.Warn("Google login exchange rejected", "error", err)
		s.googleFailure(w, r, flow.Mode)
		return
	}
	if flow.Mode == "connect" {
		authenticated, authenticatedOK := s.currentSession(w, r)
		if !authenticatedOK {
			http.Redirect(w, r, "/login?return_to=%2Fapp%2Fsecurity", http.StatusSeeOther)
			return
		}
		if err := s.googleAuthentication.Connect(r.Context(), authenticated.Session, assertion); err != nil {
			if errors.Is(err, strongauth.ErrRequired) {
				http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
				return
			}
			s.logger.Warn("connect Google identity rejected", "error", err)
			http.Redirect(w, r, "/app/security?status=google_failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/security?status=google_connected", http.StatusSeeOther)
		return
	}
	if flow.Mode != "login" {
		s.googleFailure(w, r, flow.Mode)
		return
	}
	issued, err := s.googleAuthentication.Login(r.Context(), assertion, r.UserAgent())
	if errors.Is(err, oidcauth.ErrIdentityNotFound) {
		http.Redirect(w, r, "/login?status=google_not_connected", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.logger.Warn("Google identity login rejected", "error", err)
		http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	target := safeReturnTo(flow.ReturnTo)
	if target == "" {
		target = "/app"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) disconnectGoogle(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.googleAuthentication == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Google disconnect request was not accepted.", http.StatusForbidden)
		return
	}
	err := s.googleAuthentication.Disconnect(r.Context(), authenticated.Session, s.googleIssuer)
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.logger.Warn("disconnect Google identity rejected", "error", err)
		http.Redirect(w, r, "/app/security?status=google_failed", http.StatusSeeOther)
		return
	}
	_ = s.sessions.RevokeAll(r.Context(), authenticated.Session.UserID)
	s.clearCookies(w)
	http.Redirect(w, r, "/login?status=google_disconnected", http.StatusSeeOther)
}

func (s *Server) googleFlow(r *http.Request) (googleFlow, bool) {
	cookie, err := r.Cookie(googleFlowCookie)
	if err != nil || len(cookie.Value) > 4096 {
		return googleFlow{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return googleFlow{}, false
	}
	var flow googleFlow
	if json.Unmarshal(raw, &flow) != nil || !validGoogleFlow(flow) {
		return googleFlow{}, false
	}
	return flow, true
}

func (s *Server) clearGoogleFlow(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: googleFlowCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.config.SecureCookies,
		SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func (s *Server) googleFailure(w http.ResponseWriter, r *http.Request, mode string) {
	if mode == "connect" {
		http.Redirect(w, r, "/app/security?status=google_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login?status=google_failed", http.StatusSeeOther)
}

func googleRandom() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func validGoogleFlow(flow googleFlow) bool {
	valid := func(value string) bool { return len(value) == 43 && !strings.ContainsAny(value, "\r\n\t ") }
	return valid(flow.State) && valid(flow.Nonce) && valid(flow.Verifier) && (flow.Mode == "login" || flow.Mode == "connect") && safeReturnTo(flow.ReturnTo) == flow.ReturnTo
}

func (s *Server) signupPage(w http.ResponseWriter, r *http.Request) {
	offerCode := availableOfferCode(s.catalog(), r.URL.Query().Get("offer"), time.Now().UTC())
	s.render(w, http.StatusOK, "signup", pageData{Title: "Create your Account", Notice: signupNotice(r.URL.Query().Get("status")), Email: r.URL.Query().Get("email"), ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")), OfferCode: offerCode, PrivacyControls: true})
}
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		s.render(w, http.StatusForbidden, "signup", pageData{Title: "Create your Account", Error: "This signup request could not be verified.", PrivacyControls: true})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "signup", pageData{Title: "Create your Account", Error: "The signup form could not be read.", PrivacyControls: true})
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = r.FormValue("display_name")
	}
	offerCode := availableOfferCode(s.catalog(), r.FormValue("offer_code"), time.Now().UTC())
	returnTo := safeReturnTo(r.FormValue("return_to"))
	result, err := s.registrations.Begin(r.Context(), registration.BeginCommand{Email: r.FormValue("email"), DisplayName: name, AccountName: r.FormValue("account_name"), Region: r.FormValue("region"), OfferCode: offerCode, ReturnTo: returnTo})
	if err != nil {
		s.render(w, http.StatusBadRequest, "signup", pageData{Title: "Create your Account", Error: "We could not start this registration. Check the details or sign in if the email is already registered.", Email: r.FormValue("email"), Name: name, AccountName: r.FormValue("account_name"), ReturnTo: returnTo, OfferCode: offerCode, PrivacyControls: true})
		return
	}
	data := pageData{Title: "Check your email", Notice: "Your verification link is on its way.", Email: r.FormValue("email"), ReturnTo: returnTo, OfferCode: offerCode, PrivacyControls: true}
	newAnalyticsMarkers(&data, time.Now().UTC(), "registration_started")
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
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Verify identity", Error: "The verification link is incomplete.", PrivacyControls: true})
		return
	}
	offerCode := availableOfferCode(s.catalog(), r.URL.Query().Get("offer"), time.Now().UTC())
	s.render(w, http.StatusOK, "verify", pageData{Title: "Secure your identity", Token: token, ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")), OfferCode: offerCode, PrivacyControls: true})
}
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r, false) {
		s.render(w, http.StatusForbidden, "verify", pageData{Title: "Secure your identity", Error: "This verification request could not be verified.", PrivacyControls: true})
		return
	}
	if err := s.parseForm(w, r); err != nil {
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Secure your identity", Error: "The verification form could not be read.", PrivacyControls: true})
		return
	}
	offerCode := availableOfferCode(s.catalog(), r.FormValue("offer_code"), time.Now().UTC())
	returnTo := safeReturnTo(r.FormValue("return_to"))
	_, err := s.registrations.Complete(r.Context(), registration.CompleteCommand{Token: r.FormValue("token"), Password: r.FormValue("password")})
	if err != nil {
		s.logger.Error("complete browser registration", "error", err)
		s.render(w, http.StatusBadRequest, "verify", pageData{Title: "Secure your identity", Token: r.FormValue("token"), ReturnTo: returnTo, OfferCode: offerCode, Error: "The link is invalid or expired, or the password does not meet the 12-character minimum.", PrivacyControls: true})
		return
	}
	loginQuery := url.Values{"status": {"verified"}}
	loginQuery.Set("analytics_delivery", ids.RandomGenerator{}.New())
	loginQuery.Set("analytics_at", strconv.FormatInt(time.Now().UTC().Unix(), 10))
	if returnTo != "" {
		loginQuery.Set("return_to", returnTo)
	} else if offerCode != "" {
		loginQuery.Set("return_to", "/app?offer="+url.QueryEscape(offerCode)+"&status=welcome#billing")
	}
	http.Redirect(w, r, "/login?"+loginQuery.Encode(), http.StatusSeeOther)
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	data, authenticated, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Notice = appNotice(r.URL.Query().Get("status"))
	data.OfferCode = availableOfferCode(data.Catalog, r.URL.Query().Get("offer"), time.Now().UTC())
	data.DevelopmentToken = r.URL.Query().Get("development_token")
	data.BillingConfigured = s.commercial != nil
	if data.Selected != nil && !data.OwnerEnrollmentRequired {
		if s.members != nil {
			current, err := s.members.Current(r.Context(), authenticated.Session.UserID, data.Selected.AccountID)
			if err != nil {
				s.logger.Error("load current Account Membership", "account_id", data.Selected.AccountID, "error", err)
			} else {
				data.ActorMembershipVersion = current.Version
				data.CanLeaveAccount = current.Role != accounts.RoleOwner
			}
		}
		if s.members != nil && (data.Selected.Role == accounts.RoleOwner || data.Selected.Role == accounts.RoleAdministrator) {
			members, err := s.members.List(r.Context(), authenticated.Session.UserID, data.Selected.AccountID)
			if err != nil {
				s.logger.Error("load Account Memberships", "account_id", data.Selected.AccountID, "error", err)
			} else {
				data.CanManageMembers = true
				data.CanTransferOwnership = data.Selected.Role == accounts.RoleOwner
				for _, member := range members {
					view := memberView{Member: member, IsSelf: member.UserID == authenticated.Session.UserID}
					view.CanChangeRole = member.State == accounts.MembershipActive && data.Selected.Role == accounts.RoleOwner && member.Role != accounts.RoleOwner
					view.CanRemove = member.Role != accounts.RoleOwner && (data.Selected.Role == accounts.RoleOwner || member.Role != accounts.RoleAdministrator)
					view.CanTransfer = member.State == accounts.MembershipActive && data.Selected.Role == accounts.RoleOwner && member.Role != accounts.RoleOwner
					view.CanSuspend = member.State == accounts.MembershipActive && member.Role != accounts.RoleOwner && (data.Selected.Role == accounts.RoleOwner || member.Role != accounts.RoleAdministrator)
					view.CanReactivate = member.State == accounts.MembershipSuspended && member.Role != accounts.RoleOwner && (data.Selected.Role == accounts.RoleOwner || member.Role != accounts.RoleAdministrator)
					data.Members = append(data.Members, view)
					if view.IsSelf {
						data.ActorMembershipVersion = member.Version
					}
				}
			}
		}
		data.CanCloseAccount = s.accountLifecycle != nil && data.Selected.Role == accounts.RoleOwner
		var status commercialaccess.Status
		if s.commercial != nil {
			var err error
			status, err = s.commercial.Status(r.Context(), authenticated.Session.UserID, data.Selected.AccountID)
			if err != nil {
				s.logger.Error("load billing status", "account_id", data.Selected.AccountID, "error", err)
			}
			data.CanManageBilling, data.CanStartCheckout, data.HasBillingCustomer = status.CanManage, status.CanStartCheckout, status.HasCustomer
		}
		data.BillingPlans, data.BillingState, data.BillingPeriod, data.BillingSynced = billingView(data.Catalog, data.Selected.AccountType, status, data.OfferCode, time.Now().UTC())
	}
	if data.Selected != nil && data.OwnerEnrollmentRequired {
		data.BillingPlans, data.BillingState, data.BillingPeriod, data.BillingSynced = billingView(data.Catalog, data.Selected.AccountType, commercialaccess.Status{}, data.OfferCode, time.Now().UTC())
	}
	s.render(w, http.StatusOK, "app", data)
}

func (s *Server) accountClosuresPage(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.accountLifecycle == nil {
		http.Error(w, "Account lifecycle management is unavailable.", http.StatusServiceUnavailable)
		return
	}
	choices, err := s.accounts.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		http.Error(w, "Accounts could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	closures, err := s.accountLifecycle.ListOwned(r.Context(), authenticated.Session.UserID)
	if err != nil {
		http.Error(w, "Account closure history could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	s.render(w, http.StatusOK, "closures", pageData{Title: "Account lifecycle", Choices: choices, Selected: s.selectedChoice(r, choices), Closures: closures, Notice: closureNotice(r.URL.Query().Get("status"))})
}

func (s *Server) requestAccountClosure(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.accountLifecycle == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Account closure request was not accepted.", http.StatusForbidden)
		return
	}
	accountID := r.FormValue("account_id")
	version, err := strconv.ParseUint(r.FormValue("account_version"), 10, 64)
	if ids.Validate(accountID) != nil || err != nil || version == 0 || r.FormValue("confirmation") != "CLOSE" {
		http.Error(w, "Account closure request was invalid.", http.StatusBadRequest)
		return
	}
	_, err = s.accountLifecycle.Request(r.Context(), accountlifecycle.RequestCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(accountID), ExpectedAccountVersion: version, Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectAccountLifecycleError(w, r, err)
		return
	}
	s.clearAccountCookie(w)
	http.Redirect(w, r, "/app/account-closures?status=closure_requested", http.StatusSeeOther)
}

func (s *Server) cancelAccountClosure(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.accountLifecycle == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Account restoration request was not accepted.", http.StatusForbidden)
		return
	}
	accountID := r.FormValue("account_id")
	version, err := strconv.ParseUint(r.FormValue("account_version"), 10, 64)
	if ids.Validate(accountID) != nil || err != nil || version == 0 || r.FormValue("confirmation") != "RESTORE" {
		http.Error(w, "Account restoration request was invalid.", http.StatusBadRequest)
		return
	}
	_, err = s.accountLifecycle.Cancel(r.Context(), accountlifecycle.CancelCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(accountID), ExpectedAccountVersion: version, Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectAccountLifecycleError(w, r, err)
		return
	}
	s.setAccountCookie(w, accountID)
	http.Redirect(w, r, "/app?status=closure_canceled", http.StatusSeeOther)
}

func (s *Server) redirectAccountLifecycleError(w http.ResponseWriter, r *http.Request, err error) {
	if access.IsDenied(err, access.DenialOwnerEnrollment) {
		http.Redirect(w, r, "/app/security?status=owner_enrollment_required", http.StatusSeeOther)
		return
	}
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	status := "closure_failed"
	if errors.Is(err, accountlifecycle.ErrBillingActive) {
		status = "closure_billing_active"
	} else if errors.Is(err, accountlifecycle.ErrVersionConflict) || errors.Is(err, accountlifecycle.ErrStateConflict) {
		status = "closure_conflict"
	}
	http.Redirect(w, r, "/app/account-closures?status="+status, http.StatusSeeOther)
}

func (s *Server) workPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Work"
	if data.WorkAvailable {
		data.Script = "/assets/work.js"
	}
	s.render(w, http.StatusOK, "work", data)
}

func (s *Server) yourTurnPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Your Turn"
	if data.AttentionAvailable {
		data.Script = "/assets/attention.js"
	}
	s.render(w, http.StatusOK, "your-turn", data)
}

func (s *Server) agentsPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Agents"
	if data.AgentsAvailable {
		data.Script = "/assets/agents.js"
	}
	s.render(w, http.StatusOK, "agents", data)
}

func (s *Server) schedulesPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Schedules"
	if data.AgentsAvailable {
		data.Script = "/assets/schedules.js"
	}
	s.render(w, http.StatusOK, "schedules", data)
}

func (s *Server) knowledgePage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Knowledge"
	if data.KnowledgeAvailable {
		data.Script = "/assets/knowledge.js"
	}
	s.render(w, http.StatusOK, "knowledge", data)
}

func (s *Server) financePage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Finance"
	if data.FinanceAvailable {
		data.Script = "/assets/finance.js"
	}
	s.render(w, http.StatusOK, "finance", data)
}

func (s *Server) marketingPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Marketing"
	if data.MarketingAvailable {
		data.Script = "/assets/marketing.js"
	}
	s.render(w, http.StatusOK, "marketing", data)
}

func (s *Server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := s.appPageData(w, r)
	if !ok {
		return
	}
	data.Title = "Integrations"
	if data.IntegrationsAvailable {
		data.Script = "/assets/integrations.js"
	}
	s.render(w, http.StatusOK, "integrations", data)
}

func (s *Server) appPageData(w http.ResponseWriter, r *http.Request) (pageData, sessions.Authenticated, bool) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return pageData{}, sessions.Authenticated{}, false
	}
	choices, err := s.accounts.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		http.Error(w, "Accounts could not be loaded.", http.StatusServiceUnavailable)
		return pageData{}, sessions.Authenticated{}, false
	}
	selected := s.selectedChoice(r, choices)
	modes := map[catalog.PackageCode]catalog.PackageMode{}
	if selected != nil {
		for _, item := range selected.Entitlements.Packages {
			modes[item.Code] = item.Mode
		}
	}
	workMode := modes[catalog.PackageWork]
	agentsMode := modes[catalog.PackageAgents]
	knowledgeMode := modes[catalog.PackageKnowledge]
	financeMode := modes[catalog.PackageFinance]
	marketingMode := modes[catalog.PackageMarketing]
	integrationsMode := modes[catalog.PackageIntegrations]
	canApprove := selected != nil && (selected.Role == accounts.RoleOwner || selected.Role == accounts.RoleAdministrator)
	data := pageData{
		Title:                   "Spyglass",
		Choices:                 choices,
		Selected:                selected,
		ActorUserID:             authenticated.Session.UserID,
		Catalog:                 s.catalog(),
		PackageModes:            modes,
		CanInvite:               selected != nil && !selected.OwnerEnrollmentRequired && (selected.Role == accounts.RoleOwner || selected.Role == accounts.RoleAdministrator),
		OwnerEnrollmentRequired: selected != nil && selected.OwnerEnrollmentRequired,
		WorkMode:                workMode,
		WorkAvailable:           workMode == catalog.ModeEnabled || workMode == catalog.ModeReadOnly,
		WorkReadOnly:            workMode == catalog.ModeReadOnly,
		AgentsMode:              agentsMode,
		AgentsAvailable:         agentsMode == catalog.ModeEnabled || agentsMode == catalog.ModeReadOnly,
		AgentsReadOnly:          agentsMode == catalog.ModeReadOnly,
		KnowledgeAvailable:      knowledgeMode == catalog.ModeEnabled || knowledgeMode == catalog.ModeReadOnly,
		KnowledgeReadOnly:       knowledgeMode == catalog.ModeReadOnly,
		FinanceAvailable:        financeMode == catalog.ModeEnabled || financeMode == catalog.ModeReadOnly,
		FinanceReadOnly:         financeMode == catalog.ModeReadOnly,
		MarketingAvailable:      marketingMode == catalog.ModeEnabled || marketingMode == catalog.ModeReadOnly,
		MarketingReadOnly:       marketingMode == catalog.ModeReadOnly,
		IntegrationsAvailable:   integrationsMode == catalog.ModeEnabled || integrationsMode == catalog.ModeReadOnly,
		IntegrationsReadOnly:    integrationsMode == catalog.ModeReadOnly,
		ApprovalsAvailable:      canApprove && (agentsMode == catalog.ModeEnabled || agentsMode == catalog.ModeReadOnly),
		AttentionAvailable:      workMode == catalog.ModeEnabled || workMode == catalog.ModeReadOnly || (canApprove && (agentsMode == catalog.ModeEnabled || agentsMode == catalog.ModeReadOnly)),
	}
	return data, authenticated, true
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
		if access.IsDenied(err, access.DenialOwnerEnrollment) {
			http.Redirect(w, r, "/app/security?status=owner_enrollment_required", http.StatusSeeOther)
			return
		}
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
	created, err := s.invitations.Create(r.Context(), invitations.CreateCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(accountID), Email: r.FormValue("email"), Role: accounts.MembershipRole(r.FormValue("role"))})
	if err != nil {
		if access.IsDenied(err, access.DenialOwnerEnrollment) {
			http.Redirect(w, r, "/app/security?status=owner_enrollment_required", http.StatusSeeOther)
			return
		}
		if errors.Is(err, strongauth.ErrRequired) {
			http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
			return
		}
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

func (s *Server) changeMembershipRole(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, membershipID, version, ok := s.membershipForm(w, r)
	if !ok {
		return
	}
	_, err := s.members.ChangeRole(r.Context(), accountmembers.ChangeRoleCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: membershipID, ExpectedVersion: version, Role: accounts.MembershipRole(r.FormValue("role")), Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectMembershipError(w, r, err)
		return
	}
	http.Redirect(w, r, "/app?status=member_role_changed#settings", http.StatusSeeOther)
}

func (s *Server) removeMembership(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, membershipID, version, ok := s.membershipForm(w, r)
	if !ok {
		return
	}
	err := s.members.Remove(r.Context(), accountmembers.RemoveCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: membershipID, ExpectedVersion: version, Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectMembershipError(w, r, err)
		return
	}
	http.Redirect(w, r, "/app?status=member_removed#settings", http.StatusSeeOther)
}

func (s *Server) suspendMembership(w http.ResponseWriter, r *http.Request) {
	s.changeMembershipState(w, r, true)
}

func (s *Server) reactivateMembership(w http.ResponseWriter, r *http.Request) {
	s.changeMembershipState(w, r, false)
}

func (s *Server) changeMembershipState(w http.ResponseWriter, r *http.Request, suspend bool) {
	authenticated, accountID, membershipID, version, ok := s.membershipForm(w, r)
	if !ok {
		return
	}
	command := accountmembers.StateCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: membershipID, ExpectedVersion: version, Reason: r.FormValue("reason")}
	var err error
	if suspend {
		_, err = s.members.Suspend(r.Context(), command)
	} else {
		_, err = s.members.Reactivate(r.Context(), command)
	}
	if err != nil {
		s.redirectMembershipError(w, r, err)
		return
	}
	status := "member_reactivated"
	if suspend {
		status = "member_suspended"
	}
	http.Redirect(w, r, "/app?status="+status+"#settings", http.StatusSeeOther)
}

func (s *Server) leaveAccount(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.members == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Membership request was not accepted.", http.StatusForbidden)
		return
	}
	accountID := r.FormValue("account_id")
	version, err := strconv.ParseUint(r.FormValue("version"), 10, 64)
	if ids.Validate(accountID) != nil || err != nil || version == 0 || r.FormValue("confirmation") != "LEAVE" {
		http.Error(w, "Membership request was invalid.", http.StatusBadRequest)
		return
	}
	err = s.members.Leave(r.Context(), accountmembers.LeaveCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(accountID), ExpectedVersion: version, Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectMembershipError(w, r, err)
		return
	}
	s.clearAccountCookie(w)
	http.Redirect(w, r, "/app?status=account_left", http.StatusSeeOther)
}

func (s *Server) transferOwnership(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.members == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Ownership transfer request was not accepted.", http.StatusForbidden)
		return
	}
	accountID, targetID := r.FormValue("account_id"), r.FormValue("membership_id")
	actorVersion, actorErr := strconv.ParseUint(r.FormValue("actor_version"), 10, 64)
	targetVersion, targetErr := strconv.ParseUint(r.FormValue("version"), 10, 64)
	if ids.Validate(accountID) != nil || ids.Validate(targetID) != nil || actorErr != nil || targetErr != nil || actorVersion == 0 || targetVersion == 0 || r.FormValue("confirmation") != "TRANSFER" {
		http.Error(w, "Ownership transfer request was invalid.", http.StatusBadRequest)
		return
	}
	_, err := s.members.TransferOwnership(r.Context(), accountmembers.TransferOwnershipCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(accountID), TargetMembershipID: ids.MembershipID(targetID), ExpectedActorVersion: actorVersion, ExpectedTargetVersion: targetVersion, Reason: r.FormValue("reason")})
	if err != nil {
		s.redirectMembershipError(w, r, err)
		return
	}
	http.Redirect(w, r, "/app?status=ownership_transferred#settings", http.StatusSeeOther)
}

func (s *Server) membershipForm(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, ids.AccountID, ids.MembershipID, uint64, bool) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", "", 0, false
	}
	if s.members == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Membership request was not accepted.", http.StatusForbidden)
		return sessions.Authenticated{}, "", "", 0, false
	}
	accountID, membershipID := r.FormValue("account_id"), r.FormValue("membership_id")
	version, err := strconv.ParseUint(r.FormValue("version"), 10, 64)
	if ids.Validate(accountID) != nil || ids.Validate(membershipID) != nil || err != nil || version == 0 {
		http.Error(w, "Membership request was invalid.", http.StatusBadRequest)
		return sessions.Authenticated{}, "", "", 0, false
	}
	return authenticated, ids.AccountID(accountID), ids.MembershipID(membershipID), version, true
}

func (s *Server) redirectMembershipError(w http.ResponseWriter, r *http.Request, err error) {
	if access.IsDenied(err, access.DenialOwnerEnrollment) {
		http.Redirect(w, r, "/app/security?status=owner_enrollment_required", http.StatusSeeOther)
		return
	}
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	if errors.Is(err, accountmembers.ErrVersionConflict) || errors.Is(err, accountmembers.ErrStateConflict) {
		http.Redirect(w, r, "/app?status=membership_conflict#settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/app?status=membership_failed#settings", http.StatusSeeOther)
}

func (s *Server) securityPage(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	data, err := s.securityPageData(r, authenticated)
	if err != nil {
		http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	switch r.URL.Query().Get("status") {
	case "confirmed":
		data.Notice = "Password confirmed for factor recovery. Use a passkey to unlock verified-email, Membership, invitation, and billing changes."
	case "passkey_confirmed":
		data.Notice = "Passkey confirmed. Privileged Account actions are unlocked for 10 minutes."
	case "passkey_added":
		data.Notice = "Passkey added and confirmed. Privileged Account actions are unlocked for 10 minutes."
	case "passkey_renamed":
		data.Notice = "Passkey renamed."
	case "recovery_code_accepted":
		data.Notice = "Recovery code accepted for this session. Add a replacement passkey within 10 minutes."
	case "owner_enrollment_required":
		data.Notice = "Account owners must add a passkey and save recovery codes before entering an Account or performing owner duties."
	case "revoked":
		data.Notice = "The selected session has been signed out."
	case "mcp_revoked":
		data.Notice = "The connected MCP client has been revoked. Its access and refresh credentials no longer work."
	case "reauth_required":
		data.Notice = "Confirm your password before continuing with a sensitive action."
	case "strong_reauth_required":
		data.Notice = "Confirm with a passkey before changing the identity email, managing Memberships, inviting people, or changing billing. If this is your first passkey, confirm your password and add one below."
	case "strong_reauthentication_required":
		data.Notice = "Confirm with a passkey to continue. If this is your first passkey, confirm your password and add one below."
	case "google_connected":
		data.Notice = "Google sign-in is now connected to this Infinite Ocean identity."
	case "google_disconnected":
		data.Notice = "Google sign-in has been disconnected. Your password and passkeys are unchanged."
	case "google_failed":
		data.Error = "Google sign-in could not be changed. Confirm with a passkey and try again."
	}
	s.render(w, http.StatusOK, "security", data)
}

func (s *Server) securityPageData(r *http.Request, authenticated sessions.Authenticated) (pageData, error) {
	active, err := s.sessions.Active(r.Context(), authenticated.Session.UserID, authenticated.Session.ID)
	if err != nil {
		return pageData{}, err
	}
	events, err := s.sessions.SecurityEvents(r.Context(), authenticated.Session.UserID, 50)
	if err != nil {
		return pageData{}, err
	}
	data := pageData{Title: "Identity security", ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")), ActiveSessions: active, SecurityEvents: securityEventViews(events), PasskeysConfigured: s.passkeys != nil, RecoveryCodesConfigured: s.recoveryCodes != nil, ContactChangesConfigured: s.contactChanges != nil, MCPGrantsConfigured: s.mcpGrants != nil, GoogleConfigured: s.googleAuthentication != nil, PrivacyControls: true}
	if s.googleAuthentication != nil {
		data.GoogleConnected, err = s.googleAuthentication.Connected(r.Context(), authenticated.Session.UserID, s.googleIssuer)
		if err != nil {
			return pageData{}, err
		}
	}
	if s.contactChanges != nil {
		user, loadErr := s.contactChanges.Current(r.Context(), authenticated.Session.UserID)
		if loadErr != nil {
			return pageData{}, loadErr
		}
		data.CurrentEmail = user.PrimaryEmail
	}
	if s.passkeys != nil {
		data.Passkeys, err = s.passkeys.Credentials(r.Context(), authenticated.Session.UserID)
		if err != nil {
			return pageData{}, err
		}
		data.Script = "/assets/passkeys.js"
	}
	if s.recoveryCodes != nil {
		data.RecoveryCodeStatus, err = s.recoveryCodes.Status(r.Context(), authenticated.Session)
		if err != nil {
			return pageData{}, err
		}
	}
	if s.mcpGrants != nil {
		data.MCPGrants, err = s.mcpGrants.Grants(r.Context(), authenticated.Session.UserID)
		if err != nil {
			return pageData{}, err
		}
	}
	return data, nil
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
		case sessions.EventPasskeyAdded:
			view.Label = "Passkey added"
		case sessions.EventPasskeyRemoved:
			view.Label = "Passkey removed"
		case sessions.EventPasskeyRenamed:
			view.Label = "Passkey renamed"
		case sessions.EventPasskeyCompromised:
			view.Label = "Passkey reported compromised"
			view.Detail = "Credential removed and all sessions revoked"
		case sessions.EventPasskeyAuthenticated:
			view.Label = "Signed in with a passkey"
		case sessions.EventPasskeyReauthenticated:
			view.Label = "Identity confirmed with a passkey"
		case sessions.EventPasskeyCloneWarning:
			view.Label = "Passkey counter warning"
			view.Detail = "Authentication was rejected because credential state was unsafe"
		case sessions.EventRecoveryCodesRotated:
			view.Label = "Recovery codes replaced"
			view.Detail = "Previous unused codes were revoked"
		case sessions.EventRecoveryCodeConsumed:
			view.Label = "Recovery code used"
			view.Detail = "Replacement-passkey enrollment unlocked for this session"
		case sessions.EventPrimaryEmailChangeRequested:
			view.Label = "Email change requested"
			view.Detail = "The current email remains active until the new mailbox is verified"
		case sessions.EventPrimaryEmailChanged:
			view.Label = "Identity email changed"
			view.Detail = "Every existing session was revoked"
		default:
			view.Label = "Security setting changed"
		}
		result = append(result, view)
	}
	return result
}

func (s *Server) beginContactChange(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.contactChanges == nil {
		http.Error(w, "Verified contact change is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Contact change request was not accepted.", http.StatusForbidden)
		return
	}
	newEmail := r.FormValue("new_email")
	result, err := s.contactChanges.Begin(r.Context(), contactchange.BeginCommand{Session: authenticated.Session, NewEmail: newEmail})
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	if err != nil {
		data, loadErr := s.securityPageData(r, authenticated)
		if loadErr != nil {
			http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
			return
		}
		data.NewEmail = newEmail
		switch {
		case errors.Is(err, contactchange.ErrSameEmail):
			data.Error = "Enter an email different from the current identity email."
		case errors.Is(err, contactchange.ErrEmailExists):
			data.Error = "That email is not available for this identity."
		default:
			data.Error = "The email change could not be started. Check the address and try again."
		}
		s.render(w, http.StatusBadRequest, "security", data)
		return
	}
	data, err := s.securityPageData(r, authenticated)
	if err != nil {
		http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	data.NewEmail = result.NewEmail
	data.Notice = "Check the new mailbox to verify the change. Your current email remains active until confirmation."
	if s.config.ExposeDevelopmentTokens && s.contactChangeTokens != nil {
		if message, ok := s.contactChangeTokens.LatestVerification(authenticated.Session.UserID); ok && message.NewEmail == result.NewEmail {
			data.DevelopmentToken = message.Token
		}
	}
	s.render(w, http.StatusAccepted, "security", data)
}

func (s *Server) contactChangeVerificationPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	data := pageData{Title: "Verify new email", Token: token}
	if strings.TrimSpace(token) == "" {
		data.Error = "This email verification link is incomplete."
	}
	s.render(w, http.StatusOK, "contact-verify", data)
}

func (s *Server) completeContactChange(w http.ResponseWriter, r *http.Request) {
	if s.contactChanges == nil {
		s.render(w, http.StatusServiceUnavailable, "contact-verify", pageData{Title: "Verify new email", Error: "Verified contact change is temporarily unavailable."})
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		s.render(w, http.StatusForbidden, "contact-verify", pageData{Title: "Verify new email", Error: "This email verification request could not be verified."})
		return
	}
	token := r.FormValue("token")
	_, err := s.contactChanges.Complete(r.Context(), contactchange.CompleteCommand{Token: token})
	if err != nil {
		message := "This email verification link is invalid."
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, contactchange.ErrExpired):
			message, status = "This email verification link has expired. Sign in and start a new request.", http.StatusGone
		case errors.Is(err, contactchange.ErrConsumed):
			message, status = "This email verification link has already been used.", http.StatusConflict
		case errors.Is(err, contactchange.ErrStaleIdentity), errors.Is(err, contactchange.ErrEmailExists):
			message, status = "The identity changed after this request began. Sign in and start again.", http.StatusConflict
		case errors.Is(err, contactchange.ErrNotFound):
			status = http.StatusNotFound
		}
		s.render(w, status, "contact-verify", pageData{Title: "Verify new email", Error: message, Token: token})
		return
	}
	s.clearCookies(w)
	http.Redirect(w, r, "/login?status=email_changed", http.StatusSeeOther)
}

func (s *Server) rotateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.recoveryCodes == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Recovery code request was not accepted.", http.StatusForbidden)
		return
	}
	rotation, err := s.recoveryCodes.Rotate(r.Context(), authenticated.Session)
	if errors.Is(err, strongauth.ErrRequired) {
		http.Redirect(w, r, "/app/security?status=strong_reauth_required", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "Recovery codes could not be replaced.", http.StatusServiceUnavailable)
		return
	}
	data, err := s.securityPageData(r, authenticated)
	if err != nil {
		http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
		return
	}
	data.Notice = "New recovery codes created. Save them now; Spyglass will not show them again."
	newAnalyticsMarkers(&data, time.Now().UTC(), "security_enrollment_completed")
	data.AnalyticsMethod = "passkey_recovery_codes"
	data.RecoveryCodes = rotation.Codes
	data.RecoveryCodeStatus = rotation.Status
	s.render(w, http.StatusCreated, "security", data)
}

func (s *Server) consumeRecoveryCode(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.recoveryCodes == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "Recovery code request was not accepted.", http.StatusForbidden)
		return
	}
	err := s.recoveryCodes.Consume(r.Context(), authenticated.Session, r.FormValue("code"))
	if errors.Is(err, recoverycodes.ErrPasswordRequired) {
		http.Redirect(w, r, "/app/security?status=reauth_required", http.StatusSeeOther)
		return
	}
	if errors.Is(err, recoverycodes.ErrInvalidCode) {
		data, loadErr := s.securityPageData(r, authenticated)
		if loadErr != nil {
			http.Error(w, "Security settings could not be loaded.", http.StatusServiceUnavailable)
			return
		}
		data.Error = "That recovery code is invalid or has already been used."
		s.render(w, http.StatusBadRequest, "security", data)
		return
	}
	if err != nil {
		http.Error(w, "Recovery code could not be verified.", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/app/security?status=recovery_code_accepted", http.StatusSeeOther)
}

func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		s.render(w, http.StatusForbidden, "security", pageData{Title: "Identity security", Error: "This password confirmation request could not be verified.", ReturnTo: safeReturnTo(r.FormValue("return_to"))})
		return
	}
	returnTo := safeReturnTo(r.FormValue("return_to"))
	err := s.authentication.Reauthenticate(r.Context(), authentication.ReauthenticateCommand{UserID: authenticated.Session.UserID, SessionID: authenticated.Session.ID, Password: r.FormValue("password")})
	if err != nil {
		active, _ := s.sessions.Active(r.Context(), authenticated.Session.UserID, authenticated.Session.ID)
		s.render(w, http.StatusUnauthorized, "security", pageData{Title: "Identity security", Error: "The password is incorrect.", ReturnTo: returnTo, ActiveSessions: active})
		return
	}
	target := "/app/security?status=confirmed"
	if returnTo != "" {
		target += "&return_to=" + url.QueryEscape(returnTo)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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

func (s *Server) revokeMCPGrant(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.mcpGrants == nil || !s.validOrigin(r, false) || s.parseForm(w, r) != nil {
		http.Error(w, "MCP client revocation was not accepted.", http.StatusForbidden)
		return
	}
	revoked, err := s.mcpGrants.RevokeGrant(r.Context(), authenticated.Session.UserID, r.FormValue("grant_id"))
	if err != nil {
		http.Error(w, "MCP client could not be revoked.", http.StatusServiceUnavailable)
		return
	}
	if !revoked {
		http.Error(w, "MCP client was not found.", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/app/security?status=mcp_revoked", http.StatusSeeOther)
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
func (s *Server) clearAccountCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.config.AccountCookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{s.config.SessionCookieName, s.config.AccountCookieName} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
	}
}
func (s *Server) validOrigin(r *http.Request, _ bool) bool {
	origin := r.Header.Get("Origin")
	return origin != "" && origin == s.config.TrustedOrigins[0]
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
	case "email_changed":
		return "Identity email verified and changed. Every previous session was signed out; sign in with the new email."
	case "passkey_compromised":
		return "The compromised passkey was removed and every session was signed out. Sign in again and review your identity security settings."
	case "google_not_connected":
		return "That Google account is not connected yet. Sign in another way, then connect Google from Identity Security."
	case "google_failed":
		return "Google sign-in could not be completed. Please try again."
	case "google_disconnected":
		return "Google sign-in was disconnected and existing sessions were signed out."
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
	case "welcome":
		return "Your team shell is ready. No product access is granted until you review the offer, complete Stripe checkout, and payment is confirmed."
	case "member_role_changed":
		return "Membership role updated and recorded in the Account audit history."
	case "member_removed":
		return "Membership removed. The identity no longer has access to this Account."
	case "member_suspended":
		return "Membership suspended. Account access is blocked while the role is preserved."
	case "member_reactivated":
		return "Membership reactivated with its previous role."
	case "account_left":
		return "You left the Account. Your other Account access is unchanged."
	case "ownership_transferred":
		return "Ownership transferred atomically. Your Membership is now Administrator."
	case "membership_conflict":
		return "The Membership changed while you were working. Review the current roster and try again."
	case "membership_failed":
		return "The Membership change was denied or could not be completed."
	case "closure_canceled":
		return "Account closure canceled. Normal Account access has been restored."
	}
	return ""
}

func closureNotice(status string) string {
	switch status {
	case "closure_requested":
		return "Account access is frozen. You may restore it here until the cooling-off period completes."
	case "closure_billing_active":
		return "Closure cannot begin while a subscription or checkout is active. Resolve billing, then try again."
	case "closure_conflict":
		return "The Account lifecycle changed while you were working. Review the current state and try again."
	case "closure_failed":
		return "The Account lifecycle change was denied or could not be completed."
	}
	return ""
}
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Native same-origin form submissions need a serialized Origin so the
		// request boundary can reject cross-site mutations. same-origin keeps
		// full referrers off every other origin without turning valid POSTs into
		// the opaque Origin value "null".
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
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
