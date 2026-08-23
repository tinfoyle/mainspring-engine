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
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
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
	accountLifecycle      *accountlifecycle.Service
	members               *accountmembers.Service
	invitations           *invitations.Service
	invitationTokens      InvitationTokenSource
	exposeInvitationToken bool
	commercialAccess      *commercialaccess.Service
	commercialOrigin      string
	recovery              *recovery.Service
	recoveryTokens        RecoveryTokenSource
	exposeRecoveryToken   bool
	passkeys              *passkeys.Service
	recoveryCodes         *recoverycodes.Service
	securityPosture       *securityposture.Service
	contactChanges        *contactchange.Service
	contactChangeTokens   ContactChangeTokenSource
	exposeContactToken    bool
	accountExports        *accountexport.Service
	exportDownloads       *accountexport.DownloadService
}

type SessionCookie struct {
	Name        string
	AccountName string
	Secure      bool
	Domain      string
	Origin      string
}

// VerificationTokenSource is development-only. Production compositions leave
// it nil so raw verification credentials can never enter an API response.
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

func WithAccountExports(service *accountexport.Service, downloads *accountexport.DownloadService) Option {
	return func(server *Server) { server.accountExports, server.exportDownloads = service, downloads }
}

func WithAccountLifecycle(service *accountlifecycle.Service) Option {
	return func(server *Server) { server.accountLifecycle = service }
}

func WithAccountMembers(service *accountmembers.Service) Option {
	return func(server *Server) { server.members = service }
}

func WithInvitations(service *invitations.Service, tokens InvitationTokenSource, exposeDevelopmentToken bool) Option {
	return func(server *Server) {
		server.invitations = service
		server.invitationTokens = tokens
		server.exposeInvitationToken = exposeDevelopmentToken
	}
}

func WithCommercialAccess(service *commercialaccess.Service, applicationOrigin string) Option {
	return func(server *Server) {
		server.commercialAccess = service
		server.commercialOrigin = strings.TrimSuffix(applicationOrigin, "/")
	}
}

func WithRecovery(service *recovery.Service, tokens RecoveryTokenSource, exposeDevelopmentToken bool) Option {
	return func(server *Server) {
		server.recovery = service
		server.recoveryTokens = tokens
		server.exposeRecoveryToken = exposeDevelopmentToken
	}
}

func WithPasskeys(service *passkeys.Service) Option {
	return func(server *Server) { server.passkeys = service }
}

func WithRecoveryCodes(service *recoverycodes.Service) Option {
	return func(server *Server) { server.recoveryCodes = service }
}

func WithSecurityPosture(service *securityposture.Service) Option {
	return func(server *Server) { server.securityPosture = service }
}

