// Package approuter authenticates global sessions, resolves Account placement,
// signs least-authority route context, and proxies to the assigned cell.
package approuter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/requestbody"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	DefaultMaxRequestBody  = int64(1 << 20)
	DefaultMaxResponseBody = int64(4 << 20)
	objectUploadOverhead   = int64(1 << 20)
	objectUploadTimeout    = 2 * time.Minute
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
	uploads    *http.Client
	transport  transportCounters
}

type TransportStats struct {
	RetryAttempts  uint64 `json:"retry_attempts"`
	RetryRecovered uint64 `json:"retry_recovered"`
	RequestsFailed uint64 `json:"requests_failed"`
}

type transportCounters struct {
	retryAttempts  atomic.Uint64
	retryRecovered atomic.Uint64
	requestsFailed atomic.Uint64
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
	rejectRedirect := func(*http.Request, []*http.Request) error { return errors.New("cell redirects are not allowed") }
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: rejectRedirect}
	uploads := &http.Client{Transport: transport, Timeout: objectUploadTimeout, CheckRedirect: rejectRedirect}
	return &Server{sessions: sessionService, authorizer: authorizer, directory: directory, signer: signer, ids: generator, logger: logger, config: config, origins: origins, client: client, uploads: uploads}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/accounts/{accountID}/{resource...}", s.route)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) TransportStats() TransportStats {
	return TransportStats{
		RetryAttempts:  s.transport.retryAttempts.Load(),
		RetryRecovered: s.transport.retryRecovered.Load(),
		RequestsFailed: s.transport.requestsFailed.Load(),
	}
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
	maximumBody := s.config.MaxRequestBody
	if maximum, objectUpload := objectUploadLimit(r.Method, r.PathValue("resource")); objectUpload {
		maximumBody = maximum
	}
	body, err := requestbody.Read(r.Body, maximumBody)
	if err != nil {
		if errors.Is(err, requestbody.ErrTooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the router limit")
			return
		}
		s.logger.Error("capture routed request body", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the routed request body could not be secured")
		return
	}
	defer body.Close()
	additionalRequirements, err := additionalRouteRequirements(r.Method, r.PathValue("resource"), body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "the routed request body is invalid")
		return
	}
	var additionalPackageAccesses []routecontext.PackageAccess
	for _, additional := range additionalRequirements {
		additionalContext, additionalErr := s.authorizer.Authorize(r.Context(), actor, accountID, additional)
		if additionalErr != nil {
			s.writeAuthorizationError(w, additionalErr)
			return
		}
		if additionalContext.AccountID != accountContext.AccountID || additionalContext.CellID != accountContext.CellID ||
			additionalContext.PlacementGeneration != accountContext.PlacementGeneration || additionalContext.EntitlementVersion != accountContext.EntitlementVersion ||
			additionalContext.Role != accountContext.Role || additionalContext.PackageAccess == nil {
			writeProblem(w, http.StatusServiceUnavailable, "authorization_unavailable", "cross-package Account authority could not be verified")
			return
		}
		additionalPackageAccesses = append(additionalPackageAccesses, *packageClaim(additionalContext.PackageAccess))
	}
	cellRoute, err := s.directory.Resolve(r.Context(), accountID, accountContext.CellID, accountContext.PlacementGeneration)
	if err != nil {
		s.logger.Error("resolve Account route", "cell_id", accountContext.CellID, "placement_generation", accountContext.PlacementGeneration, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the assigned Spyglass cell route could not be verified")
		return
	}
	binding, err := routecontext.BindRequestDigest(r, body.SHA256())
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return
	}
	operationID := ""
	if requirement.Mutation {
		values := r.Header.Values("Idempotency-Key")
		if len(values) != 1 || ids.Validate(strings.TrimSpace(values[0])) != nil {
			writeProblem(w, http.StatusBadRequest, "idempotency_key_required", "a UUID Idempotency-Key is required for mutations")
			return
		}
		operationID = strings.TrimSpace(values[0])
	}
	authority := routecontext.Authority{OperationID: operationID, AccountID: accountContext.AccountID, ActorKind: "user", ActorID: string(authenticated.Session.UserID), Role: string(accountContext.Role), CellID: accountContext.CellID, PlacementGeneration: accountContext.PlacementGeneration, EntitlementVersion: accountContext.EntitlementVersion, PackageAccess: packageClaim(accountContext.PackageAccess), PackageAccesses: additionalPackageAccesses}
	if r.Method == http.MethodPost && r.PathValue("resource") == "integrations/web-research/read" {
		authority.DelegatedWorkloadIDs = []string{"integration-web-research"}
	}
	authority.StrongAuthenticatedAt = strongAuthenticatedAt(authenticated.Session)
	requestID := s.ids.New()
	outbound, err := s.newCellRequest(r, cellRoute.Origin, body, authority, binding, requestID)
	if err != nil {
		s.logger.Error("build cell route request", "request_id", requestID, "cell_id", accountContext.CellID)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the Account route could not be established")
		return
	}
	client := s.client
	if _, objectUpload := objectUploadLimit(r.Method, r.PathValue("resource")); objectUpload {
		client = s.uploads
	}
	response, err := client.Do(outbound)
	if err != nil && response == nil && r.Context().Err() == nil {
		s.transport.retryAttempts.Add(1)
		requestID = s.ids.New()
		outbound, buildErr := s.newCellRequest(r, cellRoute.Origin, body, authority, binding, requestID)
		if buildErr != nil {
			s.logger.Error("build cell retry request", "request_id", requestID, "cell_id", accountContext.CellID)
			writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable", "the Account route could not be established")
			return
		}
		response, err = client.Do(outbound)
		if err == nil {
			s.transport.retryRecovered.Add(1)
		}
	}
	if err != nil {
		closeResponse(response)
		s.transport.requestsFailed.Add(1)
		s.logger.Error("cell request failed", "request_id", requestID, "cell_id", accountContext.CellID)
		w.Header().Set("X-Request-ID", requestID)
		writeProblem(w, http.StatusBadGateway, "cell_unavailable", "the assigned Spyglass cell did not respond")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, s.config.MaxResponseBody+1))
	if err != nil || int64(len(responseBody)) > s.config.MaxResponseBody {
		writeProblem(w, http.StatusBadGateway, "invalid_cell_response", "the cell response exceeded the router limit")
		return
	}
	copyResponseHeader(w.Header(), response.Header, "Content-Type", "ETag", "Location", "Cache-Control")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
}

