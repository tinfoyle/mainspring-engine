// Package mcpoauth exposes the OAuth authorization server used by public MCP
// clients. Browser consent uses the existing Spyglass identity session.
package mcpoauth

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

const defaultSessionCookie = "__Host-spyglass_session"

type SessionAuthenticator interface {
	Authenticate(context.Context, string) (sessions.Authenticated, error)
}

type RequestLimiter interface {
	Allow(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error)
}

type Config struct {
	Issuer            string
	Resource          string
	TrustedOrigin     string
	SessionCookieName string
	SecureCookies     bool
}

type Server struct {
	service  *mcpauth.Service
	sessions SessionAuthenticator
	clients  ClientMetadataLoader
	limiter  RequestLimiter
	clock    mcpauth.Clock
	config   Config
	logger   *slog.Logger
	template *template.Template
}

func New(service *mcpauth.Service, sessionAuthenticator SessionAuthenticator, clients ClientMetadataLoader, limiter RequestLimiter, clock mcpauth.Clock, config Config, logger *slog.Logger) (*Server, error) {
	if service == nil || sessionAuthenticator == nil || clients == nil || limiter == nil || clock == nil || logger == nil || !validOrigin(config.Issuer) || !validOrigin(config.Resource) || config.TrustedOrigin != config.Issuer {
		return nil, errors.New("MCP OAuth transport dependencies and canonical origins are required")
	}
	if config.SessionCookieName == "" {
		config.SessionCookieName = defaultSessionCookie
	}
	if config.SecureCookies && !strings.HasPrefix(config.Issuer, "https://") {
		return nil, errors.New("secure MCP OAuth cookies require HTTPS")
	}
	page, err := template.New("consent").Parse(consentPage)
	if err != nil {
		return nil, err
	}
	return &Server{service: service, sessions: sessionAuthenticator, clients: clients, limiter: limiter, clock: clock, config: config, logger: logger, template: page}, nil
}

func (s *Server) Handler(fallback http.Handler) http.Handler {
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.metadata)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.decide)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
	mux.Handle("/", fallback)
	return s.securityHeaders(mux)
}

func (s *Server) metadata(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.config.Issuer,
		"authorization_endpoint":                         s.config.Issuer + "/oauth/authorize",
		"token_endpoint":                                 s.config.Issuer + "/oauth/token",
		"revocation_endpoint":                            s.config.Issuer + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               []string{mcpauth.ScopeMCP},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, abuse.ScopeMCPAuthorization, abuse.MCPAuthorizationPolicy) {
		return
	}
	authenticated, ok := s.currentSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	query := r.URL.Query()
	if query.Get("response_type") != "code" || query.Get("code_challenge_method") != "S256" {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	client, err := s.clients.Load(r.Context(), query.Get("client_id"))
	if err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_client")
		return
	}
	pending, err := s.service.Begin(r.Context(), mcpauth.AuthorizationCommand{
		UserID: authenticated.Session.UserID, SessionID: authenticated.Session.ID, Client: client,
		RedirectURI: query.Get("redirect_uri"), Resource: query.Get("resource"), Scope: query.Get("scope"),
		State: query.Get("state"), CodeChallenge: query.Get("code_challenge"),
	})
	if err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.template.Execute(w, map[string]string{"PendingID": pending.ID, "ClientName": pending.ClientName, "Resource": pending.Resource, "Scope": pending.Scope}); err != nil {
		s.logger.Error("render MCP OAuth consent", "error", err)
	}
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.config.TrustedOrigin {
		s.oauthError(w, http.StatusForbidden, "access_denied")
		return
	}
	if !s.allow(w, r, abuse.ScopeMCPAuthorization, abuse.MCPAuthorizationPolicy) {
		return
	}
	authenticated, ok := s.currentSession(w, r)
	if !ok {
		s.oauthError(w, http.StatusUnauthorized, "access_denied")
		return
	}
	if err := parseForm(w, r); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	decision := r.FormValue("decision")
	if decision != "approve" && decision != "deny" {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := s.service.Decide(r.Context(), mcpauth.AuthorizationDecision{PendingID: r.FormValue("pending_id"), UserID: authenticated.Session.UserID, SessionID: authenticated.Session.ID, Approve: decision == "approve"})
	if err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	target, err := url.Parse(result.RedirectURI)
	if err != nil {
		s.oauthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	query := target.Query()
	if result.Approved {
		query.Set("code", result.Code)
	} else {
		query.Set("error", "access_denied")
	}
	if result.State != "" {
		query.Set("state", result.State)
	}
	query.Set("iss", result.Issuer)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, abuse.ScopeMCPToken, abuse.MCPTokenPolicy) {
		return
	}
	if err := requireForm(w, r); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var result mcpauth.TokenSet
	var err error
	switch r.FormValue("grant_type") {
	case "authorization_code":
		result, err = s.service.ExchangeCode(r.Context(), r.FormValue("code"), r.FormValue("client_id"), r.FormValue("redirect_uri"), r.FormValue("resource"), r.FormValue("code_verifier"))
	case "refresh_token":
		if scope := r.FormValue("scope"); scope != "" && scope != mcpauth.ScopeMCP {
			s.oauthError(w, http.StatusBadRequest, "invalid_scope")
			return
		}
		result, err = s.service.Refresh(r.Context(), r.FormValue("refresh_token"), r.FormValue("client_id"), r.FormValue("resource"))
	default:
		s.oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if err != nil {
		code := "invalid_grant"
		if errors.Is(err, mcpauth.ErrInvalid) {
			code = "invalid_request"
		}
		s.oauthError(w, http.StatusBadRequest, code)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"access_token": result.AccessToken, "refresh_token": result.RefreshToken, "token_type": result.TokenType, "scope": result.Scope, "expires_in": result.ExpiresIn})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, abuse.ScopeMCPRevocation, abuse.MCPRevocationPolicy) {
		return
	}
	if err := requireForm(w, r); err != nil {
		s.oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.service.Revoke(r.Context(), r.FormValue("token"), r.FormValue("client_id")); err != nil {
		s.oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) allow(w http.ResponseWriter, r *http.Request, scope abuse.Scope, policy abuse.Policy) bool {
	actor, _ := networkactor.FromContext(r.Context())
	allowed, err := s.limiter.Allow(r.Context(), scope, actor, s.clock.Now(), policy)
	if err != nil {
		s.logger.Error("apply MCP OAuth request budget", "scope", scope, "error", err)
		s.oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(policy.Window/time.Second), 10))
		s.oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable")
		return false
	}
	return true
}

