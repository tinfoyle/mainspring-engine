// Package dockerlauncherapi exposes the bounded stage-only launcher boundary.
// The controller receives launch/inspect/cancel authority; the broker receives
// only runner identity verification authority.
package dockerlauncherapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const DefaultMaxBody = int64(32 << 10)

type Launcher interface {
	runnercontrol.Launcher
	runnerbroker.IdentityVerifier
	Ready(context.Context) error
}

type Config struct {
	ControllerToken string
	BrokerToken     string
	MaxBody         int64
}

type Server struct {
	launcher                             Launcher
	controllerTokenHash, brokerTokenHash [sha256.Size]byte
	logger                               *slog.Logger
	maxBody                              int64
}

func New(launcher Launcher, config Config, logger *slog.Logger) (*Server, error) {
	if launcher == nil || logger == nil || !validServiceToken(config.ControllerToken) || !validServiceToken(config.BrokerToken) || subtle.ConstantTimeCompare([]byte(config.ControllerToken), []byte(config.BrokerToken)) == 1 {
		return nil, errors.New("Docker launcher API dependencies and distinct service tokens are required")
	}
	if config.MaxBody == 0 {
		config.MaxBody = DefaultMaxBody
	}
	if config.MaxBody < 1024 || config.MaxBody > 64<<10 {
		return nil, errors.New("Docker launcher API body limit is invalid")
	}
	return &Server{launcher: launcher, controllerTokenHash: sha256.Sum256([]byte(config.ControllerToken)), brokerTokenHash: sha256.Sum256([]byte(config.BrokerToken)), logger: logger, maxBody: config.MaxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("POST /internal/v1/runner-launches", s.ensure)
	mux.HandleFunc("POST /internal/v1/runner-launches/{invocation_id}/cancel", s.cancel)
	mux.HandleFunc("POST /internal/v1/runner-launches/{invocation_id}/inspect", s.inspect)
	mux.HandleFunc("POST /internal/v1/runner-launches/{invocation_id}/verify", s.verify)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.launcher.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) ensure(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r, s.controllerTokenHash) {
		writeProblem(w, http.StatusUnauthorized, "launcher_authentication_denied")
		return
	}
	invocation, ok := s.invocation(w, r, "")
	if !ok {
		return
	}
	jobName, err := s.launcher.Ensure(r.Context(), invocation)
	if errors.Is(err, runnercontrol.ErrLaunchUncertain) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"job_name": jobName, "code": "launch_uncertain"})
		return
	}
	if err != nil {
		s.logger.Error("Docker runner ensure failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "launcher_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"job_name": jobName})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r, s.controllerTokenHash) {
		writeProblem(w, http.StatusUnauthorized, "launcher_authentication_denied")
		return
	}
	invocation, ok := s.invocation(w, r, r.PathValue("invocation_id"))
	if !ok {
		return
	}
	if err := s.launcher.Cancel(r.Context(), invocation); err != nil {
		s.logger.Error("Docker runner cancel failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "launcher_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r, s.controllerTokenHash) {
		writeProblem(w, http.StatusUnauthorized, "launcher_authentication_denied")
		return
	}
	invocation, ok := s.invocation(w, r, r.PathValue("invocation_id"))
	if !ok {
		return
	}
	status, err := s.launcher.Inspect(r.Context(), invocation)
	if err != nil {
		s.logger.Error("Docker runner inspect failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "launcher_failed")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r, s.brokerTokenHash) {
		writeProblem(w, http.StatusUnauthorized, "launcher_authentication_denied")
		return
	}
	invocationID := r.PathValue("invocation_id")
	if ids.Validate(invocationID) != nil || mediaType(r.Header.Get("Content-Type")) != "application/json" {
		writeProblem(w, http.StatusBadRequest, "launcher_request_invalid")
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !s.decode(w, r, &request) {
		return
	}
	identity, err := s.launcher.Verify(r.Context(), request.Token, invocationID)
	if errors.Is(err, runnerbroker.ErrIdentityDenied) {
		writeProblem(w, http.StatusUnauthorized, "runner_identity_denied")
		return
	}
	if err != nil {
		s.logger.Error("Docker runner identity verification failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "launcher_failed")
		return
	}
	writeJSON(w, http.StatusOK, identity)
}

type invocationWire struct {
	ID           string `json:"invocation_id"`
	AccountID    string `json:"account_id"`
	Profile      string `json:"profile"`
	State        string `json:"state"`
	AttemptCount int    `json:"attempt_count"`
	JobName      string `json:"job_name,omitempty"`
}

func (s *Server) invocation(w http.ResponseWriter, r *http.Request, pathID string) (runnercontrol.Invocation, bool) {
	if mediaType(r.Header.Get("Content-Type")) != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "launcher_json_required")
		return runnercontrol.Invocation{}, false
	}
	var request invocationWire
	if !s.decode(w, r, &request) || ids.Validate(request.ID) != nil || ids.Validate(request.AccountID) != nil || (pathID != "" && pathID != request.ID) {
		writeProblem(w, http.StatusBadRequest, "launcher_request_invalid")
		return runnercontrol.Invocation{}, false
	}
	return runnercontrol.Invocation{ID: request.ID, AccountID: ids.AccountID(request.AccountID), Profile: request.Profile, State: request.State, AttemptCount: request.AttemptCount, JobName: request.JobName}, true
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "launcher_request_invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "launcher_request_invalid")
		return false
	}
	return true
}

func (s *Server) authorized(r *http.Request, expected [sha256.Size]byte) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return false
	}
	presented := strings.TrimPrefix(values[0], "Bearer ")
	digest := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(digest[:], expected[:]) == 1
}

func validServiceToken(token string) bool {
	return len(token) >= 32 && len(token) <= 4096 && strings.TrimSpace(token) == token && !strings.ContainsAny(token, " \t\r\n\x00")
}

func mediaType(value string) string { return strings.TrimSpace(strings.Split(value, ";")[0]) }

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
				s.logger.Error("Docker launcher API panic")
				writeProblem(w, http.StatusInternalServerError, "internal_error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"status": status, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