func (s *Server) newCellRequest(inbound *http.Request, origin url.URL, body *requestbody.Capture, authority routecontext.Authority, binding routecontext.Binding, requestID string) (*http.Request, error) {
	authority.RequestID = requestID
	token, err := s.signer.Issue(routecontext.Audience(authority.CellID), authority, binding)
	if err != nil {
		return nil, err
	}
	origin.Path, origin.RawPath, origin.RawQuery = inbound.URL.Path, inbound.URL.RawPath, inbound.URL.RawQuery
	reader, err := body.Open()
	if err != nil {
		return nil, err
	}
	outbound, err := http.NewRequestWithContext(inbound.Context(), inbound.Method, origin.String(), reader)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	outbound.ContentLength = body.Size()
	copyRequestHeader(outbound.Header, inbound.Header, "Accept", "Content-Type", "If-Match", "Idempotency-Key")
	outbound.Header.Set(routecontext.HeaderName, token)
	outbound.Header.Set("X-Request-ID", requestID)
	return outbound, nil
}

func objectUploadLimit(method, resource string) (int64, bool) {
	if !strings.EqualFold(method, http.MethodPost) {
		return 0, false
	}
	if resource == "knowledge/documents" {
		return knowledgedomain.MaximumDocumentBytes + objectUploadOverhead, true
	}
	parts := strings.Split(resource, "/")
	if len(parts) == 4 && parts[0] == "marketing" && parts[1] == "campaigns" && ids.Validate(parts[2]) == nil && parts[3] == "asset-revisions" {
		return int64(marketingdomain.MaximumContentBytes) + objectUploadOverhead, true
	}
	return 0, false
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
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
	if len(parts) == 1 && parts[0] == "agent-boardrooms" {
		return access.Requirement{Package: catalog.PackageAgents, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) == 3 && parts[0] == "agent-boardrooms" && ids.Validate(parts[1]) == nil {
		switch parts[2] {
		case "personas":
			return access.Requirement{Package: catalog.PackageAgents, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
		case "conversations":
			return access.Requirement{Package: catalog.PackageAgents}, method == http.MethodGet
		case "runs":
			return access.Requirement{Package: catalog.PackageAgents, Mutation: true}, method == http.MethodPost
		case "manager":
			return access.Requirement{Package: catalog.PackageAgents, Mutation: true}, method == http.MethodPut
		}
	}
	if len(parts) >= 2 && len(parts) <= 3 && parts[0] == "agent-conversations" && ids.Validate(parts[1]) == nil {
		if len(parts) == 2 || parts[2] == "messages" {
			return access.Requirement{Package: catalog.PackageAgents}, method == http.MethodGet
		}
	}
	if len(parts) == 2 && parts[0] == "agent-runs" && ids.Validate(parts[1]) == nil {
		return access.Requirement{Package: catalog.PackageAgents}, method == http.MethodGet
	}
	if len(parts) == 1 && parts[0] == "schedules" {
		return access.Requirement{Package: catalog.PackageAgents, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) == 2 && parts[0] == "schedules" && ids.Validate(parts[1]) == nil {
		return access.Requirement{Package: catalog.PackageAgents, Mutation: method == http.MethodPut || method == http.MethodDelete}, method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	}
	if len(parts) == 3 && parts[0] == "schedules" && ids.Validate(parts[1]) == nil {
		switch parts[2] {
		case "pauses", "resumptions", "triggers":
			return access.Requirement{Package: catalog.PackageAgents, Mutation: true}, method == http.MethodPost
		}
	}
	if len(parts) >= 1 && parts[0] == "baseline-assessments" {
		if len(parts) == 1 {
			return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, method == http.MethodPost
		}
		if len(parts) == 2 && parts[1] == "current" {
			return access.Requirement{Package: catalog.PackageKnowledge}, method == http.MethodGet
		}
		if ids.Validate(parts[1]) != nil {
			return access.Requirement{}, false
		}
		if len(parts) == 2 {
			return access.Requirement{Package: catalog.PackageKnowledge}, method == http.MethodGet
		}
		if len(parts) == 3 && parts[2] == "source-grants" {
			return access.Requirement{Package: catalog.PackageIntegrations, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "answers", "inventory-starts", "inventories", "evidence-decisions", "dispositions", "plans", "plan-approvals", "work-materializations", "work-evidence-confirmations", "maintenance-work-materializations", "readiness", "reassessments":
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, method == http.MethodPost
			}
		}
		if len(parts) == 5 && parts[2] == "source-grants" && ids.Validate(parts[3]) == nil && parts[4] == "revocations" {
			return access.Requirement{Package: catalog.PackageIntegrations, Mutation: true}, method == http.MethodPost
		}
		return access.Requirement{}, false
	}
	if len(parts) >= 2 && parts[0] == "knowledge" {
		switch parts[1] {
		case "evidence":
			return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, len(parts) == 2 && method == http.MethodPost
		case "facts":
			return access.Requirement{Package: catalog.PackageKnowledge}, len(parts) == 2 && method == http.MethodGet
		case "retrieval":
			return access.Requirement{Package: catalog.PackageKnowledge}, len(parts) == 2 && method == http.MethodPost
		case "documents":
			if len(parts) == 2 {
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
			}
			if len(parts) == 3 && ids.Validate(parts[2]) == nil {
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: method == http.MethodDelete}, method == http.MethodGet || method == http.MethodDelete
			}
			if len(parts) == 4 && ids.Validate(parts[2]) == nil && parts[3] == "publications" {
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, method == http.MethodPost
			}
			if len(parts) == 7 && ids.Validate(parts[2]) == nil && parts[3] == "revisions" && ids.Validate(parts[4]) == nil && parts[5] == "chunks" && ids.Validate(parts[6]) == nil {
				return access.Requirement{Package: catalog.PackageKnowledge}, method == http.MethodGet
			}
		case "claims":
			if len(parts) == 2 {
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
			}
			if len(parts) == 3 && ids.Validate(parts[2]) == nil {
				return access.Requirement{Package: catalog.PackageKnowledge}, method == http.MethodGet
			}
			if len(parts) == 4 && ids.Validate(parts[2]) == nil && parts[3] == "decisions" {
				return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, method == http.MethodPost
			}
		}
		return access.Requirement{}, false
	}
	if len(parts) >= 2 && parts[0] == "finance" {
		return financeRouteRequirement(method, parts)
	}
	if len(parts) >= 2 && parts[0] == "marketing" {
		return marketingRouteRequirement(method, parts)
	}
	if len(parts) >= 2 && parts[0] == "integrations" {
		read := access.Requirement{Package: catalog.PackageIntegrations}
		mutation := access.Requirement{Package: catalog.PackageIntegrations, Mutation: true}
		switch parts[1] {
		case "connections":
			if len(parts) == 2 {
				return access.Requirement{Package: catalog.PackageIntegrations, Mutation: method == http.MethodPost},
					method == http.MethodGet || method == http.MethodPost
			}
			if len(parts) == 3 && ids.Validate(parts[2]) == nil {
				return access.Requirement{Package: catalog.PackageIntegrations, Mutation: method == http.MethodPut},
					method == http.MethodGet || method == http.MethodPut
			}
			if len(parts) == 4 && ids.Validate(parts[2]) == nil {
				if parts[3] == "health" {
					return read, method == http.MethodGet
				}
				for _, action := range []string{"credential-bindings", "credential-rotations", "disables", "enables", "revocations", "authorizations", "credential-revocations"} {
					if parts[3] == action {
						return mutation, method == http.MethodPost
					}
				}
			}
		case "authorizations":
			return read, len(parts) == 3 && ids.Validate(parts[2]) == nil && method == http.MethodGet
		case "google":
			return read, len(parts) == 3 && parts[2] == "authorization-callback" && method == http.MethodGet
		case "executions":
			if len(parts) == 2 {
				return access.Requirement{Package: catalog.PackageIntegrations, Mutation: method == http.MethodPost},
					method == http.MethodGet || method == http.MethodPost
			}
			if len(parts) == 3 && ids.Validate(parts[2]) == nil {
				return read, method == http.MethodGet
			}
			if len(parts) == 4 && ids.Validate(parts[2]) == nil && parts[3] == "resolution-requests" {
				return mutation, method == http.MethodPost
			}
			if len(parts) == 6 && ids.Validate(parts[2]) == nil && parts[3] == "resolutions" && ids.Validate(parts[4]) == nil && parts[5] == "confirmations" {
				return mutation, method == http.MethodPost
			}
		case "web-research":
			if len(parts) == 3 && parts[2] == "search" {
				return read, method == http.MethodPost
			}
			if len(parts) == 3 && parts[2] == "read" {
				return mutation, method == http.MethodPost
			}
		}
		return access.Requirement{}, false
	}
	if len(parts) == 3 && parts[0] == "agent-runs" && ids.Validate(parts[1]) == nil && parts[2] == "resolutions" {
		return access.Requirement{Package: catalog.PackageAgents, Mutation: true}, method == http.MethodPost
	}
	if len(parts) >= 2 && parts[0] == "attention" {
		packageCode := catalog.PackageWork
		switch parts[1] {
		case "information-requests", "work-reviews":
		case "approvals", "actions":
			packageCode = catalog.PackageAgents
		default:
			return access.Requirement{}, false
		}
		if len(parts) == 2 {
			if parts[1] == "actions" {
				return access.Requirement{Package: packageCode}, method == http.MethodGet
			}
			return access.Requirement{Package: packageCode, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
		}
		if len(parts) == 3 && ids.Validate(parts[2]) == nil {
			return access.Requirement{Package: packageCode}, method == http.MethodGet
		}
		if len(parts) == 4 && ids.Validate(parts[2]) == nil {
			action := parts[3]
			if parts[1] == "actions" {
				return access.Requirement{Package: packageCode, Mutation: true}, method == http.MethodPost && action == "resolution-requests"
			}
			allowedAction := (parts[1] == "information-requests" && (action == "answers" || action == "cancellations")) ||
				(parts[1] != "information-requests" && (action == "decisions" || action == "cancellations"))
			return access.Requirement{Package: packageCode, Mutation: true}, method == http.MethodPost && allowedAction
		}
		if len(parts) == 6 && parts[1] == "actions" && ids.Validate(parts[2]) == nil && parts[3] == "resolutions" && ids.Validate(parts[4]) == nil && parts[5] == "confirmations" {
			return access.Requirement{Package: packageCode, Mutation: true}, method == http.MethodPost
		}
	}
	return access.Requirement{}, false
}

func strongAuthenticatedAt(session sessions.Session) *time.Time {
	assurance := session.ReauthenticationMethod.Assurance()
	if session.ReauthenticatedAt.IsZero() || (assurance != sessions.AssuranceUserVerifiedCryptographic && assurance != sessions.AssuranceMultiFactor) {
		return nil
	}
	value := session.ReauthenticatedAt.UTC()
	return &value
}

func financeRouteRequirement(method string, parts []string) (access.Requirement, bool) {
	read := access.Requirement{Package: catalog.PackageFinance}
	mutation := access.Requirement{Package: catalog.PackageFinance, Mutation: true}
	switch parts[1] {
	case "ledgers":
		if len(parts) == 2 {
			return access.Requirement{Package: catalog.PackageFinance, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
		}
		if ids.Validate(parts[2]) != nil {
			return access.Requirement{}, false
		}
		if len(parts) == 3 {
			return access.Requirement{Package: catalog.PackageFinance, Mutation: method == http.MethodPut || method == http.MethodDelete}, method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
		}
		if len(parts) == 4 {
			switch parts[3] {
			case "period-closes":
				return mutation, method == http.MethodPost
			case "accounts", "entries", "reconciliations":
				return access.Requirement{Package: catalog.PackageFinance, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
			}
		}
	case "accounts":
		if len(parts) == 3 && ids.Validate(parts[2]) == nil {
			return access.Requirement{Package: catalog.PackageFinance, Mutation: method == http.MethodPut || method == http.MethodDelete}, method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
		}
	case "entries":
		if len(parts) >= 3 && ids.Validate(parts[2]) == nil {
			if len(parts) == 3 {
				return access.Requirement{Package: catalog.PackageFinance, Mutation: method == http.MethodPut}, method == http.MethodGet || method == http.MethodPut
			}
			if len(parts) == 4 && (parts[3] == "postings" || parts[3] == "reversals") {
				return mutation, method == http.MethodPost
			}
		}
	case "reconciliations":
		if len(parts) >= 3 && ids.Validate(parts[2]) == nil {
			if len(parts) == 3 {
				return read, method == http.MethodGet
			}
			if len(parts) == 4 && parts[3] == "confirmations" {
				return mutation, method == http.MethodPost
			}
		}
	}
	return access.Requirement{}, false
}

func marketingRouteRequirement(method string, parts []string) (access.Requirement, bool) {
	read := access.Requirement{Package: catalog.PackageMarketing}
	mutation := access.Requirement{Package: catalog.PackageMarketing, Mutation: true}
	switch parts[1] {
	case "campaigns":
		if len(parts) == 2 {
			return access.Requirement{Package: catalog.PackageMarketing, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
		}
		if ids.Validate(parts[2]) != nil {
			return access.Requirement{}, false
		}
		if len(parts) == 3 {
			return access.Requirement{Package: catalog.PackageMarketing, Mutation: method == http.MethodPut || method == http.MethodDelete}, method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
		}
		if len(parts) == 4 {
			switch parts[3] {
			case "asset-revisions", "releases":
				return access.Requirement{Package: catalog.PackageMarketing, Mutation: method == http.MethodPost}, method == http.MethodGet || method == http.MethodPost
			case "activations", "pauses", "completions":
				return mutation, method == http.MethodPost
			}
		}
	case "releases":
		if len(parts) >= 3 && ids.Validate(parts[2]) == nil {
			if len(parts) == 3 {
				return read, method == http.MethodGet
			}
			if len(parts) == 4 && (parts[3] == "submissions" || parts[3] == "approvals" || parts[3] == "cancellations") {
				return mutation, method == http.MethodPost
			}
		}
	}
	return access.Requirement{}, false
}

func additionalRouteRequirement(method, resource string) (access.Requirement, bool) {
	if !strings.EqualFold(method, http.MethodPost) {
		return access.Requirement{}, false
	}
	if resource == "integrations/web-research/read" {
		return access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}, true
	}
	parts := strings.Split(resource, "/")
	if len(parts) == 3 && parts[0] == "baseline-assessments" && ids.Validate(parts[1]) == nil {
		switch parts[2] {
		case "work-materializations", "maintenance-work-materializations":
			return access.Requirement{Package: catalog.PackageWork, Mutation: true}, true
		case "work-evidence-confirmations":
			return access.Requirement{Package: catalog.PackageWork}, true
		}
	}
	return access.Requirement{}, false
}

func additionalRouteRequirements(method, resource string, body *requestbody.Capture) ([]access.Requirement, error) {
	requirements := make([]access.Requirement, 0, 2)
	if requirement, required := additionalRouteRequirement(method, resource); required {
		requirements = append(requirements, requirement)
	}
	parts := strings.Split(resource, "/")
	if !strings.EqualFold(method, http.MethodPost) || len(parts) != 3 || parts[0] != "agent-boardrooms" || ids.Validate(parts[1]) != nil || parts[2] != "runs" {
		return requirements, nil
	}
	reader, err := body.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var request struct {
		Context *struct {
			WorkItemIDs           []string `json:"work_item_ids"`
			KnowledgeFactIDs      []string `json:"knowledge_fact_ids"`
			KnowledgeDocumentIDs  []string `json:"knowledge_document_ids"`
			BaselineAssessmentIDs []string `json:"baseline_assessment_ids"`
		} `json:"context"`
	}
	if err := json.NewDecoder(reader).Decode(&request); err != nil {
		return nil, err
	}
	if request.Context == nil {
		return requirements, nil
	}
	if len(request.Context.WorkItemIDs) != 0 {
		requirements = append(requirements, access.Requirement{Package: catalog.PackageWork})
	}
	if len(request.Context.KnowledgeFactIDs)+len(request.Context.KnowledgeDocumentIDs)+len(request.Context.BaselineAssessmentIDs) != 0 {
		requirements = append(requirements, access.Requirement{Package: catalog.PackageKnowledge})
	}
	return requirements, nil
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
