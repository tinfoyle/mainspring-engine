// Package operationsapi exposes the staff-only Operations Console boundary.
// It never constructs a customer actor: an authenticated staff session may
// only invoke audited operations projections or an exact, bounded support
// grant owned by that same staff identity.
package operationsapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrightsadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const DefaultMaxBody int64 = 64 << 10

type Console interface {
	Staff(context.Context, ids.UserID) (operations.Staff, error)
	RecordAuthentication(context.Context, ids.UserID, ids.SessionID) (operations.Staff, error)
	RecordLogout(context.Context, ids.UserID, ids.SessionID) error
	Lookup(context.Context, ids.UserID, operations.LookupQuery) ([]operations.LookupResult, error)
	CreateGrant(context.Context, operationsconsole.CreateGrantCommand) (operations.SupportGrant, error)
	ViewAccount(context.Context, ids.UserID, ids.OperationsSupportGrantID, operations.AuditReason) (operations.AccountView, error)
	RevokeGrant(context.Context, ids.UserID, ids.OperationsSupportGrantID, uint64, operations.AuditReason) (operations.SupportGrant, error)
	Analytics(context.Context, ids.UserID, analyticsreport.Query, operations.AuditReason) (analyticsreport.Report, error)
}

type PasskeyLogin interface {
	BeginLogin(context.Context, [32]byte) (passkeys.BeginResult, error)
	CompleteLogin(context.Context, passkeys.LoginCommand) (sessions.Issued, error)
}

type SessionService interface {
	Authenticate(context.Context, string) (sessions.Authenticated, error)
	RevokeOwned(context.Context, ids.UserID, ids.SessionID) (bool, error)
}

type BillingOperator interface {
	Inspect(context.Context, int, string, string, string, string) ([]billingadmin.Record, string, error)
	ReplayEvent(context.Context, string, string, string, string, string) (billingadmin.Record, string, error)
	QueueRefresh(context.Context, string, string, string, string, string) (billingadmin.Record, string, error)
}

type PrivacyOperator interface {
	ListOpen(context.Context, time.Time, int, string, string, string) ([]privacyrightsadmin.QueueItem, error)
	Inspect(context.Context, ids.PrivacyRightsRequestID, string, string, string) (privacy.RightsRequest, error)
	StartReview(context.Context, ids.PrivacyRightsRequestID, uint64, string, string, string) (privacy.RightsRequest, error)
	Resolve(context.Context, ids.PrivacyRightsRequestID, uint64, privacy.RightsState, privacyrightsadmin.ResolutionEvidence, string, string, string) (privacy.RightsRequest, error)
}

type AffiliateOperator interface {
	Inspect(context.Context, ids.AffiliateID, string, string, string) (affiliates.Enrollment, error)
	InspectRisk(context.Context, ids.AffiliateID, string, string, string) (affiliateadmin.RiskSummary, error)
	Transition(context.Context, ids.AffiliateID, uint64, affiliates.EnrollmentState, string, string, string) (affiliates.Enrollment, error)
}

type OperatorServices struct {
	Billing   BillingOperator
	Privacy   PrivacyOperator
	Affiliate AffiliateOperator
	Traffic   interface {
		Report(context.Context, ids.UserID, trafficreport.Query, operations.AuditReason) (trafficreport.Report, error)
	}
}

type Cookie struct {
	Name   string
	Secure bool
}

type Server struct {
	console     Console
	passkeys    PasskeyLogin
	sessions    SessionService
	operators   OperatorServices
	environment string
	cookie      Cookie
	origin      string
	maxBody     int64
	logger      *slog.Logger
}

