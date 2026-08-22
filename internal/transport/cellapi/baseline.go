package cellapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type baselineStartRequest struct {
	CatalogVersion     string `json:"catalog_version"`
	ScopePolicyVersion string `json:"scope_policy_version"`
}

type baselineAnswerRequest struct {
	QuestionKey string                    `json:"question_key"`
	Kind        baselinedomain.AnswerKind `json:"kind"`
	Fact        *baselineFactRequest      `json:"fact,omitempty"`
	Reason      string                    `json:"reason,omitempty"`
}

type baselineFactRequest struct {
	FactID   ids.KnowledgeFactID `json:"fact_id"`
	Revision uint64              `json:"revision"`
}

type baselineResponsibilityRequest struct {
	Kind baselinedomain.ResponsibilityKind `json:"kind"`
	ID   string                            `json:"id,omitempty"`
}

type baselineRequirementRequest struct {
	ID             ids.BaselineRequirementID     `json:"id"`
	Code           string                        `json:"code"`
	Title          string                        `json:"title"`
	Responsibility baselineResponsibilityRequest `json:"responsibility"`
	RenewAfterDays uint16                        `json:"renew_after_days"`
}

type baselineInventoryRequest struct {
	Requirements []baselineRequirementRequest `json:"requirements"`
}

type baselineEvidenceDecisionRequest struct {
	RequirementID ids.BaselineRequirementID           `json:"requirement_id"`
	EvidenceID    ids.KnowledgeEvidenceID             `json:"evidence_id"`
	Decision      baselinedomain.EvidenceDecisionKind `json:"decision"`
	Reason        string                              `json:"reason"`
}

type baselineDispositionRequest struct {
	RequirementID ids.BaselineRequirementID             `json:"requirement_id"`
	Disposition   baselinedomain.RequirementDisposition `json:"disposition"`
	Reason        string                                `json:"reason"`
}

type baselinePlanApprovalRequest struct {
	PlanID            ids.BaselinePlanID `json:"plan_id"`
	ContentSHA256     string             `json:"content_sha256"`
	AssessmentVersion uint64             `json:"assessment_version"`
}

type baselineReassessmentRequest struct {
	CatalogVersion     string `json:"catalog_version"`
	ScopePolicyVersion string `json:"scope_policy_version"`
}

func (s *Server) baselineStart(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.baselineCommandContext(w, r, false)
	if !ok {
		return
	}
	var body baselineStartRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	assessment, err := s.baseline.Start(routecontext.WithClaims(r.Context(), claims), baselineapp.StartCommand{Actor: actor, AccountID: accountID, AssessmentID: ids.BaselineAssessmentID(operationID), CatalogVersion: body.CatalogVersion, ScopePolicyVersion: body.ScopePolicyVersion, CorrelationID: operationID})
	if err != nil {
		s.writeBaselineError(w, "start", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/baseline-assessments/%s", accountID, assessment.ID))
	writeBaselineAssessment(w, http.StatusCreated, assessment)
}

func (s *Server) baselineGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, assessmentID, ok := s.baselineRequestContext(w, r, true)
	if !ok {
		return
	}
	assessment, err := s.baseline.Get(routecontext.WithClaims(r.Context(), claims), actor, accountID, assessmentID)
	if err != nil {
		s.writeBaselineError(w, "get", err)
		return
	}
	writeBaselineAssessment(w, http.StatusOK, assessment)
}

func (s *Server) baselineAnswer(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, assessmentID, operationID, version, ok := s.baselineVersionedContext(w, r)
	if !ok {
		return
	}
	var body baselineAnswerRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	var fact *baselinedomain.FactReference
	if body.Fact != nil {
		fact = &baselinedomain.FactReference{FactID: body.Fact.FactID, Revision: body.Fact.Revision}
	}
	assessment, err := s.baseline.Answer(routecontext.WithClaims(r.Context(), claims), baselineapp.AnswerCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, QuestionKey: body.QuestionKey, Kind: body.Kind, Fact: fact, Reason: body.Reason, ExpectedVersion: version, CorrelationID: operationID})
	s.writeBaselineResult(w, "answer", assessment, err)
}

func (s *Server) baselineBeginInventory(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok || !decodeBaselineJSON(w, r, &struct{}{}) {
		return
	}
	assessment, err := s.baseline.BeginInventory(routecontext.WithClaims(r.Context(), claims), command)
	s.writeBaselineResult(w, "begin inventory", assessment, err)
}

func (s *Server) baselineCompleteInventory(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselineInventoryRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	requirements := make([]baselinedomain.RequirementDraft, 0, len(body.Requirements))
	for _, value := range body.Requirements {
		requirements = append(requirements, baselinedomain.RequirementDraft{ID: value.ID, Code: value.Code, Title: value.Title, Responsibility: baselinedomain.Responsibility{Kind: value.Responsibility.Kind, ID: value.Responsibility.ID}, RenewAfterDays: value.RenewAfterDays})
	}
	assessment, err := s.baseline.CompleteInventory(routecontext.WithClaims(r.Context(), claims), baselineapp.CompleteInventoryCommand{AdvanceCommand: command, Requirements: requirements})
	s.writeBaselineResult(w, "complete inventory", assessment, err)
}