func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, bool) {
	cookie, err := r.Cookie(s.config.SessionCookieName)
	if err != nil {
		return sessions.Authenticated{}, false
	}
	authenticated, err := s.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		return sessions.Authenticated{}, false
	}
	if authenticated.RotatedToken != "" {
		http.SetCookie(w, &http.Cookie{Name: s.config.SessionCookieName, Value: authenticated.RotatedToken, Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: authenticated.Session.ExpiresAt, MaxAge: int(time.Until(authenticated.Session.ExpiresAt).Seconds())})
	}
	return authenticated, true
}

func requireForm(w http.ResponseWriter, r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return errors.New("form content type required")
	}
	return parseForm(w, r)
}

func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	return r.ParseForm()
}

func (s *Server) oauthError(w http.ResponseWriter, status int, code string) {
	s.writeJSON(w, status, map[string]string{"error": code})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func validOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.String() == raw
}

const consentPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorize MCP client</title><style>
body{margin:0;background:#f2f5f1;color:#14241d;font:16px system-ui,sans-serif}.card{width:min(520px,calc(100% - 40px));margin:10vh auto;border:1px solid #d6dfd8;border-radius:18px;background:white;padding:32px;box-sizing:border-box}h1{font:500 2rem Georgia,serif;margin:.3rem 0 1rem}.label{color:#176a4b;font-size:.7rem;font-weight:800;letter-spacing:.12em}.detail{padding:14px;border-radius:10px;background:#eef5f0;overflow-wrap:anywhere}form{display:flex;gap:10px;margin-top:24px}button{flex:1;border:1px solid #176a4b;border-radius:10px;padding:13px;font-weight:750;background:white;color:#176a4b}button.primary{background:#176a4b;color:white}</style></head><body><main class="card"><p class="label">SPYGLASS MCP ACCESS</p><h1>Connect {{.ClientName}}?</h1><p>This client is requesting access to act as your signed-in Infinite Ocean identity.</p><div class="detail"><strong>Resource</strong><br>{{.Resource}}<br><br><strong>Permission</strong><br>{{.Scope}}</div><form method="post" action="/oauth/authorize"><input type="hidden" name="pending_id" value="{{.PendingID}}"><button type="submit" name="decision" value="deny">Deny</button><button class="primary" type="submit" name="decision" value="approve">Authorize</button></form></main></body></html>`
