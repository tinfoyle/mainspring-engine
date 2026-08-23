// Package mcpapi exposes Account-bound Spyglass use cases through the official
// Model Context Protocol transport without reimplementing their authorization
// or domain rules.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const DefaultMaxBody = int64(1 << 20)

type Authority interface {
	Authenticate(context.Context, string) (access.Actor, error)
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (routecontext.Claims, error)
}

type Config struct {
	Version             string
	MaxBody             int64
	TrustedOrigins      []string
	ResourceMetadataURL string
}

type Server struct {
	authority        Authority
	attention        AttentionService
	actions          ActionRecoveryService
	knowledge        KnowledgeService
	documents        KnowledgeDocumentService
	baseline         BaselineService
	finance          FinanceService
	marketing        MarketingService
	logger           *slog.Logger
	version          string
	maxBody          int64
	origins          map[string]struct{}
	resourceMetadata string
	schemaCache      *mcp.SchemaCache
}

type Option func(*Server) error

func WithActionRecovery(service ActionRecoveryService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP action recovery service is required")
		}
		server.actions = service
		return nil
	}
}

func WithKnowledge(service KnowledgeService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP Knowledge service is required")
		}
		server.knowledge = service
		return nil
	}
}

func WithKnowledgeDocuments(service KnowledgeDocumentService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP Knowledge document service is required")
		}
		server.documents = service
		return nil
	}
}

func WithBaseline(service BaselineService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP Baseline service is required")
		}
		server.baseline = service
		return nil
	}
}

func WithFinance(service FinanceService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP Finance service is required")
		}
		server.finance = service
		return nil
	}
}

func WithMarketing(service MarketingService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("MCP Marketing service is required")
		}
		server.marketing = service
		return nil
	}
}

type principalContextKey struct{}

func New(authority Authority, attention AttentionService, logger *slog.Logger, config Config, options ...Option) (*Server, error) {
	if authority == nil || attention == nil || logger == nil {
		return nil, errors.New("MCP authority, Attention service, and logger are required")
	}
	if strings.TrimSpace(config.Version) == "" {
		return nil, errors.New("MCP server version is required")
	}
	if config.MaxBody == 0 {
		config.MaxBody = DefaultMaxBody
	}
	if config.MaxBody <= 0 || config.MaxBody > 16<<20 {
		return nil, errors.New("MCP request body limit is invalid")
	}
	metadata, err := url.Parse(config.ResourceMetadataURL)
	if err != nil || metadata.Scheme != "https" || metadata.Host == "" || metadata.Fragment != "" {
		return nil, errors.New("MCP HTTPS protected-resource metadata URL is required")
	}
	origins := make(map[string]struct{}, len(config.TrustedOrigins))
	for _, raw := range config.TrustedOrigins {
		origin, err := url.Parse(raw)
		if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
			return nil, errors.New("MCP trusted origins must be exact HTTP origins")
		}
		origins[origin.String()] = struct{}{}
	}
	server := &Server{
		authority: authority, attention: attention, logger: logger, version: strings.TrimSpace(config.Version),
		maxBody: config.MaxBody, origins: origins, resourceMetadata: metadata.String(), schemaCache: mcp.NewSchemaCache(),
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("MCP option is required")
		}
		if err := option(server); err != nil {
			return nil, err
		}
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	transport := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		actor, ok := request.Context().Value(principalContextKey{}).(access.Actor)
		if !ok || !actor.Valid() {
			return nil
		}
		return s.protocolServer(actor)
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, Logger: s.logger, MaxRequestBodyBytes: s.maxBody,
		PropagateRequestCancellation: true,
	})
	return s.securityHeaders(s.authenticate(transport))
}

func (s *Server) protocolServer(actor access.Actor) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "infinite-ocean-spyglass", Version: s.version}, &mcp.ServerOptions{
		Instructions: "Use only the Account-bound Spyglass tools exposed for the authenticated principal. Read summaries before sensitive detail, preserve operation IDs and expected versions across retries, never treat Finance drafts as posted records or payment execution, and never treat Marketing drafts or activation as external delivery.",
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}, SchemaCache: s.schemaCache,
	})
	s.registerInformation(server, actor)
	s.registerReviews(server, actor)
	s.registerApprovals(server, actor)
	if s.actions != nil {
		s.registerActionRecovery(server, actor)
	}
	if s.knowledge != nil {
		s.registerKnowledge(server, actor)
	}
	if s.baseline != nil {
		s.registerBaseline(server, actor)
	}
	if s.finance != nil {
		s.registerFinance(server, actor)
	}
	if s.marketing != nil {
		s.registerMarketing(server, actor)
	}
	return server
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validOrigin(r) {
			writeHTTPError(w, http.StatusForbidden, "origin_denied")
			return
		}
		if len(r.Header.Values("Cookie")) != 0 {
			writeHTTPError(w, http.StatusBadRequest, "cookie_authentication_not_allowed")
			return
		}
		token, ok := bearerToken(r)
		if !ok {
			s.challenge(w)
			writeHTTPError(w, http.StatusUnauthorized, "authentication_required")
			return
		}
		actor, err := s.authority.Authenticate(r.Context(), token)
		if err != nil || !actor.Valid() {
			s.challenge(w)
			writeHTTPError(w, http.StatusUnauthorized, "authentication_required")
			return
		}
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, actor)))
	})
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

func (s *Server) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.resourceMetadata+`"`)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	return token, token != "" && len(token) <= 16<<10 && token == strings.TrimSpace(token) && !strings.ContainsAny(token, " \t\r\n\x00")
}

func writeHTTPError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code})
}

func (s *Server) toolContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (context.Context, error) {
	if ids.Validate(string(accountID)) != nil {
		return nil, safeError("invalid_account")
	}
	claims, err := s.authority.Authorize(ctx, actor, accountID, requirement)
	if err != nil {
		return nil, attentionError(err)
	}
	if claims.Authority.AccountID != accountID {
		return nil, safeError("account_binding_mismatch")
	}
	return routecontext.WithClaims(ctx, claims), nil
}