func (s *Server) baselineDecideEvidence(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselineEvidenceDecisionRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	assessment, err := s.baseline.DecideEvidence(routecontext.WithClaims(r.Context(), claims), baselineapp.DecideEvidenceCommand{AdvanceCommand: command, RequirementID: body.RequirementID, EvidenceID: body.EvidenceID, Decision: body.Decision, Reason: body.Reason})
	s.writeBaselineResult(w, "decide evidence", assessment, err)
}

func (s *Server) baselineDisposition(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselineDispositionRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	assessment, err := s.baseline.Disposition(routecontext.WithClaims(r.Context(), claims), baselineapp.DispositionCommand{AdvanceCommand: command, RequirementID: body.RequirementID, Disposition: body.Disposition, Reason: body.Reason})
	s.writeBaselineResult(w, "disposition requirement", assessment, err)
}

func (s *Server) baselineSubmitPlan(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body struct{}
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	assessment, err := s.baseline.SubmitPlan(routecontext.WithClaims(r.Context(), claims), baselineapp.SubmitPlanCommand{AdvanceCommand: command, PlanID: ids.BaselinePlanID(command.CorrelationID)})
	s.writeBaselineResult(w, "submit plan", assessment, err)
}

func (s *Server) baselineApprovePlan(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselinePlanApprovalRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	digest, ok := baselineDigest(w, body.ContentSHA256)
	if !ok {
		return
	}
	assessment, err := s.baseline.ApprovePlan(routecontext.WithClaims(r.Context(), claims), baselineapp.ApprovePlanCommand{AdvanceCommand: command, PlanID: body.PlanID, ContentSHA256: digest, AssessmentVersion: body.AssessmentVersion})
	s.writeBaselineResult(w, "approve plan", assessment, err)
}

func (s *Server) baselineMaterializePlan(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselinePlanApprovalRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	digest, ok := baselineDigest(w, body.ContentSHA256)
	if !ok {
		return
	}
	items, err := s.baseline.MaterializePlan(routecontext.WithClaims(r.Context(), claims), baselineapp.MaterializePlanCommand{AdvanceCommand: command, PlanID: body.PlanID, ContentSHA256: digest, AssessmentVersion: body.AssessmentVersion})
	if err != nil {
		s.writeBaselineError(w, "materialize plan", err)
		return
	}
	output := make([]workItemResponse, 0, len(items))
	for _, item := range items {
		output = append(output, workItemView(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": output})
}

func (s *Server) baselineMarkReady(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok || !decodeBaselineJSON(w, r, &struct{}{}) {
		return
	}
	assessment, err := s.baseline.MarkReady(routecontext.WithClaims(r.Context(), claims), command)
	s.writeBaselineResult(w, "mark ready", assessment, err)
}

func (s *Server) baselineReassess(w http.ResponseWriter, r *http.Request) {
	claims, command, ok := s.baselineAdvanceCommand(w, r)
	if !ok {
		return
	}
	var body baselineReassessmentRequest
	if !decodeBaselineJSON(w, r, &body) {
		return
	}
	archived, next, err := s.baseline.Reassess(routecontext.WithClaims(r.Context(), claims), baselineapp.ReassessCommand{AdvanceCommand: command, NewAssessmentID: ids.BaselineAssessmentID(command.CorrelationID), CatalogVersion: body.CatalogVersion, ScopePolicyVersion: body.ScopePolicyVersion})
	if err != nil {
		s.writeBaselineError(w, "reassess", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"archived": baselineAssessmentView(archived), "next": baselineAssessmentView(next)})
}

func (s *Server) baselineRequestContext(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, ids.BaselineAssessmentID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if s.baseline == nil {
		writeProblem(w, http.StatusServiceUnavailable, "baseline_unavailable", "Baseline is unavailable in this cell")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	assessmentID := ids.BaselineAssessmentID(r.PathValue("assessmentID"))
	if assessmentID != "" && ids.Validate(string(assessmentID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_assessment", "the Baseline assessment identifier is invalid")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	if detail && len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_query", "Baseline detail does not accept query parameters")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, attentionActor(claims), accountID, assessmentID, true
}

func (s *Server) baselineCommandContext(w http.ResponseWriter, r *http.Request, requireAssessment bool) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, actor, accountID, assessmentID, ok := s.baselineRequestContext(w, r, false)
	if !ok || (requireAssessment && assessmentID == "") {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || strings.TrimSpace(values[0]) != claims.Authority.OperationID || ids.Validate(claims.Authority.OperationID) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_idempotency_key", "the Idempotency-Key does not match routed command authority")
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return claims, actor, accountID, claims.Authority.OperationID, true
}

func (s *Server) baselineVersionedContext(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, ids.BaselineAssessmentID, string, uint64, bool) {
	claims, actor, accountID, operationID, ok := s.baselineCommandContext(w, r, true)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	assessmentID := ids.BaselineAssessmentID(r.PathValue("assessmentID"))
	values := r.Header.Values("If-Match")
	version, err := parseAttentionVersion(values)
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "baseline_version_required", "If-Match with the current assessment version is required")
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_version", "If-Match must contain exactly one weak assessment version ETag")
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	return claims, actor, accountID, assessmentID, operationID, version, true
}

func (s *Server) baselineAdvanceCommand(w http.ResponseWriter, r *http.Request) (routecontext.Claims, baselineapp.AdvanceCommand, bool) {
	claims, actor, accountID, assessmentID, operationID, version, ok := s.baselineVersionedContext(w, r)
	return claims, baselineapp.AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: version, CorrelationID: operationID}, ok
}

func baselineDigest(w http.ResponseWriter, raw string) ([sha256.Size]byte, bool) {
	var result [sha256.Size]byte
	value, err := hex.DecodeString(raw)
	if err != nil || len(value) != sha256.Size {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_plan", "content_sha256 must be exactly 64 hexadecimal characters")
		return result, false
	}
	copy(result[:], value)
	return result, true
}

func decodeBaselineJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_command", "Baseline commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Baseline commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_command", "the Baseline command body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_command", "the Baseline command body is invalid")
		return false
	}
	return true
}

func (s *Server) writeBaselineResult(w http.ResponseWriter, operation string, assessment baselinedomain.Assessment, err error) {
	if err != nil {
		s.writeBaselineError(w, operation, err)
		return
	}
	writeBaselineAssessment(w, http.StatusOK, assessment)
}

func writeBaselineAssessment(w http.ResponseWriter, status int, assessment baselinedomain.Assessment) {
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, assessment.Version))
	writeJSON(w, status, baselineAssessmentView(assessment))
}

