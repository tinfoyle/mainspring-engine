package cellapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
)

type scheduleExecutionLoadRequest struct {
	Claim schedulingapp.ExecutionClaim `json:"claim"`
}

type scheduleExecutionLoadResponse struct {
	Snapshot schedulingapp.ExecutionSnapshot `json:"snapshot"`
}

type scheduleExecutionCommandRequest struct {
	Command schedulingapp.OccurrenceCommand `json:"command"`
}

type scheduleExecutionCommandResponse struct {
	Reconciled bool `json:"reconciled"`
}

func (s *Server) scheduleExecutionLoad(w http.ResponseWriter, r *http.Request) {
	if !s.acceptScheduleWorker(w, r) {
		return
	}
	var request scheduleExecutionLoadRequest
	if !s.decodeScheduleExecution(w, r, &request) {
		return
	}
	if !request.Claim.Valid() {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_execution", "the Schedule execution claim is invalid")
		return
	}
	snapshot, err := s.scheduleExecution.Load(r.Context(), request.Claim)
	if err != nil {
		s.writeScheduleExecutionError(w, err)
		return
	}
	if !snapshot.ValidFor(request.Claim) {
		writeProblem(w, http.StatusConflict, "schedule_execution_conflict", "the Schedule execution no longer matches durable state")
		return
	}
	writeJSON(w, http.StatusOK, scheduleExecutionLoadResponse{Snapshot: snapshot})
}

func (s *Server) scheduleExecutionDispatch(w http.ResponseWriter, r *http.Request) {
	s.scheduleExecutionCommand(w, r, false)
}

func (s *Server) scheduleExecutionSkip(w http.ResponseWriter, r *http.Request) {
	s.scheduleExecutionCommand(w, r, true)
}

func (s *Server) scheduleExecutionCommand(w http.ResponseWriter, r *http.Request, skip bool) {
	if !s.acceptScheduleWorker(w, r) {
		return
	}
	var request scheduleExecutionCommandRequest
	if !s.decodeScheduleExecution(w, r, &request) {
		return
	}
	if !request.Command.Valid() {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_execution", "the Schedule occurrence command is invalid")
		return
	}
	var (
		reconciled bool
		err        error
	)
	if skip {
		reconciled, err = s.scheduleExecution.Skip(r.Context(), request.Command)
	} else {
		reconciled, err = s.scheduleExecution.Dispatch(r.Context(), request.Command)
	}
	if err != nil {
		s.writeScheduleExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleExecutionCommandResponse{Reconciled: reconciled})
}

func (s *Server) acceptScheduleWorker(w http.ResponseWriter, r *http.Request) bool {
	if s.scheduleExecution == nil {
		writeProblem(w, http.StatusServiceUnavailable, "schedule_execution_unavailable", "Schedule execution is temporarily unavailable")
		return false
	}
	if identity, verified := workloadidentity.ClientIdentityFromContext(r.Context()); verified && identity != s.scheduleWorkerIdentity {
		writeProblem(w, http.StatusForbidden, "schedule_workload_scope_denied", "the workload identity cannot execute Schedules for this cell")
		return false
	}
	return true
}

func (s *Server) decodeScheduleExecution(w http.ResponseWriter, r *http.Request, value any) bool {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Schedule execution requires application/json")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_execution", "the Schedule execution request is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_execution", "the Schedule execution request is invalid")
		return false
	}
	return true
}

func (s *Server) writeScheduleExecutionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schedulingapp.ErrExecutionClaimInvalid), errors.Is(err, schedulingapp.ErrExecutionSnapshotInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_execution", "the Schedule execution request is invalid")
	case errors.Is(err, schedulingapp.ErrExecutionLeaseLost):
		writeProblem(w, http.StatusConflict, "schedule_execution_lease_lost", "the Schedule execution lease was lost")
	case errors.Is(err, schedulingapp.ErrExecutionConflict), errors.Is(err, schedulingapp.ErrExecutionAuthorizationStale):
		writeProblem(w, http.StatusConflict, "schedule_execution_conflict", "the Schedule execution no longer matches durable state")
	case errors.Is(err, schedulingapp.ErrExecutionCapacity):
		writeProblem(w, http.StatusTooManyRequests, "agent_run_capacity", "Agent Run capacity is temporarily unavailable")
	default:
		s.logger.Error("Schedule execution failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "schedule_execution_unavailable", "Schedule execution is temporarily unavailable")
	}
}
