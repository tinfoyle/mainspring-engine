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
}

func New(acceptor Acceptor, logger *slog.Logger, maxBody int64) (*Server, error) {
	if acceptor == nil || logger == nil || maxBody <= 0 || maxBody > 16<<20 {
		return nil, errors.New("cell API dependencies and bounded body size are required")
	}
	return &Server{acceptor: acceptor, logger: logger, maxBody: maxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/context", s.accountContext)
	return s.recover(s.securityHeaders(mux))
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
		writeProblem(w, http.StatusUnauthorized, "route_context_required", "trusted route context is required")
		return routecontext.Claims{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the cell limit")
		return routecontext.Claims{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	binding, err := routecontext.Bind(r.Method, routecontext.Target(r), body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return routecontext.Claims{}, false
	}
	claims, err := s.acceptor.Accept(r.Context(), token, binding)
	if err != nil {
		switch {
		case errors.Is(err, routecontext.ErrReplay):
			writeProblem(w, http.StatusConflict, "route_replay", "this routed request was already consumed")
		case errors.Is(err, routecontext.ErrPlacement):
			writeProblem(w, http.StatusConflict, "stale_route", "Account placement changed; refresh routing")
		case errors.Is(err, routecontext.ErrUnavailable):
			writeProblem(w, http.StatusServiceUnavailable, "account_unavailable", "the Account is not currently available in this cell")
		default:
			writeProblem(w, http.StatusUnauthorized, "invalid_route_context", "trusted route context could not be verified")
		}
		return routecontext.Claims{}, false
	}
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
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
