// Package mcpgateway authenticates public MCP requests, resolves the selected
// Account, and forwards the unchanged JSON-RPC body to its assigned cell under
// a one-use signed route proof.
package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/requestbody"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/mcpapi"
)

const (
	DefaultMaxRequestBody  = int64(1 << 20)
	DefaultMaxResponseBody = int64(4 << 20)
	cellMCPPath            = "/internal/v1/mcp"
	currentProtocolVersion = "2026-07-28"
	RequiredScope          = "spyglass:mcp"
)

type TokenRequirement struct {
	Audience string
	Scope    string
}

type TokenAuthenticator interface {
	Authenticate(context.Context, string, TokenRequirement) (Principal, error)
}

type Principal struct {
	Actor                 access.Actor
	StrongAuthenticatedAt *time.Time
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
	TrustedOrigins       []string
	ResourceURL          string
	ResourceMetadataURL  string
	AuthorizationServers []string
	MaxRequestBody       int64
	MaxResponseBody      int64
	Transport            http.RoundTripper
	AccountExports       AccountExportService
	ExportCapabilities   ExportCapabilityIssuer
	AppOrigin            string
}

type Server struct {
	authenticator        TokenAuthenticator
	authorizer           Authorizer
	directory            AccountDirectory
	signer               TokenSigner
	ids                  ids.Generator
	logger               *slog.Logger
	resource             string
	metadata             string
	authorizationServers []string
	origins              map[string]struct{}
	maxRequest           int64
	maxResponse          int64
	client               *http.Client
	exports              *accountExportTools
}

func New(authenticator TokenAuthenticator, authorizer Authorizer, directory AccountDirectory, signer TokenSigner, generator ids.Generator, logger *slog.Logger, config Config) (*Server, error) {
	if authenticator == nil || authorizer == nil || directory == nil || signer == nil || generator == nil || logger == nil {
		return nil, errors.New("MCP gateway dependencies are required")
	}
	metadata, err := url.Parse(config.ResourceMetadataURL)
	if err != nil || metadata.Scheme != "https" || metadata.Host == "" || metadata.Path != "/.well-known/oauth-protected-resource" || metadata.RawQuery != "" || metadata.Fragment != "" {
		return nil, errors.New("MCP gateway HTTPS protected-resource metadata URL is required")
	}
	resource, err := url.Parse(config.ResourceURL)
	if err != nil || resource.Scheme != "https" || resource.Host == "" || resource.Fragment != "" || resource.RawQuery != "" || resource.String() != strings.TrimSuffix(resource.String(), "/") || metadata.Host != resource.Host {
		return nil, errors.New("MCP gateway canonical HTTPS resource URL is required")
	}
	if len(config.AuthorizationServers) == 0 {
		return nil, errors.New("MCP gateway authorization server is required")
	}
	authorizationServers := make([]string, 0, len(config.AuthorizationServers))
	for _, raw := range config.AuthorizationServers {
		issuer, err := url.Parse(raw)
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.String() != strings.TrimSuffix(issuer.String(), "/") {
			return nil, errors.New("MCP gateway authorization servers must be canonical HTTPS issuer URLs")
		}
		authorizationServers = append(authorizationServers, issuer.String())
	}
	if config.MaxRequestBody == 0 {
		config.MaxRequestBody = DefaultMaxRequestBody
	}
	if config.MaxResponseBody == 0 {
		config.MaxResponseBody = DefaultMaxResponseBody
	}
	if config.MaxRequestBody <= 0 || config.MaxRequestBody > 16<<20 || config.MaxResponseBody <= 0 || config.MaxResponseBody > 32<<20 {
		return nil, errors.New("MCP gateway body limits are invalid")
	}
	origins := make(map[string]struct{}, len(config.TrustedOrigins))
	for _, raw := range config.TrustedOrigins {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("MCP gateway trusted origins must be exact HTTP origins")
		}
		origins[parsed.String()] = struct{}{}
	}
	transport := config.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("cell redirects are not allowed")
	}}
	exports, err := newAccountExportTools(config.AccountExports, config.ExportCapabilities, config.AppOrigin, logger)
	if err != nil {
		return nil, err
	}
	return &Server{authenticator: authenticator, authorizer: authorizer, directory: directory, signer: signer, ids: generator, logger: logger, resource: resource.String(), metadata: metadata.String(), authorizationServers: authorizationServers, origins: origins, maxRequest: config.MaxRequestBody, maxResponse: config.MaxResponseBody, client: client, exports: exports}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResourceMetadata)
	mux.HandleFunc("POST /mcp/v1/accounts/{accountID}", s.route)
	return s.securityHeaders(mux)
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type accountArguments struct {
	AccountID string `json:"account_id"`
}

