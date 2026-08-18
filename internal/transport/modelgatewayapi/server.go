// Package modelgatewayapi exposes the private, workload-authenticated model
// gateway HTTP boundary.
package modelgatewayapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
)

type Gateway interface {
	Invoke(context.Context, modelgateway.Request) (modelgateway.Result, error)
}

type Server struct {
	gateway Gateway
	logger  *slog.Logger
	maxBody int64
}

func New(gateway Gateway, logger *slog.Logger, maxBody int64) (*Server, error) {
	if gateway == nil || logger == nil || maxBody < 1 || maxBody > modelgateway.MaximumRequestBytes {
		return nil, errors.New("model gateway transport configuration is invalid")
	}
	return &Server{gateway: gateway, logger: logger, maxBody: maxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/model-turns:invoke", s.invoke)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(w, http.StatusBadRequest, "model_request_invalid")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "model_request_too_large")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request modelgateway.Request
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "model_request_invalid")
		return
	}
	result, err := s.gateway.Invoke(r.Context(), request)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, modelgateway.ErrInvalidRequest):
		writeProblem(w, http.StatusBadRequest, "model_request_invalid")
	case errors.Is(err, modelgateway.ErrProviderUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "model_provider_unavailable")
	case errors.Is(err, modelgateway.ErrProviderFailed):
		writeProblem(w, http.StatusBadGateway, "model_provider_failed")
	case errors.Is(err, modelgateway.ErrInvalidProviderReply):
		writeProblem(w, http.StatusBadGateway, "model_provider_reply_invalid")
	default:
		s.logger.Error("Model gateway failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "model_gateway_unavailable")
	}
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
				s.logger.Error("Model gateway transport panic")
				writeProblem(w, http.StatusInternalServerError, "internal_error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
