package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/routeaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const routedCredential = "spyglass-routed-mcp"

// RoutedAuthority accepts only a principal already authenticated by a cell's
// one-use route-proof middleware. It never accepts a customer Bearer token.
type RoutedAuthority struct {
	authorizer *routeaccess.Authorizer
}

func NewRoutedAuthority() *RoutedAuthority {
	return &RoutedAuthority{authorizer: routeaccess.NewAuthorizer()}
}

func (a *RoutedAuthority) Authenticate(ctx context.Context, token string) (access.Actor, error) {
	claims, ok := routecontext.FromContext(ctx)
	if !ok || token != routedCredential {
		return access.Actor{}, errors.New("routed MCP authority is required")
	}
	switch claims.Authority.ActorKind {
	case "user":
		actor := access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}
		if !actor.Valid() || ids.Validate(string(actor.UserID)) != nil {
			return access.Actor{}, errors.New("invalid routed MCP user")
		}
		return actor, nil
	case "workload":
		actor := access.Actor{WorkloadID: claims.Authority.ActorID}
		if !actor.Valid() {
			return access.Actor{}, errors.New("invalid routed MCP workload")
		}
		return actor, nil
	default:
		return access.Actor{}, errors.New("invalid routed MCP actor")
	}
}

func (a *RoutedAuthority) Authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (routecontext.Claims, error) {
	if _, err := a.authorizer.Authorize(ctx, actor, accountID, requirement); err != nil {
		return routecontext.Claims{}, err
	}
	claims, ok := routecontext.FromContext(ctx)
	if !ok {
		return routecontext.Claims{}, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	return claims, nil
}

// RoutedHandler consumes and binds a signed route proof before MCP parses the
// JSON-RPC request. The proof is one use, Account/package scoped, and bound to
// the exact body and private cell target.
func RoutedHandler(acceptor *routecontext.Acceptor, next http.Handler, maxBody int64) (http.Handler, error) {
	if acceptor == nil || next == nil || maxBody <= 0 || maxBody > 16<<20 {
		return nil, errors.New("routed MCP handler configuration is invalid")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/mcp" || r.URL.RawQuery != "" {
			writeRoutedProblem(w, http.StatusNotFound, "route_not_found")
			return
		}
		proof := strings.TrimSpace(r.Header.Get(routecontext.HeaderName))
		if proof == "" {
			writeRoutedProblem(w, http.StatusUnauthorized, "route_context_required")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeRoutedProblem(w, http.StatusRequestEntityTooLarge, "request_too_large")
			return
		}
		binding, err := routecontext.BindRequest(r, body)
		if err != nil {
			writeRoutedProblem(w, http.StatusBadRequest, "invalid_request")
			return
		}
		claims, err := acceptor.Accept(r.Context(), proof, binding)
		if err != nil {
			switch {
			case errors.Is(err, routecontext.ErrReplay):
				writeRoutedProblem(w, http.StatusConflict, "route_replay")
			case errors.Is(err, routecontext.ErrPlacement):
				writeRoutedProblem(w, http.StatusConflict, "stale_route")
			case errors.Is(err, routecontext.ErrUnavailable):
				writeRoutedProblem(w, http.StatusServiceUnavailable, "account_unavailable")
			case errors.Is(err, routecontext.ErrReceiptStore):
				writeRoutedProblem(w, http.StatusServiceUnavailable, "route_boundary_unavailable")
			default:
				writeRoutedProblem(w, http.StatusUnauthorized, "invalid_route_context")
			}
			return
		}
		clone := r.Clone(routecontext.WithClaims(r.Context(), claims))
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		clone.Header = r.Header.Clone()
		clone.Header.Del(routecontext.HeaderName)
		clone.Header.Del("Cookie")
		clone.Header.Set("Authorization", "Bearer "+routedCredential)
		next.ServeHTTP(w, clone)
	}), nil
}

func writeRoutedProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code})
}

var _ Authority = (*RoutedAuthority)(nil)
