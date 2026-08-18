// Package approuter authenticates global sessions, resolves Account placement,
// signs least-authority route context, and proxies to the assigned cell.
package approuter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

const (
	DefaultMaxRequestBody  = int64(1 << 20)
	DefaultMaxResponseBody = int64(4 << 20)
)

type SessionAuthenticator interface {
	Authenticate(context.Context, string) (sessions.Authenticated, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type TokenSigner interface {
	Issue(string, routecontext.Authority, routecontext.Binding) (string, error)
}

type AccountDirectory interface {
	Resolve(context.Context, ids.AccountID, ids.CellID, uint64) (accountdirectory.Route, error)
}

type Config struct {
	SessionCookieName string
	SecureCookies     bool
	TrustedOrigins    []string
	MaxRequestBody    int64
	MaxResponseBody   int64
	Transport         http.RoundTripper
}

type Server struct {
	sessions   SessionAuthenticator
	authorizer Authorizer
	signer     TokenSigner
	directory  AccountDirectory
	ids        ids.Generator
	logger     *slog.Logger
	config     Config
	origins    map[string]struct{}
	client     *http.Client
}

func New(sessionService SessionAuthenticator, authorizer Authorizer, directory AccountDirectory, signer TokenSigner, generator ids.Generator, config Config, logger *slog.Logger) (*Server, error) {
	if sessionService == nil || authorizer == nil || directory == nil || signer == nil || generator == nil || logger == nil || len(config.TrustedOrigins) == 0 {
		return nil, errors.New("app router dependencies and trusted origins are required")
	}
	if config.SessionCookieName == "" {
		config.SessionCookieName = "__Host-spyglass_session"
	}
	if config.MaxRequestBody == 0 {
		config.MaxRequestBody = DefaultMaxRequestBody
	}
	if config.MaxResponseBody == 0 {
		config.MaxResponseBody = DefaultMaxResponseBody
	}
	if config.MaxRequestBody <= 0 || config.MaxRequestBody > 16<<20 || config.MaxResponseBody <= 0 || config.MaxResponseBody > 32<<20 {
		return nil, errors.New("app router body limits are invalid")
	}
	origins := make(map[string]struct{}, len(config.TrustedOrigins))
	for _, raw := range config.TrustedOrigins {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || (config.SecureCookies && parsed.Scheme != "https") {
			return nil, errors.New("trusted origins must be absolute origins")
		}
		origins[parsed.Scheme+"://"+parsed.Host] = struct{}{}
	}
	transport := config.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("cell redirects are not allowed") }}
	return &Server{sessions: sessionService, authorizer: authorizer, directory: directory, signer: signer, ids: generator, logger: logger, config: config, origins: origins, client: client}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/accounts/{accountID}/{resource...}", s.route)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	requirement, allowed := routeRequirement(r.Method, r.PathValue("resource"))
	if !allowed {
		writeProblem(w, http.StatusNotFound, "route_not_found", "the requested application route does not exist")
		return
	}
	if requirement.Mutation && !s.validOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied", "the request origin is not allowed")
		return
	}
	cookie, err := r.Cookie(s.config.SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeProblem(w, http.StatusUnauthorized, "authentication_required", "sign in to continue")
		return
	}
	authenticated, err := s.sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "authentication_required", "sign in to continue")
		return
	}
	if authenticated.RotatedToken != "" {
		http.SetCookie(w, &http.Cookie{Name: s.config.SessionCookieName, Value: authenticated.RotatedToken, Path: "/", HttpOnly: true, Secure: s.config.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if ids.Validate(string(accountID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account", "Account ID is invalid")
		return
	}
	actor := access.Actor{UserID: authenticated.Session.UserID}
	accountContext, err := s.authorizer.Authorize(r.Context(), actor, accountID, requirement)
	if err != nil {
		s.writeAuthorizationError(w, err)
		return
	}
	cellRoute, err := s.directory.Resolve(r.Context(), accountID, accountContext.CellID, accountContext.PlacementGeneration)
	if err != nil {
		s.logger.Error("resolve Account route", "cell_id", accountContext.CellID, "placement_generation", accountContext.PlacementGeneration, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the assigned Spyglass cell route could not be verified")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.config.MaxRequestBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the router limit")
		return
	}
	binding, err := routecontext.BindRequest(r, body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return
	}
	requestID := s.ids.New()
	operationID := ""
	if requirement.Mutation {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || ids.Validate(strings.TrimSpace(values[0])) != nil {
			writeProblem(w, http.StatusBadRequest, "idempotency_key_required", "a UUID Idempotency-Key is required for mutations")
			return
		}
		operationID = strings.TrimSpace(values[0])
	}
	authority := routecontext.Authority{RequestID: requestID, OperationID: operationID, AccountID: accountContext.AccountID, ActorKind: "user", ActorID: string(authenticated.Session.UserID), Role: string(accountContext.Role), CellID: accountContext.CellID, PlacementGeneration: accountContext.PlacementGeneration, EntitlementVersion: accountContext.EntitlementVersion, PackageAccess: packageClaim(accountContext.PackageAccess)}
	token, err := s.signer.Issue(routecontext.Audience(accountContext.CellID), authority, binding)
	if err != nil {
		s.logger.Error("issue cell route context", "request_id", requestID, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the Account route could not be established")
		return
	}
	outboundURL := cellRoute.Origin
	outboundURL.Path, outboundURL.RawPath, outboundURL.RawQuery = r.URL.Path, r.URL.RawPath, r.URL.RawQuery
	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, outboundURL.String(), bytes.NewReader(body))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "the request could not be routed")
		return
	}
	copyRequestHeader(outbound.Header, r.Header, "Accept", "Content-Type", "If-Match", "Idempotency-Key")
	outbound.Header.Set(cellapi.RouteContextHeader, token)
	outbound.Header.Set("X-Request-ID", requestID)
	response, err := s.client.Do(outbound)
	if err != nil {
		s.logger.Error("cell request failed", "request_id", requestID, "cell_id", accountContext.CellID, "error", err)
		writeProblem(w, http.StatusBadGateway, "cell_unavailable", "the assigned Spyglass cell did not respond")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, s.config.MaxResponseBody+1))
	if err != nil || int64(len(responseBody)) > s.config.MaxResponseBody {
		writeProblem(w, http.StatusBadGateway, "invalid_cell_response", "the cell response exceeded the router limit")
		return
	}
	copyResponseHeader(w.Header(), response.Header, "Content-Type", "ETag", "Cache-Control")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func routeRequirement(method, resource string) (access.Requirement, bool) {
	method = strings.ToUpper(method)
	if resource == "context" {
		return access.Requirement{}, method == http.MethodGet
	}
	parts := strings.Split(resource, "/")
	if len(parts) == 1 && parts[0] == "work-items" {
		return access.Requirement{Package: catalog.PackageWork, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) == 2 && parts[0] == "work-items" && parts[1] == "summary" {
		return access.Requirement{Package: catalog.PackageWork}, method == http.MethodGet
	}
	if len(parts) == 2 && parts[0] == "work-items" && ids.Validate(parts[1]) == nil {
		return access.Requirement{Package: catalog.PackageWork}, method == http.MethodGet
	}
	if len(parts) == 3 && parts[0] == "work-items" && ids.Validate(parts[1]) == nil {
		switch parts[2] {
		case "children":
			return access.Requirement{Package: catalog.PackageWork}, method == http.MethodGet
		case "transitions":
			return access.Requirement{Package: catalog.PackageWork, Mutation: true}, method == http.MethodPost
		case "assignment":
			return access.Requirement{Package: catalog.PackageWork, Mutation: true}, method == http.MethodPatch
		}
	}
	return access.Requirement{}, false
}

func packageClaim(value *entitlements.PackageAccess) *routecontext.PackageAccess {
	if value == nil {
		return nil
	}
	limits := make(map[string]int64, len(value.Limits))
	for code, limit := range value.Limits {
		limits[string(code)] = limit
	}
	policies := make(map[string]routecontext.LimitPolicy, len(value.LimitPolicies))
	for code, policy := range value.LimitPolicies {
		policies[string(code)] = routecontext.LimitPolicy{Kind: string(policy.Kind), Combine: string(policy.Combine), ReservationTTLSeconds: policy.ReservationTTLSeconds}
	}
	return &routecontext.PackageAccess{Code: string(value.Code), Version: value.Version, Mode: string(value.Mode), Limits: limits, LimitPolicies: policies}
}

func (s *Server) validOrigin(r *http.Request) bool {
	_, ok := s.origins[r.Header.Get("Origin")]
	return ok
}

func (s *Server) writeAuthorizationError(w http.ResponseWriter, err error) {
	var denied *access.DeniedError
	if errors.As(err, &denied) {
		status := http.StatusForbidden
		if denied.Code == access.DenialUnauthenticated {
			status = http.StatusUnauthorized
		}
		if denied.Code == access.DenialAccountUnavailable {
			status = http.StatusConflict
		}
		writeProblem(w, status, string(denied.Code), "the selected Account does not allow this operation")
		return
	}
	s.logger.Error("authorize routed Account request", "error", err)
	writeProblem(w, http.StatusServiceUnavailable, "authorization_unavailable", "Account access could not be verified")
}

func copyRequestHeader(destination, source http.Header, names ...string) {
	for _, name := range names {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}
func copyResponseHeader(destination, source http.Header, names ...string) {
	for _, name := range names {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("app router panic", "value", value)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
