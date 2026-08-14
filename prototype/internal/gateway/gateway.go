package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/control"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
)

type Resolver interface {
	ResolveRoute(context.Context, string) (control.Tenant, error)
}

type cacheEntry struct {
	tenant    control.Tenant
	expiresAt time.Time
}

type Server struct {
	logger     *slog.Logger
	resolver   Resolver
	baseDomain string
	cacheTTL   time.Duration
	mu         sync.RWMutex
	cache      map[string]cacheEntry
}

func NewServer(logger *slog.Logger, resolver Resolver, baseDomain string, cacheTTL time.Duration) *Server {
	return &Server{
		logger:     logger,
		resolver:   resolver,
		baseDomain: strings.Trim(strings.ToLower(baseDomain), "."),
		cacheTTL:   cacheTTL,
		cache:      make(map[string]cacheEntry),
	}
}

func (s *Server) Handler() http.Handler {
	return httpx.Chain(http.HandlerFunc(s.serve),
		httpx.RequestID,
		httpx.SecurityHeaders,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
	)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gateway"})
		return
	}

	slug, err := s.slugFromHost(r.Host)
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "tenant_not_found", "This boardroom address does not exist.")
		return
	}

	tenant, err := s.resolve(r.Context(), slug)
	if errors.Is(err, control.ErrTenantNotFound) {
		httpx.WriteProblem(w, http.StatusNotFound, "tenant_not_found", "This boardroom address does not exist.")
		return
	}
	if err != nil {
		s.logger.Error("resolve tenant", "slug", slug, "error", err)
		httpx.WriteProblem(w, http.StatusBadGateway, "route_unavailable", "The boardroom route could not be resolved.")
		return
	}
	if tenant.Status != control.TenantReady || tenant.InternalURL == "" {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "tenant_not_ready", "This boardroom is not ready yet.")
		return
	}

	target, err := url.Parse(tenant.InternalURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		s.logger.Error("invalid registered tenant URL", "tenant_id", tenant.ID.String())
		httpx.WriteProblem(w, http.StatusBadGateway, "invalid_route", "The boardroom route is invalid.")
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			originalHost := request.In.Host
			request.SetURL(target)
			request.SetXForwarded()
			request.Out.Host = originalHost
			request.Out.Header.Set("X-Mainspring-Tenant-ID", tenant.ID.String())
			request.Out.Header.Set("X-Mainspring-Tenant-Slug", tenant.Slug)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
			s.logger.Error("tenant proxy failed", "tenant_id", tenant.ID.String(), "error", proxyErr)
			httpx.WriteProblem(w, http.StatusBadGateway, "tenant_unavailable", "This boardroom is temporarily unavailable.")
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) slugFromHost(value string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(value))
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", errors.New("host is outside the configured base domain")
	}
	slug := strings.TrimSuffix(host, suffix)
	if strings.Contains(slug, ".") || slug == "" {
		return "", errors.New("host must contain exactly one tenant label")
	}
	return control.NormalizeSlug(slug)
}

func (s *Server) resolve(ctx context.Context, slug string) (control.Tenant, error) {
	now := time.Now()
	s.mu.RLock()
	entry, ok := s.cache[slug]
	s.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.tenant, nil
	}

	tenant, err := s.resolver.ResolveRoute(ctx, slug)
	if err != nil {
		return control.Tenant{}, err
	}
	s.mu.Lock()
	s.cache[slug] = cacheEntry{tenant: tenant, expiresAt: now.Add(s.cacheTTL)}
	s.mu.Unlock()
	return tenant, nil
}

func (s *Server) String() string {
	return fmt.Sprintf("gateway(*.%s)", s.baseDomain)
}
