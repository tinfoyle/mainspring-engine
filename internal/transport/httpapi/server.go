package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Server struct {
	registrations         *registration.Service
	catalog               func() catalog.PublishedCatalog
	verification          VerificationTokenSource
	exposeDevToken        bool
	billingWebhook        *billing.WebhookService
	logger                *slog.Logger
	authentication        *authentication.Service
	sessions              *sessions.Service
	cookie                SessionCookie
	accounts              *accountaccess.Service
	invitations           *invitations.Service
	invitationTokens      InvitationTokenSource
	exposeInvitationToken bool
}

type SessionCookie struct {
	Name        string
	AccountName string
	Secure      bool
	Domain      string
}

// VerificationTokenSource is development-only. Production compositions leave
// it nil so raw verification credentials can never enter an API response.
type VerificationTokenSource interface {
	Latest() (registration.VerificationMessage, bool)
}

type InvitationTokenSource interface {
	Latest() (invitations.Message, bool)
}

type Option func(*Server)

func WithBillingWebhook(service *billing.WebhookService) Option {
	return func(server *Server) { server.billingWebhook = service }
}

func WithAuthentication(service *authentication.Service, sessionService *sessions.Service, cookie SessionCookie) Option {
	return func(server *Server) {
		server.authentication, server.sessions, server.cookie = service, sessionService, cookie
		if server.cookie.Name == "" {
			server.cookie.Name = "__Host-spyglass_session"
		}
		if server.cookie.AccountName == "" {
			if server.cookie.Secure {
				server.cookie.AccountName = "__Host-spyglass_account"
			} else {
				server.cookie.AccountName = "spyglass_development_account"
			}
		}
	}
}

func WithAccountAccess(service *accountaccess.Service) Option {
	return func(server *Server) { server.accounts = service }
}

func WithInvitations(service *invitations.Service, tokens InvitationTokenSource, exposeDevelopmentToken bool) Option {
	return func(server *Server) {
		server.invitations = service
		server.invitationTokens = tokens
		server.exposeInvitationToken = exposeDevelopmentToken
	}
}