func WithContactChanges(service *contactchange.Service, tokens ContactChangeTokenSource, exposeDevelopmentToken bool) Option {
	return func(server *Server) {
		server.contactChanges = service
		server.contactChangeTokens = tokens
		server.exposeContactToken = exposeDevelopmentToken
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
	mux.HandleFunc("POST /api/v1/recovery-challenges", s.beginRecovery)
	mux.HandleFunc("POST /api/v1/recovery-challenges/complete", s.completeRecovery)
	mux.HandleFunc("POST /api/v1/contact-change-requests", s.beginContactChange)
	mux.HandleFunc("POST /api/v1/contact-change-verifications", s.completeContactChange)
	mux.HandleFunc("POST /api/v1/sessions", s.login)
	mux.HandleFunc("POST /api/v1/passkey-login/challenges", s.beginPasskeyLogin)
	mux.HandleFunc("POST /api/v1/passkey-login/challenges/{ceremonyID}/complete", s.completePasskeyLogin)
	mux.HandleFunc("GET /api/v1/passkeys", s.listPasskeys)
	mux.HandleFunc("POST /api/v1/passkey-registrations", s.beginPasskeyRegistration)
	mux.HandleFunc("POST /api/v1/passkey-registrations/{ceremonyID}/complete", s.completePasskeyRegistration)
	mux.HandleFunc("PATCH /api/v1/passkeys/{credentialID}", s.renamePasskey)
	mux.HandleFunc("DELETE /api/v1/passkeys/{credentialID}", s.deletePasskey)
	mux.HandleFunc("POST /api/v1/passkeys/{credentialID}/compromise", s.compromisePasskey)
	mux.HandleFunc("POST /api/v1/passkey-reauthentications", s.beginPasskeyReauthentication)
	mux.HandleFunc("POST /api/v1/passkey-reauthentications/{ceremonyID}/complete", s.completePasskeyReauthentication)
	mux.HandleFunc("GET /api/v1/recovery-codes", s.recoveryCodeStatus)
	mux.HandleFunc("GET /api/v1/security-posture", s.securityPostureStatus)
	mux.HandleFunc("POST /api/v1/recovery-codes", s.rotateRecoveryCodes)
	mux.HandleFunc("POST /api/v1/recovery-codes/consume", s.consumeRecoveryCode)
	mux.HandleFunc("GET /api/v1/sessions", s.listSessions)
	mux.HandleFunc("GET /api/v1/security-events", s.listSecurityEvents)
	mux.HandleFunc("DELETE /api/v1/sessions", s.logoutAll)
	mux.HandleFunc("DELETE /api/v1/sessions/{sessionID}", s.revokeSession)
	mux.HandleFunc("POST /api/v1/session/reauthenticate", s.reauthenticate)
	mux.HandleFunc("DELETE /api/v1/session", s.logout)
	mux.HandleFunc("GET /api/v1/session/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/v1/session/account", s.selectAccount)
	mux.HandleFunc("GET /api/v1/account-closures", s.listAccountClosures)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/closure", s.requestAccountClosure)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/closure", s.cancelAccountClosure)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/invitations", s.createInvitation)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/memberships", s.listMemberships)
	mux.HandleFunc("PATCH /api/v1/accounts/{accountID}/memberships/{membershipID}", s.changeMembershipRole)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/memberships/{membershipID}", s.removeMembership)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/memberships/{membershipID}/suspensions", s.suspendMembership)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/memberships/{membershipID}/suspensions", s.reactivateMembership)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/membership", s.leaveAccount)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/ownership-transfers", s.transferOwnership)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/checkout-sessions", s.createCheckoutSession)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/billing-portal-sessions", s.createBillingPortalSession)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/billing", s.billingStatus)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/exports", s.listAccountExports)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/exports", s.createAccountExport)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/exports/{exportID}", s.getAccountExport)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/exports/{exportID}", s.cancelAccountExport)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/exports/{exportID}/download-capabilities", s.createAccountExportDownloadCapability)
	mux.HandleFunc("GET /api/v1/account-exports/{exportID}/artifact", s.downloadAccountExport)
	mux.HandleFunc("POST /api/v1/invitations/accept", s.acceptInvitation)
	mux.HandleFunc("POST /webhooks/stripe", s.stripeWebhook)
	return s.securityHeaders(s.recoverPanics(s.requestLog(mux)))
}

