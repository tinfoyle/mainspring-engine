// Package toolrouter resolves one verified runner capability into a fresh,
// least-authority cell request. It owns no cell data and accepts no browser or
// Kubernetes credentials.
package toolrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

const (
	ContextHeader          = "X-Spyglass-Tool-Context"
	DefaultMaxRequestBody  = int64(256 << 10)
	DefaultMaxResponseBody = int64(256 << 10)
	WorkSummaryCapability  = runnercapability.WorkSummaryCapability
)

type Acceptor interface {
	Accept(context.Context, string, routecontext.Binding) (toolcontext.Claims, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type AccountDirectory interface {
	Resolve(context.Context, ids.AccountID, ids.CellID, uint64) (accountdirectory.Route, error)
}

type TokenSigner interface {
	Issue(string, routecontext.Authority, routecontext.Binding) (string, error)
}

type Config struct {
	MaxRequestBody, MaxResponseBody int64
	Transport                       http.RoundTripper
	AllowHTTPCells                  bool
}

type Server struct {
	acceptor   Acceptor
	authorizer Authorizer
	directory  AccountDirectory
	signer     TokenSigner
	ids        ids.Generator
	logger     *slog.Logger
	client     *http.Client
	config     Config
}

func New(acceptor Acceptor, authorizer Authorizer, directory AccountDirectory, signer TokenSigner, generator ids.Generator, logger *slog.Logger, config Config) (*Server, error) {
	if acceptor == nil || authorizer == nil || directory == nil || signer == nil || generator == nil || logger == nil {
		return nil, errors.New("tool router dependencies are required")
	}
	if config.MaxRequestBody == 0 {
		config.MaxRequestBody = DefaultMaxRequestBody
	}
	if config.MaxResponseBody == 0 {
		config.MaxResponseBody = DefaultMaxResponseBody
	}
	if config.MaxRequestBody <= 0 || config.MaxRequestBody > 1<<20 || config.MaxResponseBody <= 0 || config.MaxResponseBody > 1<<20 {
		return nil, errors.New("tool router body limits are invalid")
	}
	transport := config.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("cell redirects are not allowed") }}
	return &Server{acceptor: acceptor, authorizer: authorizer, directory: directory, signer: signer, ids: generator, logger: logger, client: client, config: config}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/tools:invoke", s.invoke)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(w, http.StatusBadRequest, "tool_dispatch_invalid")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.config.MaxRequestBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "tool_dispatch_too_large")
		return
	}
	if !emptyJSONObject(body) {
		writeProblem(w, http.StatusBadRequest, "tool_input_invalid")
		return
	}
	binding, err := routecontext.BindRequest(r, body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "tool_dispatch_invalid")
		return
	}
	tokenValues := r.Header.Values(ContextHeader)
	if len(tokenValues) != 1 || strings.TrimSpace(tokenValues[0]) != tokenValues[0] || tokenValues[0] == "" {
		writeProblem(w, http.StatusUnauthorized, "tool_context_required")
		return
	}
	claims, err := s.acceptor.Accept(r.Context(), tokenValues[0], binding)
	if err != nil {
		s.writeAcceptError(w, err)
		return
	}
	if claims.Authority.Capability != WorkSummaryCapability {
		writeProblem(w, http.StatusNotFound, "capability_unavailable")
		return
	}
	actor := access.Actor{WorkloadID: "runner-invocation:" + claims.Authority.InvocationID}
	accountContext, err := s.authorizer.Authorize(r.Context(), actor, claims.Authority.AccountID, access.Requirement{Package: catalog.PackageWork})
	if err != nil {
		writeProblem(w, http.StatusForbidden, "tool_authorization_denied")
		return
	}
	cellRoute, err := s.directory.Resolve(r.Context(), claims.Authority.AccountID, accountContext.CellID, accountContext.PlacementGeneration)
	if err != nil {
		s.logger.Error("resolve tool Account route", "account_id", claims.Authority.AccountID, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "tool_routing_unavailable")
		return
	}
	if !s.config.AllowHTTPCells && cellRoute.Origin.Scheme != "https" {
		writeProblem(w, http.StatusServiceUnavailable, "tool_routing_unavailable")
		return
	}
	target := "/api/v1/accounts/" + string(claims.Authority.AccountID) + "/work-items/summary"
	cellBinding, _ := routecontext.Bind(http.MethodGet, target, nil)
	requestID := s.ids.New()
	authority := routecontext.Authority{
		RequestID: requestID, OperationID: claims.Authority.OperationID, AccountID: accountContext.AccountID,
		ActorKind: "workload", ActorID: actor.WorkloadID, CellID: accountContext.CellID,
		PlacementGeneration: accountContext.PlacementGeneration, EntitlementVersion: accountContext.EntitlementVersion,
		PackageAccess: packageClaim(accountContext.PackageAccess),
	}
	routeToken, err := s.signer.Issue(routecontext.Audience(accountContext.CellID), authority, cellBinding)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "tool_routing_unavailable")
		return
	}
	origin := cellRoute.Origin
	origin.Path, origin.RawPath, origin.RawQuery = target, "", ""
	outbound, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin.String(), nil)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "tool_routing_unavailable")
		return
	}
	outbound.Header.Set(cellapi.RouteContextHeader, routeToken)
	outbound.Header.Set("X-Request-ID", requestID)
	response, err := s.client.Do(outbound)
	if err != nil {
		s.logger.Error("cell tool request failed", "account_id", claims.Authority.AccountID, "request_id", requestID)
		writeProblem(w, http.StatusBadGateway, "tool_cell_unavailable")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, s.config.MaxResponseBody+1))
	if err != nil || int64(len(responseBody)) > s.config.MaxResponseBody {
		writeProblem(w, http.StatusBadGateway, "tool_response_invalid")
		return
	}
	if response.StatusCode != http.StatusOK {
		writeProblem(w, http.StatusBadGateway, "tool_execution_failed")
		return
	}
	if !json.Valid(responseBody) {
		writeProblem(w, http.StatusBadGateway, "tool_response_invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(responseBody)
}

func emptyJSONObject(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value map[string]json.RawMessage
	return decoder.Decode(&value) == nil && value != nil && len(value) == 0 && errors.Is(decoder.Decode(&struct{}{}), io.EOF)
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

func (s *Server) writeAcceptError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, toolcontext.ErrReplay):
		writeProblem(w, http.StatusConflict, "tool_context_replay")
	case errors.Is(err, toolcontext.ErrReceiptStore):
		writeProblem(w, http.StatusServiceUnavailable, "tool_boundary_unavailable")
	default:
		writeProblem(w, http.StatusUnauthorized, "tool_context_invalid")
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
			if recover() != nil {
				s.logger.Error("Tool router panic")
				writeProblem(w, http.StatusInternalServerError, "internal_error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code})
}