func NewServer(registrations *registration.Service, catalogSource func() catalog.PublishedCatalog, verification VerificationTokenSource, exposeDevToken bool, logger *slog.Logger, options ...Option) *Server {
	server := &Server{registrations: registrations, catalog: catalogSource, verification: verification, exposeDevToken: exposeDevToken, logger: logger}
	for _, option := range options {
		option(server)
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /api/v1/catalog/public", s.publicCatalog)
	mux.HandleFunc("POST /api/v1/registrations", s.beginRegistration)
	mux.HandleFunc("POST /api/v1/registrations/verify", s.completeRegistration)
	mux.HandleFunc("POST /api/v1/sessions", s.login)
	mux.HandleFunc("DELETE /api/v1/session", s.logout)
	mux.HandleFunc("GET /api/v1/session/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/v1/session/account", s.selectAccount)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/invitations", s.createInvitation)
	mux.HandleFunc("POST /api/v1/invitations/accept", s.acceptInvitation)
	mux.HandleFunc("POST /webhooks/stripe", s.stripeWebhook)
	return s.securityHeaders(s.recoverPanics(s.requestLog(mux)))
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	if s.invitations == nil {
		writeProblem(w, http.StatusServiceUnavailable, "invitations_unconfigured", "invitations are not configured")
		return
	}
	rawAccountID := r.PathValue("accountID")
	if err := ids.Validate(rawAccountID); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "account ID is invalid")
		return
	}
	var input struct {
		Email string                  `json:"email"`
		Role  accounts.MembershipRole `json:"role"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := s.invitations.Create(r.Context(), invitations.CreateCommand{ActorUserID: authenticated.Session.UserID, AccountID: ids.AccountID(rawAccountID), Email: input.Email, Role: input.Role})
	if err != nil {
		s.writeInvitationError(w, err)
		return
	}
	response := map[string]any{"invitation_id": created.InvitationID, "expires_at": created.ExpiresAt, "status": "pending"}
	if s.exposeInvitationToken && s.invitationTokens != nil {
		if message, ok := s.invitationTokens.Latest(); ok && message.InvitationID == created.InvitationID {
			response["development_invitation_token"] = message.Token
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	if s.invitations == nil {
		writeProblem(w, http.StatusServiceUnavailable, "invitations_unconfigured", "invitations are not configured")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	membership, err := s.invitations.Accept(r.Context(), invitations.AcceptCommand{UserID: authenticated.Session.UserID, Token: input.Token})
	if err != nil {
		s.writeInvitationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"membership": membership})
}

func (s *Server) writeInvitationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, invitations.ErrMembershipExists):
		writeProblem(w, http.StatusConflict, "membership_exists", "the identity is already a member")
	case errors.Is(err, invitations.ErrInvitationExpired):
		writeProblem(w, http.StatusGone, "invitation_expired", "the invitation has expired")
	case errors.Is(err, invitations.ErrInvitationConsumed):
		writeProblem(w, http.StatusConflict, "invitation_consumed", "the invitation has already been used")
	case errors.Is(err, invitations.ErrInvitationNotFound), errors.Is(err, invitations.ErrInvitationEmailMismatch):
		writeProblem(w, http.StatusNotFound, "invitation_not_found", "the invitation is invalid")
	default:
		writeProblem(w, http.StatusForbidden, "invitation_denied", "the invitation operation was denied")
	}
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	if s.accounts == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_access_unconfigured", "account access is not configured")
		return
	}
	choices, err := s.accounts.List(r.Context(), authenticated.Session.UserID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_access_failed", "accounts could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": choices})
}

func (s *Server) selectAccount(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	if s.accounts == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_access_unconfigured", "account access is not configured")
		return
	}
	var input struct {
		AccountID string `json:"account_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := ids.Validate(input.AccountID); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "account ID is invalid")
		return
	}
	resolved, err := s.accounts.Select(r.Context(), authenticated.Session.UserID, ids.AccountID(input.AccountID))
	if err != nil {
		writeProblem(w, http.StatusForbidden, "account_access_denied", "the selected Account is unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookie.AccountName, Value: string(resolved.AccountID), Path: "/", HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"account_context": resolved})
}

func (s *Server) authenticateSession(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, bool) {
	if s.sessions == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unconfigured", "authentication is not configured")
		return sessions.Authenticated{}, false
	}
	cookie, err := r.Cookie(s.cookie.Name)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "authentication_required", "authentication is required")
		return sessions.Authenticated{}, false
	}
	authenticated, err := s.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		s.clearSessionCookie(w)
		writeProblem(w, http.StatusUnauthorized, "session_invalid", "the session is invalid or expired")
		return sessions.Authenticated{}, false
	}
	if authenticated.RotatedToken != "" {
		s.setSessionCookie(w, authenticated.RotatedToken, authenticated.Session.ExpiresAt)
	}
	return authenticated, true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.authentication == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unconfigured", "authentication is not configured")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	issued, err := s.authentication.Login(r.Context(), authentication.LoginCommand{Email: input.Email, Password: input.Password})
	if err != nil {
		if errors.Is(err, authentication.ErrInvalidCredentials) {
			writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "the email or password is incorrect")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "authentication_failed", "authentication could not be completed")
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "authenticated", "user_id": issued.Session.UserID, "expires_at": issued.Session.ExpiresAt})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.sessions != nil {
		if cookie, err := r.Cookie(s.cookie.Name); err == nil {
			if authenticated, err := s.sessions.Authenticate(r.Context(), cookie.Value); err == nil {
				_ = s.sessions.Revoke(r.Context(), authenticated.Session.ID)
			}
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: s.cookie.Name, Value: token, Path: "/", Domain: s.cookie.Domain, HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookie.Name, Value: "", Path: "/", Domain: s.cookie.Domain, HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.billingWebhook == nil {
		writeProblem(w, http.StatusServiceUnavailable, "billing_webhook_unconfigured", "billing webhook ingestion is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_webhook", "webhook payload could not be read")
		return
	}
	result, err := s.billingWebhook.Ingest(r.Context(), payload, r.Header.Get("Stripe-Signature"))
	if err != nil {
		switch {
		case errors.Is(err, billing.ErrInvalidSignature), errors.Is(err, billing.ErrStaleSignature):
			writeProblem(w, http.StatusBadRequest, "invalid_webhook_signature", "webhook signature verification failed")
		case errors.Is(err, billing.ErrWrongMode):
			writeProblem(w, http.StatusBadRequest, "wrong_webhook_mode", "webhook mode does not match this endpoint")
		case errors.Is(err, billing.ErrInvalidEvent):
			writeProblem(w, http.StatusBadRequest, "invalid_webhook_event", "webhook event is invalid")
		default:
			writeProblem(w, http.StatusServiceUnavailable, "webhook_acceptance_failed", "webhook event could not be durably accepted")
		}
		return
	}
	status := "accepted"
	if !result.Accepted {
		status = "duplicate"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "event_id": result.EventID})
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) publicCatalog(w http.ResponseWriter, _ *http.Request) {
	catalog := s.catalog()
	offers := make([]catalogOffer, 0, len(catalog.Offers))
	for _, offer := range catalog.Offers {
		if !offer.Published {
			continue
		}
		offers = append(offers, catalogOffer{Code: offer.Code, PlanCode: offer.PlanCode, PlanVersion: offer.PlanVersion, Currency: offer.Currency, AmountMinor: offer.AmountMinor, BillingInterval: offer.BillingInterval, EffectiveFrom: offer.EffectiveFrom})
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": catalog.Version, "published_at": catalog.PublishedAt, "packages": catalog.Packages, "plans": catalog.Plans, "offers": offers})
}

type catalogOffer struct {
	Code            string    `json:"code"`
	PlanCode        string    `json:"plan_code"`
	PlanVersion     uint64    `json:"plan_version"`
	Currency        string    `json:"currency"`
	AmountMinor     int64     `json:"amount_minor"`
	BillingInterval string    `json:"billing_interval"`
	EffectiveFrom   time.Time `json:"effective_from"`
}

func (s *Server) beginRegistration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		AccountName string `json:"account_name"`
		Region      string `json:"region"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.registrations.Begin(r.Context(), registration.BeginCommand{Email: input.Email, DisplayName: input.DisplayName, AccountName: input.AccountName, Region: input.Region})
	if err != nil {
		s.writeRegistrationError(w, err)
		return
	}
	response := map[string]any{"registration_id": result.RegistrationID, "expires_at": result.ExpiresAt, "status": "verification_required"}
	if s.exposeDevToken && s.verification != nil {
		if message, ok := s.verification.Latest(); ok && message.RegistrationID == result.RegistrationID {
			response["development_verification_token"] = message.Token
		}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) completeRegistration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Token) == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	result, err := s.registrations.Complete(r.Context(), registration.CompleteCommand{Token: input.Token, Password: input.Password})
	if err != nil {
		s.writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": map[string]any{"id": result.User.ID, "email": result.User.PrimaryEmail, "display_name": result.User.DisplayName, "state": result.User.State}, "account": map[string]any{"id": result.Account.ID, "slug": result.Account.Slug, "display_name": result.Account.DisplayName, "type": result.Account.Type, "state": result.Account.State, "cell_id": result.Account.CellID, "placement_generation": result.Account.PlacementGeneration}, "membership": map[string]any{"id": result.Membership.ID, "role": result.Membership.Role, "state": result.Membership.State}, "entitlements": result.Snapshot})
}

func (s *Server) writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registration.ErrEmailExists):
		writeProblem(w, http.StatusConflict, "registration_conflict", "a registration already exists for this email")
	case errors.Is(err, registration.ErrRegistrationExpired):
		writeProblem(w, http.StatusGone, "registration_expired", "the verification link has expired")
	case errors.Is(err, registration.ErrRegistrationConsumed):
		writeProblem(w, http.StatusConflict, "registration_consumed", "the verification link has already been used")
	case errors.Is(err, registration.ErrRegistrationNotFound):
		writeProblem(w, http.StatusNotFound, "registration_not_found", "the verification link is invalid")
	default:
		writeProblem(w, http.StatusBadRequest, "registration_invalid", err.Error())
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		return errors.New("content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body must be valid JSON with known fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("http panic", "recovered", recovered)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