func (s *Server) beginContactChange(w http.ResponseWriter, r *http.Request) {
	if s.contactChanges == nil {
		writeProblem(w, http.StatusServiceUnavailable, "contact_change_unconfigured", "verified contact change is not configured")
		return
	}
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	var input struct {
		NewEmail string `json:"new_email"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.contactChanges.Begin(r.Context(), contactchange.BeginCommand{Session: authenticated.Session, NewEmail: input.NewEmail})
	if err != nil {
		s.writeContactChangeError(w, err)
		return
	}
	response := map[string]any{"contact_change_id": result.ID, "new_email": result.NewEmail, "expires_at": result.ExpiresAt, "status": "verification_required"}
	if s.exposeContactToken && s.contactChangeTokens != nil {
		if message, ok := s.contactChangeTokens.LatestVerification(authenticated.Session.UserID); ok && message.NewEmail == result.NewEmail {
			response["development_verification_token"] = message.Token
		}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) completeContactChange(w http.ResponseWriter, r *http.Request) {
	if s.contactChanges == nil {
		writeProblem(w, http.StatusServiceUnavailable, "contact_change_unconfigured", "verified contact change is not configured")
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Token) == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	result, err := s.contactChanges.Complete(r.Context(), contactchange.CompleteCommand{Token: input.Token})
	if err != nil {
		s.writeContactChangeError(w, err)
		return
	}
	s.clearIdentityCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"status": "email_changed", "new_email": result.NewEmail, "changed_at": result.ChangedAt, "sessions_revoked": true})
}

func (s *Server) writeContactChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before changing the identity email")
	case errors.Is(err, contactchange.ErrSameEmail):
		writeProblem(w, http.StatusBadRequest, "contact_change_same_email", "the new email must differ from the current email")
	case errors.Is(err, contactchange.ErrEmailExists):
		writeProblem(w, http.StatusConflict, "contact_change_conflict", "the new email is unavailable")
	case errors.Is(err, contactchange.ErrExpired):
		writeProblem(w, http.StatusGone, "contact_change_expired", "the verification link has expired")
	case errors.Is(err, contactchange.ErrConsumed):
		writeProblem(w, http.StatusConflict, "contact_change_consumed", "the verification link has already been used")
	case errors.Is(err, contactchange.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "contact_change_not_found", "the verification link is invalid")
	case errors.Is(err, contactchange.ErrStaleIdentity), errors.Is(err, contactchange.ErrInvalidUser):
		writeProblem(w, http.StatusConflict, "contact_change_stale", "the identity changed; start a new email change request")
	default:
		writeProblem(w, http.StatusBadRequest, "contact_change_invalid", "the email change could not be completed")
	}
}

func (s *Server) securityPostureStatus(w http.ResponseWriter, r *http.Request) {
	if s.securityPosture == nil {
		writeProblem(w, http.StatusServiceUnavailable, "security_posture_unconfigured", "security posture is not configured")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	state, err := s.securityPosture.Status(r.Context(), authenticated.Session.UserID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "security_posture_unavailable", "security posture could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) listAccountClosures(w http.ResponseWriter, r *http.Request) {
	if s.accountLifecycle == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_lifecycle_unconfigured", "Account lifecycle management is not configured")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	closures, err := s.accountLifecycle.ListOwned(r.Context(), authenticated.Session.UserID)
	if err != nil {
		s.writeAccountLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_closures": closures})
}

func (s *Server) requestAccountClosure(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountLifecycleRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedAccountVersion uint64 `json:"expected_account_version"`
		Reason                 string `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status, err := s.accountLifecycle.Request(r.Context(), accountlifecycle.RequestCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, ExpectedAccountVersion: input.ExpectedAccountVersion, Reason: input.Reason})
	if err != nil {
		s.writeAccountLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) cancelAccountClosure(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.accountLifecycleRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedAccountVersion uint64 `json:"expected_account_version"`
		Reason                 string `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status, err := s.accountLifecycle.Cancel(r.Context(), accountlifecycle.CancelCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, ExpectedAccountVersion: input.ExpectedAccountVersion, Reason: input.Reason})
	if err != nil {
		s.writeAccountLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) accountLifecycleRequest(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, ids.AccountID, bool) {
	if s.accountLifecycle == nil {
		writeProblem(w, http.StatusServiceUnavailable, "account_lifecycle_unconfigured", "Account lifecycle management is not configured")
		return sessions.Authenticated{}, "", false
	}
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, "", false
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	raw := r.PathValue("accountID")
	if ids.Validate(raw) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "Account ID is invalid")
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(raw), true
}

func (s *Server) writeAccountLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case access.IsDenied(err, access.DenialOwnerEnrollment):
		writeProblem(w, http.StatusForbidden, "owner_security_enrollment_required", "add a passkey and save recovery codes before using owner authority")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before this privileged operation")
	case errors.Is(err, accountlifecycle.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "account_closure_not_found", "the Account closure request was not found")
	case errors.Is(err, accountlifecycle.ErrVersionConflict):
		writeProblem(w, http.StatusConflict, "account_version_conflict", "the Account changed; reload before trying again")
	case errors.Is(err, accountlifecycle.ErrStateConflict):
		writeProblem(w, http.StatusConflict, "account_state_conflict", "the Account is not in a state that allows this transition")
	case errors.Is(err, accountlifecycle.ErrBillingActive):
		writeProblem(w, http.StatusConflict, "account_closure_billing_active", "resolve active subscriptions or checkout before requesting closure")
	case errors.Is(err, accountlifecycle.ErrReasonRequired):
		writeProblem(w, http.StatusBadRequest, "invalid_account_closure", err.Error())
	case errors.Is(err, accountlifecycle.ErrOwnershipRequired), access.IsDenied(err, access.DenialRole), access.IsDenied(err, access.DenialMembership), access.IsDenied(err, access.DenialAccountUnavailable):
		writeProblem(w, http.StatusForbidden, "account_closure_denied", "only an active Account owner may manage closure")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "account_lifecycle_failed", "Account lifecycle management could not be completed")
	}
}

func (s *Server) beginRecovery(w http.ResponseWriter, r *http.Request) {
	if s.recovery == nil {
		writeProblem(w, http.StatusServiceUnavailable, "recovery_unconfigured", "credential recovery is not configured")
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	result, err := s.recovery.Begin(r.Context(), recovery.BeginCommand{Email: input.Email, NetworkActor: actor})
	if err != nil {
		s.logger.Error("begin credential recovery", "error", err)
	}
	response := map[string]any{"status": "accepted"}
	if s.exposeRecoveryToken && result.Delivered && s.recoveryTokens != nil {
		if message, ok := s.recoveryTokens.Latest(); ok && message.RecoveryID == result.RecoveryID {
			response["development_recovery_token"] = message.Token
		}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) completeRecovery(w http.ResponseWriter, r *http.Request) {
	if s.recovery == nil {
		writeProblem(w, http.StatusServiceUnavailable, "recovery_unconfigured", "credential recovery is not configured")
		return
	}
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := s.recovery.Complete(r.Context(), recovery.CompleteCommand{Token: input.Token, Password: input.Password})
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, recovery.ErrInvalidChallenge), errors.Is(err, recovery.ErrInvalidPassword):
		writeProblem(w, http.StatusBadRequest, "recovery_invalid", "the recovery link is invalid or expired, or the password does not meet policy")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "recovery_failed", "credential recovery could not be completed")
	}
}

func (s *Server) billingStatus(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.commercialRequest(w, r)
	if !ok {
		return
	}
	status, err := s.commercialAccess.Status(r.Context(), authenticated.Session.UserID, accountID)
	if err != nil {
		s.writeCommercialError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) createCheckoutSession(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.commercialRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		OfferCode string `json:"offer_code"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session, err := s.commercialAccess.Checkout(r.Context(), commercialaccess.CheckoutCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, OfferCode: input.OfferCode, RequestID: r.Header.Get("Idempotency-Key")})
	if err != nil {
		s.writeCommercialError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session_id": session.ID, "url": session.URL, "expires_at": session.ExpiresAt})
}

func (s *Server) createBillingPortalSession(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.commercialRequest(w, r)
	if !ok {
		return
	}
	session, err := s.commercialAccess.Portal(r.Context(), commercialaccess.PortalCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, RequestID: r.Header.Get("Idempotency-Key")})
	if err != nil {
		s.writeCommercialError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session_id": session.ID, "url": session.URL, "expires_at": session.ExpiresAt})
}

func (s *Server) commercialRequest(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, ids.AccountID, bool) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.commercialOrigin {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, "", false
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	if s.commercialAccess == nil {
		writeProblem(w, http.StatusServiceUnavailable, "billing_unconfigured", "billing is not configured")
		return sessions.Authenticated{}, "", false
	}
	raw := r.PathValue("accountID")
	if err := ids.Validate(raw); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "account ID is invalid")
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(raw), true
}

