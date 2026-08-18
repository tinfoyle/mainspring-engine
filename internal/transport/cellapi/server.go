// Package cellapi exposes account-owned use cases only after accepting a
// signed, request-bound route context from the global app router.
package cellapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	RouteContextHeader = "X-Spyglass-Route-Context"
	DefaultMaxBody     = int64(1 << 20)
)

type Acceptor interface {
	Accept(context.Context, string, routecontext.Binding) (routecontext.Claims, error)
}

type Server struct {
	acceptor Acceptor
	logger   *slog.Logger
	maxBody  int64
	work     WorkQueries
	commands WorkCommands
	agents   AgentService
	counters routeCounters
}

type RouteStats struct {
	Accepted                uint64 `json:"accepted"`
	MissingContext          uint64 `json:"missing_context"`
	ReplayDenied            uint64 `json:"replay_denied"`
	StalePlacementDenied    uint64 `json:"stale_placement_denied"`
	AccountUnavailable      uint64 `json:"account_unavailable"`
	VerificationDenied      uint64 `json:"verification_denied"`
	ReceiptStoreUnavailable uint64 `json:"receipt_store_unavailable"`
}

type routeCounters struct {
	accepted, missing, replay, stale, unavailable, invalid, receiptStore atomic.Uint64
}

type Option func(*Server)

func WithWorkQueries(queries WorkQueries) Option {
	return func(server *Server) { server.work = queries }
}

func WithWorkCommands(commands WorkCommands) Option {
	return func(server *Server) { server.commands = commands }
}

func WithAgents(service AgentService) Option {
	return func(server *Server) { server.agents = service }
}

func New(acceptor Acceptor, logger *slog.Logger, maxBody int64, options ...Option) (*Server, error) {
	if acceptor == nil || logger == nil || maxBody <= 0 || maxBody > 16<<20 {
		return nil, errors.New("cell API dependencies and bounded body size are required")
	}
	server := &Server{acceptor: acceptor, logger: logger, maxBody: maxBody}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/context", s.accountContext)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items", s.workList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items", s.workCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/summary", s.workSummary)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/{itemID}", s.workItem)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/{itemID}/children", s.workChildren)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items/{itemID}/transitions", s.workTransition)
	mux.HandleFunc("PATCH /api/v1/accounts/{accountID}/work-items/{itemID}/assignment", s.workAssign)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-boardrooms", s.agentBoardrooms)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms", s.agentBoardroomCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/personas", s.agentPersonas)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/personas", s.agentPersonaPublish)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/runs", s.agentRunStart)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-runs/{runID}", s.agentRun)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) Stats() RouteStats {
	return RouteStats{Accepted: s.counters.accepted.Load(), MissingContext: s.counters.missing.Load(), ReplayDenied: s.counters.replay.Load(), StalePlacementDenied: s.counters.stale.Load(), AccountUnavailable: s.counters.unavailable.Load(), VerificationDenied: s.counters.invalid.Load(), ReceiptStoreUnavailable: s.counters.receiptStore.Load()}
}

func (s *Server) accountContext(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.accept(w, r)
	if !ok {
		return
	}
	if ids.AccountID(r.PathValue("accountID")) != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": claims.Authority.AccountID, "actor_kind": claims.Authority.ActorKind,
		"role": claims.Authority.Role, "cell_id": claims.Authority.CellID,
		"placement_generation": claims.Authority.PlacementGeneration,
		"entitlement_version":  claims.Authority.EntitlementVersion,
		"package_access":       claims.Authority.PackageAccess,
	})
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (routecontext.Claims, bool) {
	token := strings.TrimSpace(r.Header.Get(RouteContextHeader))
	if token == "" {
		s.counters.missing.Add(1)
		writeProblem(w, http.StatusUnauthorized, "route_context_required", "trusted route context is required")
		return routecontext.Claims{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the cell limit")
		return routecontext.Claims{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	binding, err := routecontext.BindRequest(r, body)
	if err != nil {
		s.counters.invalid.Add(1)
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return routecontext.Claims{}, false
	}
	claims, err := s.acceptor.Accept(r.Context(), token, binding)
	if err != nil {
		switch {
		case errors.Is(err, routecontext.ErrReplay):
			s.counters.replay.Add(1)
			writeProblem(w, http.StatusConflict, "route_replay", "this routed request was already consumed")
		case errors.Is(err, routecontext.ErrPlacement):
			s.counters.stale.Add(1)
			writeProblem(w, http.StatusConflict, "stale_route", "Account placement changed; refresh routing")
		case errors.Is(err, routecontext.ErrUnavailable):
			s.counters.unavailable.Add(1)
			writeProblem(w, http.StatusServiceUnavailable, "account_unavailable", "the Account is not currently available in this cell")
		case errors.Is(err, routecontext.ErrReceiptStore):
			s.counters.receiptStore.Add(1)
			s.logger.Error("route receipt store unavailable", "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "route_boundary_unavailable", "the trusted route boundary is temporarily unavailable")
		default:
			s.counters.invalid.Add(1)
			writeProblem(w, http.StatusUnauthorized, "invalid_route_context", "trusted route context could not be verified")
		}
		return routecontext.Claims{}, false
	}
	s.counters.accepted.Add(1)
	*r = *r.WithContext(routecontext.WithProof(r.Context(), routecontext.Proof{Token: token, Binding: binding}))
	return claims, true
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("cell API panic", "value", value)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