func baselineAssessmentView(value baselinedomain.Assessment) map[string]any {
	answers := make([]map[string]any, 0, len(value.Answers))
	for _, answer := range value.Answers {
		item := map[string]any{"question_key": answer.QuestionKey, "kind": answer.Kind, "reason": answer.Reason, "answered_by_user_id": answer.AnsweredBy.UserID, "answered_at": answer.AnsweredAt}
		if answer.Fact != nil {
			item["fact"] = map[string]any{"fact_id": answer.Fact.FactID, "revision": answer.Fact.Revision}
		}
		answers = append(answers, item)
	}
	requirements := make([]map[string]any, 0, len(value.Requirements))
	for _, requirement := range value.Requirements {
		evidence := make([]map[string]any, 0, len(requirement.Evidence))
		for _, decision := range requirement.Evidence {
			evidence = append(evidence, map[string]any{"evidence_id": decision.EvidenceID, "decision": decision.Decision, "reason": decision.Reason, "decided_by_user_id": decision.DecidedBy.UserID, "decided_at": decision.DecidedAt})
		}
		responsibility := map[string]any{"kind": requirement.Responsibility.Kind}
		if requirement.Responsibility.ID != "" {
			responsibility["id"] = requirement.Responsibility.ID
		}
		item := map[string]any{"id": requirement.ID, "code": requirement.Code, "title": requirement.Title, "responsibility": responsibility, "renew_after_days": requirement.RenewAfterDays, "catalog_version": requirement.CatalogVersion, "scope_policy_version": requirement.ScopePolicyVersion, "disposition": requirement.Disposition, "reason": requirement.Reason, "evidence": evidence}
		if requirement.RenewAt != nil {
			item["renew_at"] = requirement.RenewAt
		}
		requirements = append(requirements, item)
	}
	result := map[string]any{"id": value.ID, "account_id": value.AccountID, "catalog_version": value.CatalogVersion, "scope_policy_version": value.ScopePolicyVersion, "state": value.State, "answers": answers, "requirements": requirements, "created_by_user_id": value.CreatedBy.UserID, "version": value.Version, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt}
	if value.Plan != nil {
		plan := map[string]any{"id": value.Plan.ID, "assessment_version": value.Plan.AssessmentVersion, "content_sha256": hex.EncodeToString(value.Plan.ContentSHA256[:]), "proposed_work_count": value.Plan.ProposedWorkCount}
		if value.Plan.ApprovedBy != nil {
			plan["approved_by_user_id"], plan["approved_at"] = value.Plan.ApprovedBy.UserID, value.Plan.ApprovedAt
		}
		result["plan"] = plan
	}
	if value.SupersededBy != "" {
		result["superseded_by_assessment_id"] = value.SupersededBy
	}
	return result
}

func (s *Server) writeBaselineError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, baselineapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_baseline_request", "the Baseline request is invalid")
	case errors.Is(err, baselineapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "baseline_not_found", "the Baseline assessment was not found")
	case errors.Is(err, baselineapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "baseline_conflict", "Baseline changed; reload before retrying")
	case errors.Is(err, baselineapp.ErrConstraint):
		writeProblem(w, http.StatusUnprocessableEntity, "baseline_rejected", "the Baseline command violates an evidence, plan, or state constraint")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Baseline operation")
	default:
		s.logger.Error("Baseline operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "baseline_unavailable", "the Baseline operation could not be completed")
	}
}

var _ BaselineService = (*baselineapp.Service)(nil)