func (s *Server) writeCommercialError(w http.ResponseWriter, err error) {
	switch {
	case access.IsDenied(err, access.DenialOwnerEnrollment):
		writeProblem(w, http.StatusForbidden, "owner_security_enrollment_required", "add a passkey and save recovery codes before using owner authority")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before this privileged operation")
	case errors.Is(err, commercialaccess.ErrInvalidRequestID):
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "a UUID Idempotency-Key header is required")
	case errors.Is(err, commercialaccess.ErrOfferUnavailable):
		writeProblem(w, http.StatusNotFound, "offer_unavailable", "the selected offer is unavailable")
	case errors.Is(err, commercialaccess.ErrCustomerRequired):
		writeProblem(w, http.StatusConflict, "billing_customer_required", "this Account has not started billing")
	case errors.Is(err, commercialaccess.ErrSubscriptionExists):
		writeProblem(w, http.StatusConflict, "subscription_exists", "manage the existing subscription in the billing portal")
	case errors.Is(err, commercialaccess.ErrCheckoutInProgress):
		writeProblem(w, http.StatusConflict, "checkout_in_progress", "a checkout session is already in progress")
	case errors.Is(err, commercialaccess.ErrBillingUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "billing_unavailable", "billing is temporarily unavailable")
	case access.IsDenied(err, access.DenialRole), access.IsDenied(err, access.DenialMembership), access.IsDenied(err, access.DenialAccountUnavailable):
		writeProblem(w, http.StatusForbidden, "billing_denied", "billing access was denied")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "billing_failed", "billing could not be completed")
	}
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
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
	created, err := s.invitations.Create(r.Context(), invitations.CreateCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: ids.AccountID(rawAccountID), Email: input.Email, Role: input.Role})
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

