// Package admissionapi exposes a private, route-proof-bound capacity broker.
// It owns no cell data and accepts no browser session or bearer credential.
package admissionapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
)

const DefaultMaxBody = int64(64 << 10)

type Usage interface {
	Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error)
	Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error)
}

type Verifier interface {
	Verify(string, routecontext.Binding) (routecontext.Claims, error)
}

type ReviewerDirectory interface {
	ActiveRole(context.Context, ids.AccountID, ids.UserID) (accounts.MembershipRole, bool, error)
}

type AgentExecutionAuthorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Server struct {
	usage     Usage
	verifiers map[ids.CellID]Verifier
	logger    *slog.Logger
	maxBody   int64
	reviewers ReviewerDirectory
	agents    AgentExecutionAuthorizer
}

type Option func(*Server)

func WithReviewerDirectory(directory ReviewerDirectory) Option {
	return func(server *Server) { server.reviewers = directory }
}

func WithAgentExecutionAuthorizer(authorizer AgentExecutionAuthorizer) Option {
	return func(server *Server) { server.agents = authorizer }
}

func New(usage Usage, verifiers map[ids.CellID]Verifier, logger *slog.Logger, maxBody int64, options ...Option) (*Server, error) {
	if usage == nil || len(verifiers) == 0 || logger == nil || maxBody <= 0 || maxBody > 1<<20 {
		return nil, errors.New("admission API dependencies and bounded body size are required")
	}
	copyVerifiers := make(map[ids.CellID]Verifier, len(verifiers))
	for cellID, verifier := range verifiers {
		if !routecontext.ValidCellID(cellID) || verifier == nil {
			return nil, errors.New("admission API cell verifier is invalid")
		}
		copyVerifiers[cellID] = verifier
	}
	server := &Server{usage: usage, verifiers: copyVerifiers, logger: logger, maxBody: maxBody}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/route-canary", s.routeCanary)
	mux.HandleFunc("POST /internal/v1/work/capacity/reserve", s.reserve)
	mux.HandleFunc("POST /internal/v1/work/capacity/release", s.release)
	mux.HandleFunc("POST /internal/v1/attention/reviewers:resolve", s.resolveReviewer)
	mux.HandleFunc("POST /internal/v1/agents/work-executions:authorize", s.authorizeWorkAgentExecution)
	mux.HandleFunc("POST /internal/v1/agents/schedule-executions:authorize", s.authorizeScheduleExecution)
	return s.recover(s.securityHeaders(mux))
}

type scheduleExecutionAuthorizationRequest struct {
	CellID            ids.CellID     `json:"cell_id"`
	AccountID         ids.AccountID  `json:"account_id"`
	UserID            ids.UserID     `json:"user_id"`
	ScheduleID        ids.ScheduleID `json:"schedule_id"`
	RequiresWork      bool           `json:"requires_work"`
	RequiresKnowledge bool           `json:"requires_knowledge"`
}

type scheduleExecutionAuthorizationResponse struct {
	CellID                ids.CellID     `json:"cell_id"`
	AccountID             ids.AccountID  `json:"account_id"`
	UserID                ids.UserID     `json:"user_id"`
	ScheduleID            ids.ScheduleID `json:"schedule_id"`
	EntitlementVersion    uint64         `json:"entitlement_version"`
	MaximumConcurrentRuns int64          `json:"maximum_concurrent_runs"`
	CanReadRestricted     bool           `json:"can_read_restricted"`
}

