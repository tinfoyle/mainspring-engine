package cellapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type WorkCommands interface {
	Create(context.Context, workapp.CreateCommand) (workdomain.Item, error)
	Transition(context.Context, workapp.TransitionCommand) (workdomain.Item, error)
	Assign(context.Context, workapp.AssignCommand) (workdomain.Item, error)
	AttachProvenance(context.Context, workapp.AttachProvenanceCommand) (workdomain.Item, error)
	LinkConversation(context.Context, workapp.LinkConversationCommand) (workdomain.Item, error)
}

type createWorkRequest struct {
	ParentID    ids.WorkItemID        `json:"parent_id,omitempty"`
	Kind        workdomain.Kind       `json:"kind"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	Priority    workdomain.Priority   `json:"priority"`
	Assignment  workAssignmentRequest `json:"assignment"`
	DueAt       *time.Time            `json:"due_at,omitempty"`
	Reason      string                `json:"reason,omitempty"`
}

type transitionWorkRequest struct {
	To     workdomain.State `json:"to"`
	Reason string           `json:"reason,omitempty"`
}

type assignWorkRequest struct {
	Assignment workAssignmentRequest `json:"assignment"`
	Reason     string                `json:"reason,omitempty"`
}

type attachWorkProvenanceRequest struct {
	Kind        workdomain.ProvenanceLinkKind `json:"kind"`
	ReferenceID string                        `json:"reference_id"`
	Reason      string                        `json:"reason,omitempty"`
}

type linkWorkConversationRequest struct {
	ConversationID string `json:"conversation_id"`
	Reason         string `json:"reason,omitempty"`
}

type workAssignmentRequest struct {
	Responsibility workdomain.Responsibility `json:"responsibility"`
	UserID         ids.UserID                `json:"user_id,omitempty"`
	PersonaID      string                    `json:"persona_id,omitempty"`
	ExternalRef    string                    `json:"external_ref,omitempty"`
}

func (s *Server) workCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.workCommandRequest(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_work_command", "Work creation does not accept query parameters")
		return
	}
	var request createWorkRequest
	if !decodeWorkCommand(w, r, &request) {
		return
	}
	assignment, valid := interactiveAssignment(request.Assignment, actor)
	if !valid {
		writeProblem(w, http.StatusUnprocessableEntity, "invalid_work_assignment", "interactive Work may be shared, assigned to you, assigned to an active Persona, or assigned to an external reference")
		return
	}
	item, err := s.commands.Create(routecontext.WithClaims(r.Context(), claims), workapp.CreateCommand{
		Actor: actor, AccountID: accountID, RequestID: operationID, ParentID: request.ParentID,
		Kind: request.Kind, Title: request.Title, Description: request.Description, Priority: request.Priority,
		Assignment: assignment, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: commandActor(actor)},
		DueAt: request.DueAt, Reason: request.Reason, CorrelationID: operationID,
	})
	if err != nil {
		s.writeWorkCommandError(w, "create", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/work-items/%s", accountID, item.ID))
	writeWorkItem(w, http.StatusCreated, item)
}

func (s *Server) workTransition(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.workCommandRequest(w, r)
	if !ok {
		return
	}
	itemID, version, ok := commandTarget(w, r)
	if !ok {
		return
	}
	var request transitionWorkRequest
	if !decodeWorkCommand(w, r, &request) {
		return
	}
	item, err := s.commands.Transition(routecontext.WithClaims(r.Context(), claims), workapp.TransitionCommand{Actor: actor, AccountID: accountID, WorkItemID: itemID, To: request.To, ExpectedVersion: version, RequestID: operationID, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeWorkCommandError(w, "transition", err)
		return
	}
	writeWorkItem(w, http.StatusOK, item)
}

func (s *Server) workAssign(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.workCommandRequest(w, r)
	if !ok {
		return
	}
	itemID, version, ok := commandTarget(w, r)
	if !ok {
		return
	}
	var request assignWorkRequest
	if !decodeWorkCommand(w, r, &request) {
		return
	}
	assignment, valid := interactiveAssignment(request.Assignment, actor)
	if !valid {
		writeProblem(w, http.StatusUnprocessableEntity, "invalid_work_assignment", "interactive Work may be shared, assigned to you, assigned to an active Persona, or assigned to an external reference")
		return
	}
	item, err := s.commands.Assign(routecontext.WithClaims(r.Context(), claims), workapp.AssignCommand{Actor: actor, AccountID: accountID, WorkItemID: itemID, Assignment: assignment, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeWorkCommandError(w, "assign", err)
		return
	}
	writeWorkItem(w, http.StatusOK, item)
}

func (s *Server) workAttachProvenance(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.workCommandRequest(w, r)
	if !ok {
		return
	}
	itemID, version, ok := commandTarget(w, r)
	if !ok {
		return
	}
	var request attachWorkProvenanceRequest
	if !decodeWorkCommand(w, r, &request) {
		return
	}
	item, err := s.commands.AttachProvenance(routecontext.WithClaims(r.Context(), claims), workapp.AttachProvenanceCommand{
		Actor: actor, AccountID: accountID, WorkItemID: itemID, Kind: request.Kind, ReferenceID: request.ReferenceID,
		ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID,
	})
	if err != nil {
		s.writeWorkCommandError(w, "attach provenance", err)
		return
	}
	writeWorkItem(w, http.StatusOK, item)
}

func (s *Server) workLinkConversation(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.workCommandRequest(w, r)
	if !ok {
		return
	}
	itemID, version, ok := commandTarget(w, r)
	if !ok {
		return
	}
	var request linkWorkConversationRequest
	if !decodeWorkCommand(w, r, &request) {
		return
	}
	item, err := s.commands.LinkConversation(routecontext.WithClaims(r.Context(), claims), workapp.LinkConversationCommand{
		Actor: actor, AccountID: accountID, WorkItemID: itemID, ConversationID: request.ConversationID,
		ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID,
	})
	if err != nil {
		s.writeWorkCommandError(w, "link conversation", err)
		return
	}
	writeWorkItem(w, http.StatusOK, item)
}

func (s *Server) workCommandRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.commands == nil {
		writeProblem(w, http.StatusServiceUnavailable, "work_unavailable", "Work commands are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func commandTarget(w http.ResponseWriter, r *http.Request) (ids.WorkItemID, uint64, bool) {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_work_command", "Work commands do not accept query parameters")
		return "", 0, false
	}
	itemID := ids.WorkItemID(r.PathValue("itemID"))
	if ids.Validate(string(itemID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_work_item", "Work item ID is invalid")
		return "", 0, false
	}
	values := r.Header.Values("If-Match")
	if len(values) != 1 {
		writeProblem(w, http.StatusPreconditionRequired, "work_version_required", "If-Match with the current Work version is required")
		return "", 0, false
	}
	raw := strings.TrimSpace(values[0])
	if !strings.HasPrefix(raw, `W/"`) || !strings.HasSuffix(raw, `"`) {
		writeProblem(w, http.StatusBadRequest, "invalid_work_version", "If-Match must use the Work weak ETag")
		return "", 0, false
	}
	version, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(raw, `W/"`), `"`), 10, 64)
	if err != nil || version == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_work_version", "If-Match must use the Work weak ETag")
		return "", 0, false
	}
	return itemID, version, true
}

func decodeWorkCommand(w http.ResponseWriter, r *http.Request, destination any) bool {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Work commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_work_command", "the Work command body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_work_command", "the Work command body is invalid")
		return false
	}
	return true
}

func interactiveAssignment(request workAssignmentRequest, actor access.Actor) (workdomain.Assignment, bool) {
	assignment := workdomain.Assignment{Responsibility: request.Responsibility, UserID: request.UserID, PersonaID: request.PersonaID, ExternalRef: request.ExternalRef}
	switch assignment.Responsibility {
	case workdomain.ResponsibilityShared:
		return assignment, assignment.UserID == "" && assignment.PersonaID == "" && strings.TrimSpace(assignment.ExternalRef) == ""
	case workdomain.ResponsibilityUser:
		if assignment.UserID == "" {
			assignment.UserID = actor.UserID
		}
		return assignment, actor.UserID != "" && assignment.UserID == actor.UserID && assignment.PersonaID == "" && strings.TrimSpace(assignment.ExternalRef) == ""
	case workdomain.ResponsibilityPersona:
		return assignment, assignment.UserID == "" && ids.Validate(assignment.PersonaID) == nil && strings.TrimSpace(assignment.ExternalRef) == ""
	case workdomain.ResponsibilityExternal:
		return assignment, assignment.UserID == "" && assignment.PersonaID == "" && len(strings.TrimSpace(assignment.ExternalRef)) >= 2
	default:
		return workdomain.Assignment{}, false
	}
}

func commandActor(actor access.Actor) workdomain.Actor {
	if actor.UserID != "" {
		return workdomain.Actor{Kind: workdomain.ActorUser, ID: string(actor.UserID)}
	}
	return workdomain.Actor{Kind: workdomain.ActorWorkload, ID: actor.WorkloadID}
}

func writeWorkItem(w http.ResponseWriter, status int, item workdomain.Item) {
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, item.Version))
	writeJSON(w, status, workItemView(item))
}

func (s *Server) writeWorkCommandError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, workapp.ErrInvalidCommand), errors.Is(err, workdomain.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_work_command", "the Work command is invalid")
	case errors.Is(err, workapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "work_item_not_found", "the Work item was not found")
	case errors.Is(err, workapp.ErrCapacityCompensation):
		s.logger.Error("compensate Work capacity", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "capacity_compensation_pending", "the Work command failed and capacity recovery is pending")
	case errors.Is(err, workapp.ErrCapacityOutcomeUnknown):
		w.Header().Set("Retry-After", "1")
		writeProblem(w, http.StatusServiceUnavailable, "work_outcome_unknown", "retry the exact Work command with the same Idempotency-Key")
	case errors.Is(err, workapp.ErrConflict):
		writeProblem(w, http.StatusPreconditionFailed, "work_version_conflict", "the Work item changed; reload it before retrying")
	case errors.Is(err, workapp.ErrConstraint), errors.Is(err, workdomain.ErrTransition), errors.Is(err, workdomain.ErrReasonRequired), errors.Is(err, workdomain.ErrLinkExists):
		writeProblem(w, http.StatusUnprocessableEntity, "work_command_rejected", "the Work command violates the item lifecycle or relationship rules")
	case errors.Is(err, workdomain.ErrRole):
		writeProblem(w, http.StatusForbidden, string(access.DenialRole), "your Account role cannot perform this Work command")
	case errors.Is(err, usageadmission.ErrReservationConflict), errors.Is(err, usageadmission.ErrReservationClosed):
		writeProblem(w, http.StatusConflict, "work_operation_conflict", "the operation key cannot be used for this Work command")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Work command")
	default:
		s.logger.Error("command Work", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "work_unavailable", "the Work command could not be completed")
	}
}