func New(console Console, passkeyLogin PasskeyLogin, sessionService SessionService, operators OperatorServices, cookie Cookie, origin, environment string, maxBody int64, logger *slog.Logger) (*Server, error) {
	origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
	environment = strings.TrimSpace(environment)
	if console == nil || passkeyLogin == nil || sessionService == nil || operators.Billing == nil || operators.Privacy == nil || operators.Affiliate == nil || origin == "" || environment == "" || logger == nil {
		return nil, errors.New("operations API dependencies and origin are required")
	}
	if cookie.Name == "" {
		if cookie.Secure {
			cookie.Name = "__Host-spyglass_operations"
		} else {
			cookie.Name = "spyglass_development_operations"
		}
	}
	if maxBody == 0 {
		maxBody = DefaultMaxBody
	}
	if maxBody < 1024 || maxBody > 1<<20 {
		return nil, errors.New("operations API maximum request body is invalid")
	}
	return &Server{console: console, passkeys: passkeyLogin, sessions: sessionService, operators: operators, cookie: cookie, origin: origin, environment: environment, maxBody: maxBody, logger: logger}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", server.live)
	mux.HandleFunc("GET /health/ready", server.ready)
	mux.HandleFunc("POST /api/operations/v1/passkey-login/challenges", server.beginPasskeyLogin)
	mux.HandleFunc("POST /api/operations/v1/passkey-login/challenges/{ceremonyID}/complete", server.completePasskeyLogin)
	mux.HandleFunc("GET /api/operations/v1/session", server.currentSession)
	mux.HandleFunc("DELETE /api/operations/v1/session", server.logout)
	mux.HandleFunc("POST /api/operations/v1/lookups", server.lookup)
	mux.HandleFunc("POST /api/operations/v1/support-grants", server.createGrant)
	mux.HandleFunc("POST /api/operations/v1/support-grants/{grantID}/views", server.viewAccount)
	mux.HandleFunc("POST /api/operations/v1/support-grants/{grantID}/revocations", server.revokeGrant)
	mux.HandleFunc("POST /api/operations/v1/analytics/reports", server.analytics)
	mux.HandleFunc("POST /api/operations/v1/traffic/reports", server.traffic)
	mux.HandleFunc("POST /api/operations/v1/billing/failures/reports", server.billingFailures)
	mux.HandleFunc("POST /api/operations/v1/billing/events/{eventID}/replays", server.replayBillingEvent)
	mux.HandleFunc("POST /api/operations/v1/billing/subscriptions/{subscriptionID}/refreshes", server.refreshBillingSubscription)
	mux.HandleFunc("POST /api/operations/v1/privacy-rights/reports/open", server.openPrivacyRights)
	mux.HandleFunc("POST /api/operations/v1/privacy-rights/{requestID}/inspections", server.inspectPrivacyRight)
	mux.HandleFunc("POST /api/operations/v1/privacy-rights/{requestID}/review-starts", server.startPrivacyReview)
	mux.HandleFunc("POST /api/operations/v1/privacy-rights/{requestID}/resolutions", server.resolvePrivacyRight)
	mux.HandleFunc("POST /api/operations/v1/affiliates/{affiliateID}/inspections", server.inspectAffiliate)
	mux.HandleFunc("POST /api/operations/v1/affiliates/{affiliateID}/risk-inspections", server.inspectAffiliateRisk)
	mux.HandleFunc("POST /api/operations/v1/affiliates/{affiliateID}/transitions", server.transitionAffiliate)
	return server.securityHeaders(mux)
}

func (server *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

func (server *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (server *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, request)
	})
}

func (server *Server) validMutationOrigin(request *http.Request) bool {
	return request.Header.Get("Origin") == server.origin && request.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (server *Server) authenticate(w http.ResponseWriter, request *http.Request) (sessions.Authenticated, operations.Staff, bool) {
	cookie, err := request.Cookie(server.cookie.Name)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "authentication_required", "staff authentication is required")
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	authenticated, err := server.sessions.Authenticate(request.Context(), cookie.Value)
	if err != nil {
		server.clearCookie(w)
		writeProblem(w, http.StatusUnauthorized, "session_invalid", "the staff session is invalid or expired")
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	if authenticated.Session.AuthenticationMethod != sessions.AuthenticationMethodPasskey {
		server.clearCookie(w)
		writeProblem(w, http.StatusUnauthorized, "passkey_required", "staff sessions require passkey authentication")
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	staff, err := server.console.Staff(request.Context(), authenticated.Session.UserID)
	if err != nil {
		server.clearCookie(w)
		writeProblem(w, http.StatusForbidden, "staff_access_denied", "staff access is unavailable")
		return sessions.Authenticated{}, operations.Staff{}, false
	}
	if authenticated.RotatedToken != "" {
		server.setCookie(w, authenticated.RotatedToken, authenticated.Session.ExpiresAt)
	}
	return authenticated, staff, true
}

func (server *Server) decode(w http.ResponseWriter, request *http.Request, value any) bool {
	request.Body = http.MaxBytesReader(w, request.Body, server.maxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "the request body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "the request must contain one JSON value")
		return false
	}
	return true
}

func (server *Server) setCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{Name: server.cookie.Name, Value: token, Path: "/", HttpOnly: true, Secure: server.cookie.Secure,
		SameSite: http.SameSiteStrictMode, Expires: expiresAt.UTC(), MaxAge: int(time.Until(expiresAt).Seconds())})
}

func (server *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: server.cookie.Name, Value: "", Path: "/", HttpOnly: true, Secure: server.cookie.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}