func (s *Server) listMemberships(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.membershipRequest(w, r, false)
	if !ok {
		return
	}
	members, err := s.members.List(r.Context(), authenticated.Session.UserID, accountID)
	if err != nil {
		s.writeMembershipError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": members})
}

func (s *Server) changeMembershipRole(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.membershipRequest(w, r, true)
	if !ok {
		return
	}
	memberID := r.PathValue("membershipID")
	if ids.Validate(memberID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_membership_id", "Membership ID is invalid")
		return
	}
	var input struct {
		Role            accounts.MembershipRole `json:"role"`
		ExpectedVersion uint64                  `json:"expected_version"`
		Reason          string                  `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	member, err := s.members.ChangeRole(r.Context(), accountmembers.ChangeRoleCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: ids.MembershipID(memberID), ExpectedVersion: input.ExpectedVersion, Role: input.Role, Reason: input.Reason})
	if err != nil {
		s.writeMembershipError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"membership": member})
}

func (s *Server) removeMembership(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.membershipRequest(w, r, true)
	if !ok {
		return
	}
	memberID := r.PathValue("membershipID")
	if ids.Validate(memberID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_membership_id", "Membership ID is invalid")
		return
	}
	var input struct {
		ExpectedVersion uint64 `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.members.Remove(r.Context(), accountmembers.RemoveCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: ids.MembershipID(memberID), ExpectedVersion: input.ExpectedVersion, Reason: input.Reason}); err != nil {
		s.writeMembershipError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) suspendMembership(w http.ResponseWriter, r *http.Request) {
	s.changeMembershipState(w, r, true)
}

func (s *Server) reactivateMembership(w http.ResponseWriter, r *http.Request) {
	s.changeMembershipState(w, r, false)
}

func (s *Server) changeMembershipState(w http.ResponseWriter, r *http.Request, suspend bool) {
	authenticated, accountID, ok := s.membershipRequest(w, r, true)
	if !ok {
		return
	}
	memberID := r.PathValue("membershipID")
	if ids.Validate(memberID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_membership_id", "Membership ID is invalid")
		return
	}
	var input struct {
		ExpectedVersion uint64 `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	command := accountmembers.StateCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: ids.MembershipID(memberID), ExpectedVersion: input.ExpectedVersion, Reason: input.Reason}
	var member accountmembers.Member
	var err error
	if suspend {
		member, err = s.members.Suspend(r.Context(), command)
	} else {
		member, err = s.members.Reactivate(r.Context(), command)
	}
	if err != nil {
		s.writeMembershipError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"membership": member})
}

func (s *Server) leaveAccount(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.membershipRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion uint64 `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.members.Leave(r.Context(), accountmembers.LeaveCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, ExpectedVersion: input.ExpectedVersion, Reason: input.Reason}); err != nil {
		s.writeMembershipError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) transferOwnership(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.membershipRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		TargetMembershipID    ids.MembershipID `json:"target_membership_id"`
		ExpectedActorVersion  uint64           `json:"expected_actor_version"`
		ExpectedTargetVersion uint64           `json:"expected_target_version"`
		Reason                string           `json:"reason"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if ids.Validate(string(input.TargetMembershipID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_membership_id", "Target Membership ID is invalid")
		return
	}
	result, err := s.members.TransferOwnership(r.Context(), accountmembers.TransferOwnershipCommand{ActorUserID: authenticated.Session.UserID, Session: authenticated.Session, AccountID: accountID, TargetMembershipID: input.TargetMembershipID, ExpectedActorVersion: input.ExpectedActorVersion, ExpectedTargetVersion: input.ExpectedTargetVersion, Reason: input.Reason})
	if err != nil {
		s.writeMembershipError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) membershipRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, ids.AccountID, bool) {
	if s.members == nil {
		writeProblem(w, http.StatusServiceUnavailable, "membership_management_unconfigured", "Membership management is not configured")
		return sessions.Authenticated{}, "", false
	}
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, "", false
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	accountID := r.PathValue("accountID")
	if ids.Validate(accountID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account_id", "Account ID is invalid")
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(accountID), true
}

func (s *Server) writeMembershipError(w http.ResponseWriter, err error) {
	switch {
	case access.IsDenied(err, access.DenialOwnerEnrollment):
		writeProblem(w, http.StatusForbidden, "owner_security_enrollment_required", "add a passkey and save recovery codes before using owner authority")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before this privileged operation")
	case errors.Is(err, accountmembers.ErrMembershipNotFound):
		writeProblem(w, http.StatusNotFound, "membership_not_found", "the Membership was not found")
	case errors.Is(err, accountmembers.ErrVersionConflict):
		writeProblem(w, http.StatusConflict, "membership_version_conflict", "the Membership changed; reload before trying again")
	case errors.Is(err, accountmembers.ErrStateConflict):
		writeProblem(w, http.StatusConflict, "membership_state_conflict", "the Membership is not in a state that allows this change")
	case errors.Is(err, accountmembers.ErrOwnershipRequired):
		writeProblem(w, http.StatusConflict, "ownership_required", "transfer ownership before changing or removing the owner")
	case errors.Is(err, accountmembers.ErrRoleInvalid), errors.Is(err, accountmembers.ErrReasonRequired):
		writeProblem(w, http.StatusBadRequest, "invalid_membership_change", err.Error())
	case errors.Is(err, accountmembers.ErrTargetDenied), access.IsDenied(err, access.DenialRole), access.IsDenied(err, access.DenialMembership), access.IsDenied(err, access.DenialAccountUnavailable):
		writeProblem(w, http.StatusForbidden, "membership_denied", "Membership management was denied")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "membership_change_failed", "Membership management could not be completed")
	}
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
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
	writeJSON(w, http.StatusCreated, invitationAcceptanceResponse{Membership: acceptedMembershipResponse{ID: membership.ID, AccountID: membership.AccountID, UserID: membership.UserID, Role: membership.Role, State: membership.State, Version: membership.Version, CreatedAt: membership.CreatedAt}})
}

type invitationAcceptanceResponse struct {
	Membership acceptedMembershipResponse `json:"membership"`
}

type acceptedMembershipResponse struct {
	ID        ids.MembershipID         `json:"id"`
	AccountID ids.AccountID            `json:"account_id"`
	UserID    ids.UserID               `json:"user_id"`
	Role      accounts.MembershipRole  `json:"role"`
	State     accounts.MembershipState `json:"state"`
	Version   uint64                   `json:"version"`
	CreatedAt time.Time                `json:"created_at"`
}

func (s *Server) writeInvitationError(w http.ResponseWriter, err error) {
	switch {
	case access.IsDenied(err, access.DenialOwnerEnrollment):
		writeProblem(w, http.StatusForbidden, "owner_security_enrollment_required", "add a passkey and save recovery codes before using owner authority")
	case errors.Is(err, strongauth.ErrRequired):
		writeProblem(w, http.StatusForbidden, "strong_reauthentication_required", "confirm with a passkey before this privileged operation")
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
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
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
		if access.IsDenied(err, access.DenialOwnerEnrollment) {
			writeProblem(w, http.StatusForbidden, "owner_security_enrollment_required", "add a passkey and save recovery codes before entering this owner Account")
			return
		}
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
	actor, _ := networkactor.FromContext(r.Context())
	issued, err := s.authentication.Login(r.Context(), authentication.LoginCommand{Email: input.Email, Password: input.Password, ClientLabel: r.UserAgent(), NetworkActor: actor})
	if err != nil {
		if errors.Is(err, authentication.ErrInvalidCredentials) {
			writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "the email or password is incorrect")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "authentication_failed", "authentication could not be completed")
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "authenticated", "user_id": issued.Session.UserID, "expires_at": issued.Session.ExpiresAt, "authentication_method": issued.Session.AuthenticationMethod, "authentication_assurance": issued.Session.AuthenticationMethod.Assurance()})
}

func (s *Server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	if s.passkeys == nil {
		writeProblem(w, http.StatusServiceUnavailable, "passkeys_unconfigured", "passkey authentication is not configured")
		return
	}
	actor, _ := networkactor.FromContext(r.Context())
	result, err := s.passkeys.BeginLogin(r.Context(), actor)
	if err != nil {
		if errors.Is(err, passkeys.ErrInvalidCredential) {
			writeProblem(w, http.StatusTooManyRequests, "login_rate_limited", "try again later")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "passkey_login_unavailable", "passkey authentication could not be started")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) completePasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	if s.passkeys == nil {
		writeProblem(w, http.StatusServiceUnavailable, "passkeys_unconfigured", "passkey authentication is not configured")
		return
	}
	ceremonyID := r.PathValue("ceremonyID")
	if ids.Validate(ceremonyID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_ceremony", "the passkey ceremony is invalid")
		return
	}
	var input struct {
		Credential  json.RawMessage `json:"credential"`
		ClientLabel string          `json:"client_label"`
	}
	if err := decodePasskeyJSON(w, r, &input); err != nil || len(input.Credential) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a valid passkey credential is required")
		return
	}
	if strings.TrimSpace(input.ClientLabel) == "" {
		input.ClientLabel = r.UserAgent()
	}
	issued, err := s.passkeys.CompleteLogin(r.Context(), passkeys.LoginCommand{CeremonyID: ceremonyID, Response: input.Credential, ClientLabel: input.ClientLabel})
	if err != nil {
		if errors.Is(err, passkeys.ErrInvalidCeremony) || errors.Is(err, passkeys.ErrInvalidCredential) || errors.Is(err, passkeys.ErrCredentialStateConflict) {
			writeProblem(w, http.StatusUnauthorized, "passkey_login_failed", "the passkey could not be verified")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "passkey_login_unavailable", "passkey authentication could not be completed")
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "authenticated", "user_id": issued.Session.UserID, "expires_at": issued.Session.ExpiresAt, "authentication_method": issued.Session.AuthenticationMethod, "authentication_assurance": issued.Session.AuthenticationMethod.Assurance()})
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, false)
	if !ok {
		return
	}
	credentials, err := s.passkeys.Credentials(r.Context(), authenticated.Session.UserID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "passkeys_unavailable", "passkeys could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": credentials})
}

func (s *Server) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	result, err := s.passkeys.BeginRegistration(r.Context(), authenticated.Session)
	if err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) completePasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	ceremonyID := r.PathValue("ceremonyID")
	if ids.Validate(ceremonyID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_ceremony", "the passkey ceremony is invalid")
		return
	}
	var input struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := decodePasskeyJSON(w, r, &input); err != nil || len(input.Credential) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a name and valid passkey credential are required")
		return
	}
	credential, err := s.passkeys.CompleteRegistration(r.Context(), authenticated.Session, ceremonyID, input.Name, input.Credential)
	if err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, credential)
}

func (s *Server) beginPasskeyReauthentication(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	result, err := s.passkeys.BeginReauthentication(r.Context(), authenticated.Session)
	if err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) completePasskeyReauthentication(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	ceremonyID := r.PathValue("ceremonyID")
	if ids.Validate(ceremonyID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_ceremony", "the passkey ceremony is invalid")
		return
	}
	var input struct {
		Credential json.RawMessage `json:"credential"`
	}
	if err := decodePasskeyJSON(w, r, &input); err != nil || len(input.Credential) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a valid passkey credential is required")
		return
	}
	if err := s.passkeys.CompleteReauthentication(r.Context(), authenticated.Session, ceremonyID, input.Credential); err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	if err := s.passkeys.Delete(r.Context(), authenticated.Session, r.PathValue("credentialID")); err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodePasskeyJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a valid passkey name is required")
		return
	}
	if err := s.passkeys.Rename(r.Context(), authenticated.Session, r.PathValue("credentialID"), input.Name); err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) compromisePasskey(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.passkeyRequest(w, r, true)
	if !ok {
		return
	}
	if err := s.passkeys.Compromise(r.Context(), authenticated.Session, r.PathValue("credentialID")); err != nil {
		s.writePasskeyMutationError(w, err)
		return
	}
	s.clearIdentityCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) passkeyRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, bool) {
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, false
	}
	if s.passkeys == nil {
		writeProblem(w, http.StatusServiceUnavailable, "passkeys_unconfigured", "passkeys are not configured")
		return sessions.Authenticated{}, false
	}
	return s.authenticateSession(w, r)
}

func (s *Server) writePasskeyMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, passkeys.ErrReauthenticationNeeded):
		writeProblem(w, http.StatusForbidden, "reauthentication_required", "confirm your identity before this sensitive operation")
	case errors.Is(err, passkeys.ErrCredentialNotFound):
		writeProblem(w, http.StatusNotFound, "passkey_not_found", "the passkey was not found")
	case errors.Is(err, passkeys.ErrCredentialNameInvalid):
		writeProblem(w, http.StatusBadRequest, "passkey_name_invalid", "the passkey name must contain 2 to 80 characters")
	case errors.Is(err, passkeys.ErrCredentialLimit):
		writeProblem(w, http.StatusConflict, "passkey_limit_reached", "the identity has reached its passkey limit")
	case errors.Is(err, passkeys.ErrCredentialStateConflict):
		writeProblem(w, http.StatusConflict, "passkey_state_changed", "the passkey state changed; try again")
	case errors.Is(err, passkeys.ErrRecoveryCodeRequired):
		writeProblem(w, http.StatusForbidden, "recovery_code_required", "use an unused recovery code before replacing a lost passkey")
	case errors.Is(err, passkeys.ErrRecoveryCodesRequired):
		writeProblem(w, http.StatusConflict, "recovery_codes_required", "configure recovery codes before removing the last passkey")
	case errors.Is(err, passkeys.ErrInvalidCeremony), errors.Is(err, passkeys.ErrInvalidCredential):
		writeProblem(w, http.StatusBadRequest, "passkey_invalid", "the passkey ceremony is invalid or expired")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "passkey_operation_failed", "the passkey operation could not be completed")
	}
}

func (s *Server) recoveryCodeStatus(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.recoveryCodeRequest(w, r, false)
	if !ok {
		return
	}
	status, err := s.recoveryCodes.Status(r.Context(), authenticated.Session)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "recovery_codes_unavailable", "recovery code status could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) rotateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.recoveryCodeRequest(w, r, true)
	if !ok {
		return
	}
	result, err := s.recoveryCodes.Rotate(r.Context(), authenticated.Session)
	if err != nil {
		if errors.Is(err, strongauth.ErrRequired) {
			writeProblem(w, http.StatusForbidden, "passkey_reauthentication_required", "confirm with a passkey before replacing recovery codes")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "recovery_codes_unavailable", "recovery codes could not be replaced")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) consumeRecoveryCode(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.recoveryCodeRequest(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "a recovery code is required")
		return
	}
	if err := s.recoveryCodes.Consume(r.Context(), authenticated.Session, input.Code); err != nil {
		switch {
		case errors.Is(err, recoverycodes.ErrPasswordRequired):
			writeProblem(w, http.StatusForbidden, "password_reauthentication_required", "confirm your password before using a recovery code")
		case errors.Is(err, recoverycodes.ErrInvalidCode):
			writeProblem(w, http.StatusBadRequest, "recovery_code_invalid", "the recovery code is invalid or already used")
		default:
			writeProblem(w, http.StatusServiceUnavailable, "recovery_codes_unavailable", "the recovery code could not be verified")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recoveryCodeRequest(w http.ResponseWriter, r *http.Request, mutation bool) (sessions.Authenticated, bool) {
	if mutation && !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return sessions.Authenticated{}, false
	}
	if s.recoveryCodes == nil {
		writeProblem(w, http.StatusServiceUnavailable, "recovery_codes_unconfigured", "recovery codes are not configured")
		return sessions.Authenticated{}, false
	}
	return s.authenticateSession(w, r)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	active, err := s.sessions.Active(r.Context(), authenticated.Session.UserID, authenticated.Session.ID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "sessions_unavailable", "active sessions could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": active})
}

func (s *Server) listSecurityEvents(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	events, err := s.sessions.SecurityEvents(r.Context(), authenticated.Session.UserID, 50)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "security_events_unavailable", "security history could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	raw := r.PathValue("sessionID")
	if ids.Validate(raw) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_session_id", "session ID is invalid")
		return
	}
	revoked, err := s.sessions.RevokeOwned(r.Context(), authenticated.Session.UserID, ids.SessionID(raw))
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "session_revoke_failed", "the session could not be revoked")
		return
	}
	if !revoked {
		writeProblem(w, http.StatusNotFound, "session_not_found", "the session was not found")
		return
	}
	if ids.SessionID(raw) == authenticated.Session.ID {
		s.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	if err := s.sessions.RevokeAll(r.Context(), authenticated.Session.UserID); err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "session_revoke_failed", "sessions could not be revoked")
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) {
	if !s.validSessionMutationOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	authenticated, ok := s.authenticateSession(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	err := s.authentication.Reauthenticate(r.Context(), authentication.ReauthenticateCommand{UserID: authenticated.Session.UserID, SessionID: authenticated.Session.ID, Password: input.Password})
	if errors.Is(err, authentication.ErrInvalidCredentials) {
		writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "the password is incorrect")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "reauthentication_failed", "password confirmation could not be completed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validSessionMutationOrigin(r *http.Request) bool {
	return s.cookie.Origin == "" || r.Header.Get("Origin") == s.cookie.Origin
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

func (s *Server) clearIdentityCookies(w http.ResponseWriter) {
	s.clearSessionCookie(w)
	http.SetCookie(w, &http.Cookie{Name: s.cookie.AccountName, Value: "", Path: "/", Domain: s.cookie.Domain, HttpOnly: true, Secure: s.cookie.Secure, SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
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
	offers := effectiveCatalogOffers(catalog, time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"version": catalog.Version, "published_at": catalog.PublishedAt, "packages": catalog.Packages, "limits": catalog.EffectiveLimitDefinitions(), "plans": catalog.Plans, "offers": offers})
}

func effectiveCatalogOffers(publication catalog.PublishedCatalog, now time.Time) []catalogOffer {
	offers := make([]catalogOffer, 0, len(publication.Offers))
	for _, offer := range publication.Offers {
		if !offer.Published || offer.EffectiveFrom.After(now) {
			continue
		}
		offers = append(offers, catalogOffer{Code: offer.Code, PlanCode: offer.PlanCode, PlanVersion: offer.PlanVersion, Currency: offer.Currency, AmountMinor: offer.AmountMinor, BillingInterval: offer.BillingInterval, EffectiveFrom: offer.EffectiveFrom})
	}
	return offers
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
		OfferCode   string `json:"offer_code"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.registrations.Begin(r.Context(), registration.BeginCommand{Email: input.Email, DisplayName: input.DisplayName, AccountName: input.AccountName, Region: input.Region, OfferCode: input.OfferCode})
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
	case errors.Is(err, registration.ErrOfferUnavailable):
		writeProblem(w, http.StatusBadRequest, "offer_unavailable", "the selected offer is unavailable")
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
	return decodeJSONLimit(w, r, target, 32<<10)
}

func decodePasskeyJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONLimit(w, r, target, 256<<10)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, target any, maximum int64) error {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		return errors.New("content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
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
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
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