func (s *Server) authorizeScheduleExecution(w http.ResponseWriter, r *http.Request) {
	if s.agents == nil {
		writeProblem(w, http.StatusServiceUnavailable, "agent_authorization_unavailable", "Agent authorization is temporarily unavailable")
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Schedule authorization requires application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request scheduleExecutionAuthorizationRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Schedule authorization request is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Schedule authorization request is invalid")
		return
	}
	if _, exists := s.verifiers[request.CellID]; !exists || ids.Validate(string(request.AccountID)) != nil ||
		ids.Validate(string(request.UserID)) != nil || ids.Validate(string(request.ScheduleID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Schedule authorization request is invalid")
		return
	}
	if identity, verified := workloadidentity.ClientIdentityFromContext(r.Context()); verified {
		expected := "spiffe://infiniteocean.net/spyglass/cells/" + string(request.CellID) + "/schedule-execution-worker"
		if identity != expected {
			writeProblem(w, http.StatusForbidden, "agent_workload_scope_denied", "the workload identity cannot authorize Schedules for this cell")
			return
		}
	}
	actor := access.Actor{UserID: request.UserID}
	accountContext, err := s.agents.Authorize(r.Context(), actor, request.AccountID,
		access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}, Package: catalog.PackageAgents, Mutation: true})
	if err == nil && request.RequiresWork {
		_, err = s.agents.Authorize(r.Context(), actor, request.AccountID, access.Requirement{Package: catalog.PackageWork})
	}
	if err == nil && request.RequiresKnowledge {
		_, err = s.agents.Authorize(r.Context(), actor, request.AccountID, access.Requirement{Package: catalog.PackageKnowledge})
	}
	if err != nil {
		var denied *access.DeniedError
		if errors.As(err, &denied) {
			writeProblem(w, http.StatusForbidden, string(denied.Code), "the Schedule creator can no longer execute the configured packages for this Account")
			return
		}
		s.logger.Error("Schedule Agent authorization failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "agent_authorization_unavailable", "Schedule authorization is temporarily unavailable")
		return
	}
	maximum, exists := accountContext.PackageAccess.Limits[catalog.LimitCode("concurrent_runs")]
	if accountContext.CellID != request.CellID || accountContext.AccountID != request.AccountID || accountContext.EntitlementVersion == 0 || !exists || maximum < 1 {
		writeProblem(w, http.StatusForbidden, "agent_authorization_stale", "the Account placement or Agent entitlement changed")
		return
	}
	writeJSON(w, http.StatusOK, scheduleExecutionAuthorizationResponse{CellID: request.CellID, AccountID: request.AccountID,
		UserID: request.UserID, ScheduleID: request.ScheduleID, EntitlementVersion: accountContext.EntitlementVersion,
		MaximumConcurrentRuns: maximum, CanReadRestricted: accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleAdministrator})
}

type workAgentAuthorizationRequest struct {
	CellID      ids.CellID    `json:"cell_id"`
	AccountID   ids.AccountID `json:"account_id"`
	UserID      ids.UserID    `json:"user_id"`
	ExecutionID string        `json:"execution_id"`
}

type workAgentAuthorizationResponse struct {
	CellID                ids.CellID    `json:"cell_id"`
	AccountID             ids.AccountID `json:"account_id"`
	UserID                ids.UserID    `json:"user_id"`
	ExecutionID           string        `json:"execution_id"`
	EntitlementVersion    uint64        `json:"entitlement_version"`
	MaximumConcurrentRuns int64         `json:"maximum_concurrent_runs"`
}

// authorizeWorkAgentExecution is workload-only. The outer admission server
// authenticates the caller with mTLS; this handler resolves fresh global
// Membership, placement, package mode, and limit state without accepting or
// replaying a browser credential.
func (s *Server) authorizeWorkAgentExecution(w http.ResponseWriter, r *http.Request) {
	if s.agents == nil {
		writeProblem(w, http.StatusServiceUnavailable, "agent_authorization_unavailable", "Agent authorization is temporarily unavailable")
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Agent authorization requires application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request workAgentAuthorizationRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Agent authorization request is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Agent authorization request is invalid")
		return
	}
	if _, exists := s.verifiers[request.CellID]; !exists || ids.Validate(string(request.AccountID)) != nil ||
		ids.Validate(string(request.UserID)) != nil || ids.Validate(request.ExecutionID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_agent_authorization", "the Agent authorization request is invalid")
		return
	}
	if identity, verified := workloadidentity.ClientIdentityFromContext(r.Context()); verified {
		expected := "spiffe://infiniteocean.net/spyglass/cells/" + string(request.CellID) + "/agent-dispatch-worker"
		if identity != expected {
			writeProblem(w, http.StatusForbidden, "agent_workload_scope_denied", "the workload identity cannot authorize executions for this cell")
			return
		}
	}
	accountContext, err := s.agents.Authorize(r.Context(), access.Actor{UserID: request.UserID}, request.AccountID,
		access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}, Package: catalog.PackageAgents, Mutation: true})
	if err != nil {
		var denied *access.DeniedError
		if errors.As(err, &denied) {
			writeProblem(w, http.StatusForbidden, string(denied.Code), "the initiating User can no longer execute Agents for this Account")
			return
		}
		s.logger.Error("Work Agent authorization failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "agent_authorization_unavailable", "Agent authorization is temporarily unavailable")
		return
	}
	maximum, exists := accountContext.PackageAccess.Limits[catalog.LimitCode("concurrent_runs")]
	if accountContext.CellID != request.CellID || accountContext.AccountID != request.AccountID || accountContext.EntitlementVersion == 0 || !exists || maximum < 1 {
		writeProblem(w, http.StatusForbidden, "agent_authorization_stale", "the Account placement or Agent entitlement changed")
		return
	}
	writeJSON(w, http.StatusOK, workAgentAuthorizationResponse{CellID: request.CellID, AccountID: request.AccountID,
		UserID: request.UserID, ExecutionID: request.ExecutionID, EntitlementVersion: accountContext.EntitlementVersion,
		MaximumConcurrentRuns: maximum})
}

type reviewerRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	UserID       ids.UserID           `json:"user_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

type reviewerResponse struct {
	UserID ids.UserID              `json:"user_id"`
	Active bool                    `json:"active"`
	Role   accounts.MembershipRole `json:"role,omitempty"`
}

func (s *Server) resolveReviewer(w http.ResponseWriter, r *http.Request) {
	if s.reviewers == nil {
		writeProblem(w, http.StatusServiceUnavailable, "reviewer_directory_unavailable", "reviewer eligibility is temporarily unavailable")
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "reviewer lookup requires application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request reviewerRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_reviewer_request", "the reviewer lookup is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_reviewer_request", "the reviewer lookup is invalid")
		return
	}
	verifier, exists := s.verifiers[request.CellID]
	if !exists || ids.Validate(string(request.UserID)) != nil || request.RouteContext == "" {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return
	}
	claims, err := verifier.Verify(request.RouteContext, request.Binding)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return
	}
	if claims.Authority.CellID != request.CellID || claims.Authority.ActorKind != "user" ||
		claims.Authority.PackageAccess == nil || claims.Authority.PackageAccess.Code != string(catalog.PackageWork) ||
		claims.Authority.PackageAccess.Mode != string(catalog.ModeEnabled) || !reviewCreationBinding(claims) {
		writeProblem(w, http.StatusForbidden, "reviewer_scope_denied", "the routed operation cannot resolve a Work reviewer")
		return
	}
	role, active, err := s.reviewers.ActiveRole(r.Context(), claims.Authority.AccountID, request.UserID)
	if err != nil {
		s.logger.Error("Attention reviewer lookup failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "reviewer_directory_unavailable", "reviewer eligibility is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, reviewerResponse{UserID: request.UserID, Active: active, Role: role})
}

func reviewCreationBinding(claims routecontext.Claims) bool {
	return claims.Binding.Method == http.MethodPost && claims.Authority.OperationID != "" &&
		claims.Binding.Target == "/api/v1/accounts/"+string(claims.Authority.AccountID)+"/attention/work-reviews"
}

type routeCanaryRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

func (s *Server) routeCanary(w http.ResponseWriter, r *http.Request) {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "route canary requires application/json")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request routeCanaryRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_route_canary", "the route canary is invalid")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_route_canary", "the route canary is invalid")
		return
	}
	verifier, exists := s.verifiers[request.CellID]
	if !exists || request.RouteContext == "" {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_canary", "the route canary could not be verified")
		return
	}
	claims, err := verifier.Verify(request.RouteContext, request.Binding)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_canary", "the route canary could not be verified")
		return
	}
	expectedTarget := "/api/v1/accounts/" + string(claims.Authority.AccountID) + "/context"
	expectedBinding, _ := routecontext.Bind(http.MethodGet, expectedTarget, nil)
	if claims.Authority.CellID != request.CellID || claims.Authority.ActorKind != "workload" || claims.Authority.ActorID != routecontext.RotationCanaryActorID || claims.Authority.Role != "" || claims.Authority.OperationID != "" || claims.Authority.PackageAccess != nil || claims.Binding != expectedBinding {
		writeProblem(w, http.StatusForbidden, "route_canary_scope_denied", "the route proof is not a rotation canary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "verified", "cell_id": request.CellID, "key_id": claims.KeyID})
}

type capacityRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	RequestID    string               `json:"request_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