type requestMetadata struct {
	Meta struct {
		ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
	} `json:"_meta"`
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		writeProblem(w, http.StatusForbidden, "origin_denied")
		return
	}
	if r.URL.RawQuery != "" || len(r.Header.Values("Cookie")) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_mcp_request")
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		s.challenge(w)
		writeProblem(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	principal, err := s.authenticator.Authenticate(r.Context(), token, TokenRequirement{Audience: s.resource, Scope: RequiredScope})
	if err != nil || !principal.Actor.Valid() {
		s.challenge(w)
		writeProblem(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	if !validMediaHeaders(r) {
		writeProblem(w, http.StatusBadRequest, "invalid_mcp_request")
		return
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if ids.Validate(string(accountID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_account")
		return
	}
	body, err := requestbody.Read(r.Body, s.maxRequest)
	if err != nil {
		if errors.Is(err, requestbody.ErrTooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable")
		}
		return
	}
	defer body.Close()
	requirement, method, tool, err := s.classify(r, body, accountID)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_mcp_request")
		return
	}
	accountContext, err := s.authorizer.Authorize(r.Context(), principal.Actor, accountID, requirement)
	if err != nil {
		s.writeAuthorizationError(w, err)
		return
	}
	if s.exports != nil && s.exports.handles(tool) {
		requestID := s.ids.New()
		s.exports.call(w, r, body, principal, accountContext, requestID)
		return
	}
	cellRoute, err := s.directory.Resolve(r.Context(), accountID, accountContext.CellID, accountContext.PlacementGeneration)
	if err != nil {
		s.logger.Error("resolve MCP Account route", "cell_id", accountContext.CellID, "placement_generation", accountContext.PlacementGeneration)
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable")
		return
	}
	requestID := s.ids.New()
	outbound, err := s.newCellRequest(r, cellRoute.Origin, body, principal.Actor, accountContext, requestID)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "routing_unavailable")
		return
	}
	response, err := s.client.Do(outbound)
	if err != nil && response == nil && r.Context().Err() == nil {
		requestID = s.ids.New()
		outbound, err = s.newCellRequest(r, cellRoute.Origin, body, principal.Actor, accountContext, requestID)
		if err == nil {
			response, err = s.client.Do(outbound)
		}
	}
	if err != nil {
		closeResponse(response)
		s.logger.Error("MCP cell request failed", "request_id", requestID, "cell_id", accountContext.CellID, "rpc_method", method, "tool", tool)
		writeProblem(w, http.StatusBadGateway, "cell_unavailable")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, s.maxResponse+1))
	if err != nil || int64(len(responseBody)) > s.maxResponse {
		writeProblem(w, http.StatusBadGateway, "invalid_cell_response")
		return
	}
	if method == "tools/list" && s.exports != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		responseBody, err = s.exports.mergeList(responseBody)
		if err != nil || int64(len(responseBody)) > s.maxResponse {
			s.logger.Error("merge MCP global tool list", "request_id", requestID, "cell_id", accountContext.CellID)
			writeProblem(w, http.StatusBadGateway, "invalid_cell_response")
			return
		}
	}
	copyResponseHeader(w.Header(), response.Header, "Content-Type")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(responseBody)
	s.logger.Info("MCP request routed", "request_id", requestID, "account_id", accountID, "rpc_method", method, "tool", tool, "mutation", requirement.Mutation, "status", response.StatusCode)
}

