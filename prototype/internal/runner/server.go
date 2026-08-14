package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
)

type Server struct {
	logger *slog.Logger
	docker *DockerClient
	limits ContainerLimits
	slots  chan struct{}
	mu     sync.Mutex
	active map[string]string
}

func NewServer(logger *slog.Logger, docker *DockerClient, limits ContainerLimits, concurrency int) (*Server, error) {
	if docker == nil || limits.Image == "" || limits.Network == "" {
		return nil, errors.New("runner Docker client, image, and network are required")
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Server{logger: logger, docker: docker, limits: limits, slots: make(chan struct{}, concurrency), active: make(map[string]string)}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "runner-controller"})
	})
	router.Post("/v1/invocations", s.invoke)
	router.Delete("/v1/invocations/{invocationID}", s.cancel)
	return httpx.Chain(router, httpx.RequestID, httpx.Recover(s.logger), httpx.AccessLog(s.logger))
}

// CleanupOrphans removes runner containers that survived a controller restart
// or exceeded their hard invocation timeout. Active containers owned by this
// controller instance are never removed by the periodic reconciliation pass.
func (s *Server) CleanupOrphans(ctx context.Context, startup bool) error {
	containers, err := s.docker.ListRunners(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-s.limits.Timeout - 5*time.Minute).Unix()
	s.mu.Lock()
	active := make(map[string]bool, len(s.active))
	for _, containerID := range s.active {
		active[containerID] = true
	}
	s.mu.Unlock()
	for _, container := range containers {
		if active[container.ID] || (!startup && container.Created > cutoff) {
			continue
		}
		if err := s.docker.Remove(ctx, container.ID); err != nil {
			s.logger.Warn("remove abandoned runner", "container_id", container.ID, "state", container.State, "error", err)
			continue
		}
		s.logger.Warn("removed abandoned runner", "container_id", container.ID, "state", container.State)
	}
	return nil
}

func (s *Server) RunCleanup(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.CleanupOrphans(cleanup, false)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("reconcile abandoned runners", "error", err)
			}
		}
	}
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_invocation", "The runner request was invalid.")
		return
	}
	var request agent.RemoteInvocation
	if err := json.Unmarshal(body, &request); err != nil || request.Invocation.ID.String() == "00000000-0000-0000-0000-000000000000" {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_invocation", "The runner request was invalid.")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-r.Context().Done():
		return
	}
	invocationID := request.Invocation.ID.String()
	containerID, err := s.docker.CreateRunner(r.Context(), "mainspring-agent-"+invocationID, s.limits)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	s.mu.Lock()
	s.active[invocationID] = containerID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.active, invocationID)
		s.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.docker.Remove(cleanup, containerID); err != nil {
			s.logger.Warn("remove runner container", "container_id", containerID, "error", err)
		}
	}()
	if err := s.docker.PutFile(r.Context(), containerID, "/work", "invocation.json", body); err != nil {
		s.writeFailure(w, err)
		return
	}
	if err := s.docker.PutFile(r.Context(), containerID, "/work", "result.json", nil); err != nil {
		s.writeFailure(w, err)
		return
	}
	if err := s.docker.Start(r.Context(), containerID); err != nil {
		s.writeFailure(w, err)
		return
	}
	exitCode, err := s.docker.Wait(r.Context(), containerID)
	if err != nil {
		s.writeFailure(w, err)
		return
	}
	result, readErr := s.docker.ReadFile(r.Context(), containerID, "/work/result.json")
	if readErr != nil {
		s.writeFailure(w, fmt.Errorf("runner exited %d without a result: %w; logs: %s", exitCode, readErr, s.docker.Logs(r.Context(), containerID)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "invocationID")
	s.mu.Lock()
	containerID := s.active[id]
	s.mu.Unlock()
	if containerID == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := s.docker.Stop(r.Context(), containerID); err != nil {
		s.writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeFailure(w http.ResponseWriter, err error) {
	s.logger.Error("runner invocation", "error", err)
	httpx.WriteJSON(w, http.StatusServiceUnavailable, agent.RemoteResult{Error: err.Error(), Category: agent.FailureUnavailable, Retryable: true})
}