type capacityResponse struct {
	RequestID    string                          `json:"request_id"`
	State        usageadmission.ReservationState `json:"state"`
	Current      int64                           `json:"current"`
	Maximum      int64                           `json:"maximum"`
	ExpiresAt    *time.Time                      `json:"expires_at,omitempty"`
	NewlyCreated bool                            `json:"newly_created"`
}

func (s *Server) reserve(w http.ResponseWriter, r *http.Request) {
	request, claims, actor, ok := s.accept(w, r)
	if !ok {
		return
	}
	reservation, err := s.usage.Reserve(r.Context(), usageadmission.ReserveCommand{Actor: actor, AccountID: claims.Authority.AccountID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: request.RequestID})
	if err != nil {
		s.writeUsageError(w, "reserve", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityView(reservation))
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	request, claims, actor, ok := s.accept(w, r)
	if !ok {
		return
	}
	reservation, err := s.usage.Release(r.Context(), usageadmission.ReleaseCommand{Actor: actor, AccountID: claims.Authority.AccountID, RequestID: request.RequestID})
	if err != nil {
		s.writeUsageError(w, "release", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityView(reservation))
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (capacityRequest, routecontext.Claims, access.Actor, bool) {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "capacity admission requires application/json")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request capacityRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_admission_request", "the capacity request is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_admission_request", "the capacity request is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	verifier, exists := s.verifiers[request.CellID]
	if !exists || ids.Validate(request.RequestID) != nil || request.RouteContext == "" {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	claims, err := verifier.Verify(request.RouteContext, request.Binding)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	if claims.Authority.CellID != request.CellID || claims.Authority.OperationID != request.RequestID || claims.Authority.ActorKind != "user" || !workMutationBinding(claims) || claims.Authority.PackageAccess == nil || claims.Authority.PackageAccess.Code != string(catalog.PackageWork) || claims.Authority.PackageAccess.Mode != string(catalog.ModeEnabled) {
		writeProblem(w, http.StatusForbidden, "admission_scope_denied", "the routed operation cannot manage Work capacity")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	return request, claims, access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}, true
}

func workMutationBinding(claims routecontext.Claims) bool {
	binding, accountID := claims.Binding, claims.Authority.AccountID
	base := "/api/v1/accounts/" + string(accountID) + "/work-items"
	if binding.Method != http.MethodPost {
		return false
	}
	if binding.Target == base {
		return true
	}
	remaining, found := strings.CutPrefix(binding.Target, base+"/")
	if !found {
		return false
	}
	itemID, action, found := strings.Cut(remaining, "/")
	return found && action == "transitions" && ids.Validate(itemID) == nil
}

func capacityView(reservation usageadmission.Reservation) capacityResponse {
	return capacityResponse{RequestID: reservation.RequestID, State: reservation.State, Current: reservation.Current, Maximum: reservation.Maximum, ExpiresAt: reservation.ExpiresAt, NewlyCreated: reservation.NewlyCreated}
}

func (s *Server) writeUsageError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.As(err, &denied):
		writeProblemFields(w, http.StatusForbidden, string(denied.Code), "current Account access does not admit this Work operation", denied.Current, denied.Maximum)
	case errors.Is(err, usageadmission.ErrInvalidRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_usage_request", "the capacity request is invalid")
	case errors.Is(err, usageadmission.ErrReservationConflict):
		writeProblem(w, http.StatusConflict, "reservation_conflict", "the operation key belongs to different capacity work")
	case errors.Is(err, usageadmission.ErrReservationClosed):
		writeProblem(w, http.StatusConflict, "reservation_closed", "the operation key was already finalized")
	case errors.Is(err, usageadmission.ErrEntitlementChanged):
		writeProblem(w, http.StatusConflict, "entitlement_changed", "Account access changed; retry with fresh routing")
	default:
		s.logger.Error("Work capacity admission failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "admission_unavailable", "Work capacity admission is temporarily unavailable")
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("admission API panic", "value", value)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "capacity admission could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblemFields(w, status, code, detail, 0, 0)
}

func writeProblemFields(w http.ResponseWriter, status int, code, detail string, current, maximum int64) {
	value := map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail}
	if current != 0 {
		value["current"] = current
	}
	if maximum != 0 {
		value["maximum"] = maximum
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
