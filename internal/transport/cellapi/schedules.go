package cellapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	schedulingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type scheduleDefinitionRequest struct {
	Name            string                            `json:"name"`
	Timezone        string                            `json:"timezone"`
	Recurrence      schedulingdomain.Recurrence       `json:"recurrence"`
	MissedRunPolicy schedulingdomain.MissedRunPolicy  `json:"missed_run_policy"`
	Template        schedulingdomain.AgentRunTemplate `json:"template"`
	Reason          string                            `json:"reason,omitempty"`
}

type reviseScheduleRequest struct {
	ExpectedVersion *uint64 `json:"expected_version"`
	scheduleDefinitionRequest
}

type transitionScheduleRequest struct {
	ExpectedVersion *uint64 `json:"expected_version"`
	Reason          string  `json:"reason,omitempty"`
}

func (s *Server) scheduleList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.scheduleRequest(w, r, false)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "cursor", "limit") {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_query", "the Schedule query is invalid")
		return
	}
	limit, err := parseLimit(r, schedulingapp.MaximumPageSize)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_query", "the Schedule query is invalid")
		return
	}
	query := schedulingapp.ListQuery{Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "schedule")
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_schedule_query", "the Schedule cursor is invalid")
			return
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.ScheduleID(cursor.ID)
	}
	page, err := s.scheduling.List(routecontext.WithClaims(r.Context(), claims), schedulingapp.ListCommand{Actor: actor, AccountID: accountID, Query: query})
	if err != nil {
		s.writeSchedulingError(w, "list", err)
		return
	}
	response := map[string]any{"items": page.Items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("schedule", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) scheduleCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.scheduleRequest(w, r, true)
	if !ok {
		return
	}
	var request scheduleDefinitionRequest
	if len(r.URL.Query()) != 0 || !decodeScheduleJSON(w, r, &request) {
		return
	}
	value, created, err := s.scheduling.Create(routecontext.WithClaims(r.Context(), claims), schedulingapp.CreateCommand{
		Actor: actor, AccountID: accountID, RequestID: operationID, Name: request.Name, Timezone: request.Timezone,
		Recurrence: request.Recurrence, MissedRunPolicy: request.MissedRunPolicy, Template: request.Template, Reason: request.Reason,
	})
	if err != nil {
		s.writeSchedulingError(w, "create", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/schedules/%s", accountID, value.ID))
	writeJSON(w, status, value)
}

func (s *Server) scheduleGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.scheduleRequest(w, r, false)
	if !ok {
		return
	}
	scheduleID, ok := scheduleTarget(w, r)
	if !ok {
		return
	}
	value, err := s.scheduling.Get(routecontext.WithClaims(r.Context(), claims), schedulingapp.GetQuery{Actor: actor, AccountID: accountID, ScheduleID: scheduleID})
	if err != nil {
		s.writeSchedulingError(w, "get", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) scheduleRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.scheduleRequest(w, r, true)
	if !ok {
		return
	}
	scheduleID, ok := scheduleTarget(w, r)
	if !ok {
		return
	}
	var request reviseScheduleRequest
	if !decodeScheduleJSON(w, r, &request) {
		return
	}
	if request.ExpectedVersion == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "expected_version is required")
		return
	}
	value, err := s.scheduling.Revise(routecontext.WithClaims(r.Context(), claims), schedulingapp.ReviseCommand{
		Actor: actor, AccountID: accountID, ScheduleID: scheduleID, RequestID: operationID, ExpectedVersion: *request.ExpectedVersion,
		Name: request.Name, Timezone: request.Timezone, Recurrence: request.Recurrence, MissedRunPolicy: request.MissedRunPolicy,
		Template: request.Template, Reason: request.Reason,
	})
	if err != nil {
		s.writeSchedulingError(w, "revise", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) schedulePause(w http.ResponseWriter, r *http.Request) {
	s.scheduleTransition(w, r, "pause")
}

func (s *Server) scheduleResume(w http.ResponseWriter, r *http.Request) {
	s.scheduleTransition(w, r, "resume")
}

func (s *Server) scheduleDelete(w http.ResponseWriter, r *http.Request) {
	s.scheduleTransition(w, r, "delete")
}

func (s *Server) scheduleTrigger(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.scheduleRequest(w, r, true)
	if !ok {
		return
	}
	scheduleID, ok := scheduleTarget(w, r)
	if !ok {
		return
	}
	var request transitionScheduleRequest
	if !decodeScheduleJSON(w, r, &request) {
		return
	}
	if request.ExpectedVersion == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "expected_version is required")
		return
	}
	value, created, err := s.scheduling.TriggerNow(routecontext.WithClaims(r.Context(), claims), schedulingapp.TransitionCommand{
		Actor: actor, AccountID: accountID, ScheduleID: scheduleID, RequestID: operationID, ExpectedVersion: *request.ExpectedVersion, Reason: request.Reason,
	})
	if err != nil {
		s.writeSchedulingError(w, "trigger", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, value)
}

func (s *Server) scheduleTransition(w http.ResponseWriter, r *http.Request, operation string) {
	claims, actor, accountID, operationID, ok := s.scheduleRequest(w, r, true)
	if !ok {
		return
	}
	scheduleID, ok := scheduleTarget(w, r)
	if !ok {
		return
	}
	var request transitionScheduleRequest
	if !decodeScheduleJSON(w, r, &request) {
		return
	}
	if request.ExpectedVersion == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "expected_version is required")
		return
	}
	command := schedulingapp.TransitionCommand{Actor: actor, AccountID: accountID, ScheduleID: scheduleID, RequestID: operationID, ExpectedVersion: *request.ExpectedVersion, Reason: request.Reason}
	var value schedulingdomain.Schedule
	var err error
	switch operation {
	case "pause":
		value, err = s.scheduling.Pause(routecontext.WithClaims(r.Context(), claims), command)
	case "resume":
		value, err = s.scheduling.Resume(routecontext.WithClaims(r.Context(), claims), command)
	case "delete":
		value, err = s.scheduling.Delete(routecontext.WithClaims(r.Context(), claims), command)
	}
	if err != nil {
		s.writeSchedulingError(w, operation, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) scheduleRequest(w http.ResponseWriter, r *http.Request, command bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.scheduling == nil {
		writeProblem(w, http.StatusServiceUnavailable, "schedules_unavailable", "Schedules are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	if !command {
		return claims, actor, accountID, "", true
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func scheduleTarget(w http.ResponseWriter, r *http.Request) (ids.ScheduleID, bool) {
	value := ids.ScheduleID(r.PathValue("scheduleID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(value)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "the Schedule target is invalid")
		return "", false
	}
	return value, true
}

func decodeScheduleJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Schedule commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "the Schedule command body is invalid")
		return false
	}
	return true
}

func (s *Server) writeSchedulingError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, schedulingapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_schedule_command", "the Schedule command is invalid")
	case errors.Is(err, schedulingapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "schedule_not_found", "the Schedule was not found")
	case errors.Is(err, schedulingapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "schedule_operation_conflict", "the operation conflicts with durable Schedule state")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Schedule operation")
	default:
		s.logger.Error("Schedule operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "schedules_unavailable", "the Schedule operation could not be completed")
	}
}

var _ SchedulingService = (*schedulingapp.Service)(nil)

func (s *Server) scheduleHistory(w http.ResponseWriter, r *http.Request) {
	claims, actor, account, _, ok := s.scheduleRequest(w, r, false)
	if !ok {
		return
	}
	id, ok := scheduleTarget(w, r)
	if !ok {
		return
	}
	service, ok := s.scheduling.(interface {
		History(context.Context, schedulingapp.GetQuery) (schedulingapp.HistoryPage, error)
	})
	if !ok {
		writeProblem(w, 503, "schedules_unavailable", "Schedule history is unavailable")
		return
	}
	page, err := service.History(routecontext.WithClaims(r.Context(), claims), schedulingapp.GetQuery{Actor: actor, AccountID: account, ScheduleID: id})
	if err != nil {
		s.writeSchedulingError(w, "history", err)
		return
	}
	writeJSON(w, 200, page)
}
