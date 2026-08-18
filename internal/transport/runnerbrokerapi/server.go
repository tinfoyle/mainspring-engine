// Package runnerbrokerapi exposes the narrow Pod-to-broker exchange protocol.
package runnerbrokerapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
)

const (
	DefaultMaxBody     = int64(runnerbroker.MaximumEnvelopeBytes + 64<<10)
	maximumBearerToken = 16 << 10
	requestPathPrefix  = "/internal/v1/runner/invocations/"
)

type Exchange interface {
	Fetch(context.Context, string, string) (runnerbroker.Request, error)
	Submit(context.Context, string, string, runnerbroker.Result) (bool, error)
}

type Server struct {
	exchange Exchange
	logger   *slog.Logger
	maxBody  int64
}

func New(exchange Exchange, logger *slog.Logger, maxBody int64) (*Server, error) {
	if exchange == nil || logger == nil || maxBody <= 0 || maxBody > 2<<20 {
		return nil, errors.New("runner broker transport dependencies and bounded body size are required")
	}
	return &Server{exchange: exchange, logger: logger, maxBody: maxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+requestPathPrefix+"{invocation_id}/request", s.fetch)
	mux.HandleFunc("PUT "+requestPathPrefix+"{invocation_id}/result", s.submit)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) fetch(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || !emptyBody(r.Body) {
		writeProblem(w, http.StatusBadRequest, "runner_request_invalid", "the runner request is invalid")
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied", "runner identity could not be verified")
		return
	}
	request, err := s.exchange.Fetch(r.Context(), token, r.PathValue("invocation_id"))
	if err != nil {
		s.writeExchangeError(w, "fetch", err)
		return
	}
	writeJSON(w, http.StatusOK, request)
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeProblem(w, http.StatusBadRequest, "runner_result_invalid", "the runner result is invalid")
		return
	}
	if mediaType(r.Header.Get("Content-Type")) != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "runner_result_json_required", "runner results require application/json")
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied", "runner identity could not be verified")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var result runnerbroker.Result
	if err := decoder.Decode(&result); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "runner_result_too_large", "the runner result exceeds the broker limit")
			return
		}
		writeProblem(w, http.StatusBadRequest, "runner_result_invalid", "the runner result is invalid")
		return
	}
	if !decodeEOF(decoder) {
		writeProblem(w, http.StatusBadRequest, "runner_result_invalid", "the runner result is invalid")
		return
	}
	created, err := s.exchange.Submit(r.Context(), token, r.PathValue("invocation_id"), result)
	if err != nil {
		s.writeExchangeError(w, "submit", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"status": "accepted", "newly_created": created})
}

func (s *Server) writeExchangeError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, runnerbroker.ErrIdentityDenied):
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied", "runner identity could not be verified")
	case errors.Is(err, runnerbroker.ErrInvalidExchange):
		writeProblem(w, http.StatusBadRequest, "runner_exchange_invalid", "the runner exchange is invalid")
	case errors.Is(err, runnerbroker.ErrExchangeNotReady):
		writeProblem(w, http.StatusConflict, "runner_exchange_not_ready", "the runner exchange is not ready")
	case errors.Is(err, runnerbroker.ErrExchangeCanceled):
		writeProblem(w, http.StatusGone, "runner_exchange_canceled", "the runner exchange was canceled")
	case errors.Is(err, runnerbroker.ErrExchangeExpired):
		writeProblem(w, http.StatusGone, "runner_exchange_expired", "the runner exchange expired")
	case errors.Is(err, runnerbroker.ErrExchangeConflict):
		writeProblem(w, http.StatusConflict, "runner_exchange_conflict", "the runner exchange conflicts with durable state")
	default:
		s.logger.Error("Runner broker exchange failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "runner_broker_unavailable", "the runner broker is temporarily unavailable")
	}
}

func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	return token, token != "" && len(token) <= maximumBearerToken && strings.TrimSpace(token) == token && !strings.ContainsAny(token, " \t\r\n\x00")
}

func emptyBody(body io.ReadCloser) bool {
	defer body.Close()
	value, err := io.ReadAll(io.LimitReader(body, 1))
	return err == nil && len(value) == 0
}

func decodeEOF(decoder *json.Decoder) bool {
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func mediaType(value string) string { return strings.TrimSpace(strings.Split(value, ";")[0]) }

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				s.logger.Error("Runner broker transport panic")
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the runner exchange could not be completed")
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
