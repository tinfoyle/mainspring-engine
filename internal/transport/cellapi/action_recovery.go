package cellapi

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type resolutionRequestBody struct {
	Outcome actionrecovery.State `json:"outcome"`
	Reason  string               `json:"reason"`
}

func (s *Server) actionRecoveryList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.actionRecoveryRequestContext(w, r, false)
	if !ok {
		return
	}
	query, err := parseActionRecoveryQuery(r)
	if err != nil {
		s.writeActionRecoveryError(w, "list", err)
		return
	}
	page, err := s.actions.List(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeActionRecoveryError(w, "list", err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, actionSummaryResponse(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("action", page.NextCursor.UpdatedAt, page.NextCursor.OperationID)
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *Server) actionRecoveryGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.actionRecoveryRequestContext(w, r, true)
	if !ok {
		return
	}
	operationID := r.PathValue("operationID")
	if ids.Validate(operationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "the action operation is invalid")
		return
	}
	detail, err := s.actions.Get(routecontext.WithClaims(r.Context(), claims), actor, accountID, operationID)
	if err != nil {
		s.writeActionRecoveryError(w, "get", err)
		return
	}
	writeJSON(w, http.StatusOK, actionDetailResponse(detail))
}
func (s *Server) actionRecoveryRequest(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, resolutionID, ok := s.actionRecoveryCommandContext(w, r)
	if !ok {
		return
	}
	operationID := r.PathValue("operationID")
	if ids.Validate(operationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "the action operation is invalid")
		return
	}
	var body resolutionRequestBody
	if !decodeAttentionJSON(w, r, &body) {
		return
	}
	detail, err := s.actions.Request(routecontext.WithClaims(r.Context(), claims), actionrecovery.RequestCommand{Actor: actor, AccountID: accountID, OperationID: operationID, ResolutionID: resolutionID, Outcome: body.Outcome, Reason: body.Reason})
	if err != nil {
		s.writeActionRecoveryError(w, "request resolution", err)
		return
	}
	writeJSON(w, http.StatusOK, actionDetailResponse(detail))
}
func (s *Server) actionRecoveryConfirm(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, _, ok := s.actionRecoveryCommandContext(w, r)
	if !ok {
		return
	}
	operationID, resolutionID := r.PathValue("operationID"), r.PathValue("resolutionID")
	if ids.Validate(operationID) != nil || ids.Validate(resolutionID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "the action resolution target is invalid")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "resolution confirmation has no request body")
		return
	}
	detail, err := s.actions.Confirm(routecontext.WithClaims(r.Context(), claims), actionrecovery.ConfirmCommand{Actor: actor, AccountID: accountID, OperationID: operationID, ResolutionID: resolutionID})
	if err != nil {
		s.writeActionRecoveryError(w, "confirm resolution", err)
		return
	}
	writeJSON(w, http.StatusOK, actionDetailResponse(detail))
}

func (s *Server) actionRecoveryRequestContext(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.actions == nil {
		writeProblem(w, http.StatusServiceUnavailable, "action_recovery_unavailable", "action recovery is unavailable")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if detail && len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "action detail does not accept query parameters")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	return claims, attentionActor(claims), accountID, true
}
func (s *Server) actionRecoveryCommandContext(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, actor, accountID, ok := s.actionRecoveryRequestContext(w, r, true)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}
func parseActionRecoveryQuery(r *http.Request) (actionrecovery.ListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "state", "cursor", "limit") {
		return actionrecovery.ListQuery{}, actionrecovery.ErrInvalid
	}
	query := actionrecovery.ListQuery{State: actionrecovery.State(values.Get("state"))}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return query, actionrecovery.ErrInvalid
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "action")
		if err != nil {
			return query, actionrecovery.ErrInvalid
		}
		query.AfterUpdatedAt = &cursor.UpdatedAt
		query.AfterOperationID = cursor.ID
	}
	return query, nil
}
func actionSummaryResponse(item actionrecovery.Summary) map[string]any {
	result := map[string]any{"operation_id": item.OperationID, "approval_id": item.ApprovalID, "invocation_id": item.InvocationID, "capability": item.Capability, "executor_id": item.ExecutorID, "executor_version": item.ExecutorVersion, "policy_version": item.PolicyVersion, "state": item.State, "attempt_count": item.AttemptCount, "started_at": item.StartedAt, "updated_at": item.UpdatedAt}
	if item.LastErrorCode != "" {
		result["last_error_code"] = item.LastErrorCode
	}
	if item.NextAttemptAt != nil {
		result["next_attempt_at"] = *item.NextAttemptAt
	}
	if item.CompletedAt != nil {
		result["completed_at"] = *item.CompletedAt
	}
	return result
}
func actionDetailResponse(detail actionrecovery.Detail) map[string]any {
	result := actionSummaryResponse(detail.Summary)
	if detail.Resolution != nil {
		resolution := detail.Resolution
		encoded := map[string]any{"id": resolution.ID, "operation_id": resolution.OperationID, "requested_outcome": resolution.RequestedOutcome, "reason_sha256": hex.EncodeToString(resolution.ReasonSHA256[:]), "requested_by_user_id": resolution.RequestedByUserID, "requested_at": resolution.RequestedAt, "state": resolution.State}
		if resolution.ConfirmedByUserID != "" {
			encoded["confirmed_by_user_id"] = resolution.ConfirmedByUserID
		}
		if resolution.ConfirmedAt != nil {
			encoded["confirmed_at"] = *resolution.ConfirmedAt
		}
		result["resolution"] = encoded
	}
	return result
}
func (s *Server) writeActionRecoveryError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, actionrecovery.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_action_recovery", "the action recovery request is invalid")
	case errors.Is(err, actionrecovery.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "action_not_found", "the consequential action was not found")
	case errors.Is(err, actionrecovery.ErrConflict):
		writeProblem(w, http.StatusConflict, "action_recovery_conflict", "the action recovery state changed; reload before retrying")
	case errors.Is(err, actionrecovery.ErrConstraint):
		writeProblem(w, http.StatusUnprocessableEntity, "action_recovery_rejected", "the action is not eligible for this recovery command")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow action recovery")
	default:
		s.logger.Error("action recovery operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "action_recovery_unavailable", "the action recovery operation could not be completed")
	}
}