func (s *Server) classify(request *http.Request, body *requestbody.Capture, accountID ids.AccountID) (access.Requirement, string, string, error) {
	reader, err := body.Open()
	if err != nil {
		return access.Requirement{}, "", "", err
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var envelope rpcEnvelope
	if err := decoder.Decode(&envelope); err != nil || envelope.JSONRPC != "2.0" || strings.TrimSpace(envelope.Method) == "" {
		return access.Requirement{}, "", "", errors.New("invalid JSON-RPC envelope")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return access.Requirement{}, "", "", errors.New("multiple JSON values are not allowed")
	}
	var metadata requestMetadata
	_ = json.Unmarshal(envelope.Params, &metadata)
	protocolValues := request.Header.Values("MCP-Protocol-Version")
	methodValues := request.Header.Values("Mcp-Method")
	nameValues := request.Header.Values("Mcp-Name")
	modern := len(protocolValues) == 1 && protocolValues[0] == currentProtocolVersion
	if modern {
		if metadata.Meta.ProtocolVersion != currentProtocolVersion || len(methodValues) != 1 || methodValues[0] != envelope.Method {
			return access.Requirement{}, envelope.Method, "", errors.New("MCP metadata header mismatch")
		}
	} else if len(protocolValues) > 1 || len(methodValues) != 0 || len(nameValues) != 0 {
		return access.Requirement{}, envelope.Method, "", errors.New("unsupported or partial MCP metadata headers")
	}
	if envelope.Method != "tools/call" {
		if modern && len(nameValues) != 0 {
			return access.Requirement{}, envelope.Method, "", errors.New("unexpected MCP name header")
		}
		switch envelope.Method {
		case "initialize", "notifications/initialized", "ping", "tools/list":
			return access.Requirement{}, envelope.Method, "", nil
		default:
			return access.Requirement{}, envelope.Method, "", errors.New("unsupported MCP method")
		}
	}
	var call callParams
	if len(envelope.Params) == 0 || json.Unmarshal(envelope.Params, &call) != nil || call.Name == "" || len(call.Arguments) == 0 {
		return access.Requirement{}, envelope.Method, "", errors.New("invalid tool call")
	}
	requirement, ok := mcpapi.ToolRequirement(call.Name)
	if !ok && s.exports != nil {
		requirement, ok = s.exports.requirement(call.Name)
	}
	if !ok {
		return access.Requirement{}, envelope.Method, call.Name, errors.New("unknown tool")
	}
	if modern && (len(nameValues) != 1 || nameValues[0] != call.Name) {
		return access.Requirement{}, envelope.Method, call.Name, errors.New("MCP tool header mismatch")
	}
	var arguments accountArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil || arguments.AccountID != string(accountID) {
		return access.Requirement{}, envelope.Method, call.Name, errors.New("tool Account does not match route")
	}
	return requirement, envelope.Method, call.Name, nil
}

func (s *Server) newCellRequest(inbound *http.Request, origin url.URL, body *requestbody.Capture, actor access.Actor, account access.AccountContext, requestID string) (*http.Request, error) {
	origin.Path, origin.RawPath, origin.RawQuery = cellMCPPath, "", ""
	reader, err := body.Open()
	if err != nil {
		return nil, err
	}
	outbound, err := http.NewRequestWithContext(inbound.Context(), http.MethodPost, origin.String(), reader)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	outbound.ContentLength = body.Size()
	copyRequestHeader(outbound.Header, inbound.Header, "Accept", "Content-Type", "MCP-Protocol-Version", "Mcp-Method", "Mcp-Name")
	binding, err := routecontext.BindRequestDigest(outbound, body.SHA256())
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	authority := routecontext.Authority{RequestID: requestID, AccountID: account.AccountID, CellID: account.CellID, PlacementGeneration: account.PlacementGeneration, EntitlementVersion: account.EntitlementVersion, PackageAccess: packageClaim(account.PackageAccess)}
	if actor.UserID != "" {
		authority.ActorKind, authority.ActorID, authority.Role = "user", string(actor.UserID), string(account.Role)
	} else {
		authority.ActorKind, authority.ActorID = "workload", actor.WorkloadID
	}
	proof, err := s.signer.Issue(routecontext.Audience(account.CellID), authority, binding)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	outbound.Header.Set(routecontext.HeaderName, proof)
	outbound.Header.Set("X-Request-ID", requestID)
	return outbound, nil
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
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	_, ok := s.origins[values[0]]
	return ok
}

func validMediaHeaders(r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return false
	}
	acceptsJSON, acceptsSSE := false, false
	for _, raw := range r.Header.Values("Accept") {
		for _, part := range strings.Split(raw, ",") {
			mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				return false
			}
			acceptsJSON = acceptsJSON || mediaType == "application/json" || mediaType == "*/*"
			acceptsSSE = acceptsSSE || mediaType == "text/event-stream" || mediaType == "*/*"
		}
	}
	return acceptsJSON && acceptsSSE
}

func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	return token, token != "" && len(token) <= 16<<10 && token == strings.TrimSpace(token) && !strings.ContainsAny(token, " \t\r\n\x00")
}

func (s *Server) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.metadata+`", scope="`+RequiredScope+`"`)
}

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeProblem(w, http.StatusBadRequest, "invalid_metadata_request")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 s.resource,
		"authorization_servers":    s.authorizationServers,
		"scopes_supported":         []string{RequiredScope},
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) writeAuthorizationError(w http.ResponseWriter, err error) {
	var denied *access.DeniedError
	if errors.As(err, &denied) {
		status := http.StatusForbidden
		if denied.Code == access.DenialUnauthenticated {
			status = http.StatusUnauthorized
			s.challenge(w)
		}
		if denied.Code == access.DenialAccountUnavailable {
			status = http.StatusConflict
		}
		writeProblem(w, status, string(denied.Code))
		return
	}
	s.logger.Error("authorize MCP Account request", "error", err)
	writeProblem(w, http.StatusServiceUnavailable, "authorization_unavailable")
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code})
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

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
