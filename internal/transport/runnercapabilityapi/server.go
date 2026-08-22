// Package runnercapabilityapi exposes the narrow runner-to-capability gateway.
package runnercapabilityapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

const DefaultMaxBody = int64(runnercapability.MaximumInputBytes + 64<<10)

type Gateway interface {
	Invoke(context.Context, string, string, runnercapability.Call) (runnercapability.Result, error)
}

type Server struct {
	gateway Gateway
	logger  *slog.Logger
	maxBody int64
}

func New(gateway Gateway, logger *slog.Logger, maxBody int64) (*Server, error) {
	if gateway == nil || logger == nil || maxBody <= 0 || maxBody > 1<<20 {
		return nil, runnercapability.ErrInvalidCall
	}
	return &Server{gateway: gateway, logger: logger, maxBody: maxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/runner/invocations/{invocation_id}/capabilities:invoke", s.invoke)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeProblem(w, http.StatusBadRequest, "capability_call_invalid", "the capability call is invalid")
		return
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "capability_json_required", "capability calls require application/json")
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied", "runner identity could not be verified")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var call runnercapability.Call
	if err := decoder.Decode(&call); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "capability_call_too_large", "the capability call exceeds the gateway limit")
			return
		}
		writeProblem(w, http.StatusBadRequest, "capability_call_invalid", "the capability call is invalid")
		return
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "capability_call_invalid", "the capability call is invalid")
		return
	}
	result, err := s.gateway.Invoke(r.Context(), token, r.PathValue("invocation_id"), call)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runnerbroker.ErrIdentityDenied):
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied", "runner identity could not be verified")
	case errors.Is(err, runnerbroker.ErrExchangeCanceled):
		writeProblem(w, http.StatusGone, "runner_exchange_canceled", "the runner exchange was canceled")
	case errors.Is(err, runnerbroker.ErrExchangeExpired):
		writeProblem(w, http.StatusGone, "runner_exchange_expired", "the runner exchange expired")
	case errors.Is(err, runnerbroker.ErrExchangeNotReady):
		writeProblem(w, http.StatusConflict, "runner_exchange_not_ready", "the runner exchange is not ready")
	case errors.Is(err, runnerbroker.ErrCapabilityDenied):
		writeProblem(w, http.StatusForbidden, "capability_denied", "the runner does not hold this capability")
	case errors.Is(err, runnercapability.ErrInvalidCall):
		writeProblem(w, http.StatusBadRequest, "capability_call_invalid", "the capability call is invalid")
	case errors.Is(err, runnercapability.ErrUnavailable):
		writeProblem(w, http.StatusNotFound, "capability_unavailable", "the capability is unavailable")
	case errors.Is(err, runnercapability.ErrActionDenied):
		writeProblem(w, http.StatusForbidden, "capability_action_denied", "the consequential action was denied")
	case errors.Is(err, runnercapability.ErrActionUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "capability_action_unavailable", "consequential action state is temporarily unavailable")
	case errors.Is(err, runnercapability.ErrExecutionFailed):
		writeExecutionProblem(w, err)
	case errors.Is(err, runnercapability.ErrAuditUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "capability_audit_unavailable", "capability audit is temporarily unavailable")
	default:
		s.logger.Error("Runner capability gateway failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "capability_gateway_unavailable", "the capability gateway is temporarily unavailable")
	}
}

func writeExecutionProblem(w http.ResponseWriter, err error) {
	reason, ok := runnercapability.ExecutionFailureCode(err)
	if !ok {
		reason = "execution_failed"
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, http.StatusBadGateway, map[string]any{"type": "https://infiniteocean.net/problems/capability_execution_failed", "title": http.StatusText(http.StatusBadGateway), "status": http.StatusBadGateway, "code": "capability_execution_failed", "detail": "the capability could not be completed", "reason_code": reason})
}

func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	return token, token != "" && len(token) <= 16<<10 && strings.TrimSpace(token) == token && !strings.ContainsAny(token, " \t\r\n\x00")
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				s.logger.Error("Runner capability transport panic")
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the capability call could not be completed")
			}
		}()
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
