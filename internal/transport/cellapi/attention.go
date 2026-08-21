package cellapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type AttentionService interface {
	CreateInformation(context.Context, attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error)
	AnswerInformation(context.Context, attentionapp.AnswerInformationCommand) (attentionapp.InformationCompletion, error)
	CancelInformation(context.Context, attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error)
	GetInformation(context.Context, access.Actor, ids.AccountID, ids.InformationRequestID) (attentiondomain.InformationRequest, error)
	ListInformation(context.Context, access.Actor, ids.AccountID, attentionapp.InformationListQuery) (attentionapp.InformationSummaryPage, error)
	CreateWorkReview(context.Context, attentionapp.CreateWorkReviewCommand) (attentiondomain.WorkReview, error)
	DecideWorkReview(context.Context, attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error)
	CancelWorkReview(context.Context, attentionapp.CancelWorkReviewCommand) (attentiondomain.WorkReview, error)
	GetWorkReview(context.Context, access.Actor, ids.AccountID, ids.WorkReviewID) (attentiondomain.WorkReview, error)
	ListWorkReviews(context.Context, access.Actor, ids.AccountID, attentionapp.WorkReviewListQuery) (attentionapp.WorkReviewSummaryPage, error)
	CreateApproval(context.Context, attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	DecideApproval(context.Context, attentionapp.DecideApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	CancelApproval(context.Context, attentionapp.CancelApprovalCommand) (attentiondomain.ConsequentialApproval, error)
	GetApproval(context.Context, access.Actor, ids.AccountID, ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error)
	ListApprovals(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error)
}

type attentionRequirementRequest struct {
	Key     string                           `json:"key"`
	Scope   attentiondomain.InformationScope `json:"scope"`
	ScopeID string                           `json:"scope_id,omitempty"`
}
type createInformationRequest struct {
	ParentWorkItemID ids.WorkItemID              `json:"parent_work_item_id"`
	Requirement      attentionRequirementRequest `json:"requirement"`
	Question         string                      `json:"question"`
}
type answerInformationRequest struct {
	FactID      string                      `json:"fact_id"`
	FactVersion uint64                      `json:"fact_version"`
	Requirement attentionRequirementRequest `json:"requirement"`
}
type reasonRequest struct {
	Reason string `json:"reason"`
}
type createReviewRequest struct {
	WorkItemID     ids.WorkItemID `json:"work_item_id"`
	WorkVersion    uint64         `json:"work_version"`
	ProposalSHA256 string         `json:"proposal_sha256"`
	Question       string         `json:"question"`
	ReviewerID     ids.UserID     `json:"reviewer_id"`
}
type reviewDecisionRequest struct {
	Decision attentiondomain.WorkReviewDecision `json:"decision"`
	Reason   string                             `json:"reason"`
}
type createApprovalRequest struct {
	OperationID              string                `json:"operation_id"`
	InvocationID             ids.AgentInvocationID `json:"invocation_id"`
	WorkItemID               ids.WorkItemID        `json:"work_item_id,omitempty"`
	Capability               string                `json:"capability"`
	Payload                  json.RawMessage       `json:"payload"`
	EvidenceSHA256           string                `json:"evidence_sha256"`
	PolicyVersion            uint64                `json:"policy_version"`
	RequireIndependentReview bool                  `json:"require_independent_review"`
	ExpiresAt                time.Time             `json:"expires_at"`
}
type approvalDecisionRequest struct {
	Decision attentiondomain.ApprovalDecision `json:"decision"`
	Reason   string                           `json:"reason"`
}

type attentionCursorEnvelope struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

func (s *Server) attentionInformationList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, false)
	if !ok {
		return
	}
	query, err := parseInformationQuery(r)
	if err != nil {
		s.writeAttentionError(w, "list information", err)
		return
	}
	page, err := s.attention.ListInformation(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeAttentionError(w, "list information", err)
		return
	}
	items := make([]informationSummaryResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, informationSummaryView(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("information", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) attentionInformationCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	var request createInformationRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.CreateInformation(routecontext.WithClaims(r.Context(), claims), attentionapp.CreateInformationCommand{
		Actor: actor, AccountID: accountID, RequestID: ids.InformationRequestID(operationID), ParentWorkItemID: request.ParentWorkItemID,
		Requirement: request.Requirement.domain(), Question: request.Question, CorrelationID: operationID,
	})
	if err != nil {
		s.writeAttentionError(w, "create information", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/attention/information-requests/%s", accountID, item.ID))
	writeAttentionAggregate(w, http.StatusCreated, informationView(item))
}

func (s *Server) attentionInformationGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, true)
	if !ok {
		return
	}
	requestID := ids.InformationRequestID(r.PathValue("requestID"))
	if !validAttentionDetail(r, string(requestID)) {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_query", "the Attention request is invalid")
		return
	}
	item, err := s.attention.GetInformation(routecontext.WithClaims(r.Context(), claims), actor, accountID, requestID)
	if err != nil {
		s.writeAttentionError(w, "get information", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, informationView(item))
}

func (s *Server) attentionInformationAnswer(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	requestID, version, ok := attentionTarget[ids.InformationRequestID](w, r, "requestID")
	if !ok {
		return
	}
	var request answerInformationRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	completion, err := s.attention.AnswerInformation(routecontext.WithClaims(r.Context(), claims), attentionapp.AnswerInformationCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ExpectedVersion: version, CorrelationID: operationID,
		Fact: attentiondomain.FactReference{ID: request.FactID, Version: request.FactVersion, Requirement: request.Requirement.domain()},
	})
	if err != nil {
		s.writeAttentionError(w, "answer information", err)
		return
	}
	answered := make([]informationResponse, 0, len(completion.Answered))
	for _, item := range completion.Answered {
		answered = append(answered, informationView(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": answered, "resumable_parent_ids": completion.ResumableParents})
}

func (s *Server) attentionInformationCancel(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	requestID, version, ok := attentionTarget[ids.InformationRequestID](w, r, "requestID")
	if !ok {
		return
	}
	var request reasonRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.CancelInformation(routecontext.WithClaims(r.Context(), claims), attentionapp.CancelInformationCommand{Actor: actor, AccountID: accountID, RequestID: requestID, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeAttentionError(w, "cancel information", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, informationView(item))
}

func (s *Server) attentionReviewList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, false)
	if !ok {
		return
	}
	query, err := parseReviewQuery(r)
	if err != nil {
		s.writeAttentionError(w, "list reviews", err)
		return
	}
	page, err := s.attention.ListWorkReviews(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeAttentionError(w, "list reviews", err)
		return
	}
	items := make([]reviewSummaryResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, reviewSummaryView(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("review", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) attentionReviewCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	var request createReviewRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	digest, valid := decodeDigest(request.ProposalSHA256)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "proposal_sha256 must be a lowercase SHA-256 value")
		return
	}
	item, err := s.attention.CreateWorkReview(routecontext.WithClaims(r.Context(), claims), attentionapp.CreateWorkReviewCommand{
		Actor: actor, AccountID: accountID, ReviewID: ids.WorkReviewID(operationID), WorkItemID: request.WorkItemID, WorkVersion: request.WorkVersion,
		ProposalSHA256: digest, Question: request.Question, ReviewerID: request.ReviewerID, CorrelationID: operationID,
	})
	if err != nil {
		s.writeAttentionError(w, "create review", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/attention/work-reviews/%s", accountID, item.ID))
	writeAttentionAggregate(w, http.StatusCreated, reviewView(item))
}

func (s *Server) attentionReviewGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, true)
	if !ok {
		return
	}
	reviewID := ids.WorkReviewID(r.PathValue("reviewID"))
	if !validAttentionDetail(r, string(reviewID)) {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_query", "the Attention review is invalid")
		return
	}
	item, err := s.attention.GetWorkReview(routecontext.WithClaims(r.Context(), claims), actor, accountID, reviewID)
	if err != nil {
		s.writeAttentionError(w, "get review", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, reviewView(item))
}

func (s *Server) attentionReviewDecide(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	reviewID, version, ok := attentionTarget[ids.WorkReviewID](w, r, "reviewID")
	if !ok {
		return
	}
	var request reviewDecisionRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.DecideWorkReview(routecontext.WithClaims(r.Context(), claims), attentionapp.DecideWorkReviewCommand{Actor: actor, AccountID: accountID, ReviewID: reviewID, Decision: request.Decision, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeAttentionError(w, "decide review", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, reviewView(item))
}

func (s *Server) attentionReviewCancel(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	reviewID, version, ok := attentionTarget[ids.WorkReviewID](w, r, "reviewID")
	if !ok {
		return
	}
	var request reasonRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.CancelWorkReview(routecontext.WithClaims(r.Context(), claims), attentionapp.CancelWorkReviewCommand{Actor: actor, AccountID: accountID, ReviewID: reviewID, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeAttentionError(w, "cancel review", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, reviewView(item))
}

func (s *Server) attentionApprovalList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, false)
	if !ok {
		return
	}
	query, err := parseApprovalQuery(r)
	if err != nil {
		s.writeAttentionError(w, "list approvals", err)
		return
	}
	page, err := s.attention.ListApprovals(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeAttentionError(w, "list approvals", err)
		return
	}
	items := make([]approvalSummaryResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, approvalSummaryView(item))
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeAttentionCursor("approval", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) attentionApprovalCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	var request createApprovalRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	evidence, valid := decodeDigest(request.EvidenceSHA256)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "evidence_sha256 must be a lowercase SHA-256 value")
		return
	}
	item, err := s.attention.CreateApproval(routecontext.WithClaims(r.Context(), claims), attentionapp.CreateApprovalCommand{
		Actor: actor, AccountID: accountID, ApprovalID: ids.ConsequentialApprovalID(operationID), OperationID: request.OperationID,
		InvocationID: request.InvocationID, WorkItemID: request.WorkItemID, Capability: request.Capability, CanonicalPayload: request.Payload,
		EvidenceSHA256: evidence, PolicyVersion: request.PolicyVersion, RequireIndependentReview: request.RequireIndependentReview,
		ExpiresAt: request.ExpiresAt, CorrelationID: operationID,
	})
	if err != nil {
		s.writeAttentionError(w, "create approval", err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/attention/approvals/%s", accountID, item.ID))
	writeAttentionAggregate(w, http.StatusCreated, approvalView(item))
}

func (s *Server) attentionApprovalGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, true)
	if !ok {
		return
	}
	approvalID := ids.ConsequentialApprovalID(r.PathValue("approvalID"))
	if !validAttentionDetail(r, string(approvalID)) {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_query", "the Attention approval is invalid")
		return
	}
	item, err := s.attention.GetApproval(routecontext.WithClaims(r.Context(), claims), actor, accountID, approvalID)
	if err != nil {
		s.writeAttentionError(w, "get approval", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, approvalView(item))
}

func (s *Server) attentionApprovalDecide(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	approvalID, version, ok := attentionTarget[ids.ConsequentialApprovalID](w, r, "approvalID")
	if !ok {
		return
	}
	var request approvalDecisionRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.DecideApproval(routecontext.WithClaims(r.Context(), claims), attentionapp.DecideApprovalCommand{Actor: actor, AccountID: accountID, ApprovalID: approvalID, Decision: request.Decision, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeAttentionError(w, "decide approval", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, approvalView(item))
}

func (s *Server) attentionApprovalCancel(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, operationID, ok := s.attentionCommandRequest(w, r)
	if !ok {
		return
	}
	approvalID, version, ok := attentionTarget[ids.ConsequentialApprovalID](w, r, "approvalID")
	if !ok {
		return
	}
	var request reasonRequest
	if !decodeAttentionJSON(w, r, &request) {
		return
	}
	item, err := s.attention.CancelApproval(routecontext.WithClaims(r.Context(), claims), attentionapp.CancelApprovalCommand{Actor: actor, AccountID: accountID, ApprovalID: approvalID, ExpectedVersion: version, Reason: request.Reason, CorrelationID: operationID})
	if err != nil {
		s.writeAttentionError(w, "cancel approval", err)
		return
	}
	writeAttentionAggregate(w, http.StatusOK, approvalView(item))
}

func (s *Server) attentionRequest(w http.ResponseWriter, r *http.Request, detail bool) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.attention == nil {
		writeProblem(w, http.StatusServiceUnavailable, "attention_unavailable", "Attention is not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if detail && len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_query", "Attention detail queries do not accept query parameters")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	return claims, attentionActor(claims), accountID, true
}

func (s *Server) attentionCommandRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, actor, accountID, ok := s.attentionRequest(w, r, false)
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

func attentionActor(claims routecontext.Claims) access.Actor {
	if claims.Authority.ActorKind == "user" {
		return access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}
	}
	return access.Actor{WorkloadID: claims.Authority.ActorID}
}

func attentionTarget[T ~string](w http.ResponseWriter, r *http.Request, pathName string) (T, uint64, bool) {
	var zero T
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "Attention commands do not accept query parameters")
		return zero, 0, false
	}
	id := T(r.PathValue(pathName))
	if ids.Validate(string(id)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "the Attention target is invalid")
		return zero, 0, false
	}
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "attention_version_required", "If-Match with the current Attention version is required")
		return zero, 0, false
	}
	version, err := parseAttentionVersion(values)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_version", "If-Match must contain exactly one weak Attention version ETag")
		return zero, 0, false
	}
	return id, version, true
}

func parseAttentionVersion(values []string) (uint64, error) {
	if len(values) != 1 {
		return 0, errors.New("missing version")
	}
	raw := strings.TrimSpace(values[0])
	if !strings.HasPrefix(raw, `W/"`) || !strings.HasSuffix(raw, `"`) {
		return 0, errors.New("invalid version")
	}
	version, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(raw, `W/"`), `"`), 10, 64)
	if err != nil || version == 0 {
		return 0, errors.New("invalid version")
	}
	return version, nil
}

func decodeAttentionJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "Attention commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Attention commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "the Attention command body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "the Attention command body is invalid")
		return false
	}
	return true
}

func (request attentionRequirementRequest) domain() attentiondomain.FactRequirement {
	return attentiondomain.FactRequirement{Key: request.Key, Scope: request.Scope, ScopeID: request.ScopeID}
}

func decodeDigest(value string) ([sha256.Size]byte, bool) {
	var digest [sha256.Size]byte
	if value != strings.ToLower(value) {
		return digest, false
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return digest, false
	}
	copy(digest[:], raw)
	return digest, digest != ([sha256.Size]byte{})
}

func validAttentionDetail(r *http.Request, id string) bool {
	return len(r.URL.Query()) == 0 && ids.Validate(id) == nil
}

func writeAttentionAggregate(w http.ResponseWriter, status int, value any) {
	if versioned, ok := value.(interface{ attentionVersion() uint64 }); ok {
		w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, versioned.attentionVersion()))
	}
	writeJSON(w, status, value)
}

type attentionActorResponse struct {
	Kind attentiondomain.ActorKind `json:"kind"`
	ID   string                    `json:"id"`
}

type attentionRequirementResponse struct {
	Key     string                           `json:"key"`
	Scope   attentiondomain.InformationScope `json:"scope"`
	ScopeID string                           `json:"scope_id,omitempty"`
}

type informationAnswerResponse struct {
	FactID      string                 `json:"fact_id"`
	FactVersion uint64                 `json:"fact_version"`
	AnsweredBy  attentionActorResponse `json:"answered_by"`
	AnsweredAt  time.Time              `json:"answered_at"`
}

type informationResponse struct {
	ID               ids.InformationRequestID                `json:"id"`
	ParentWorkItemID ids.WorkItemID                          `json:"parent_work_item_id"`
	Requirement      attentionRequirementResponse            `json:"requirement"`
	Question         string                                  `json:"question"`
	RequestedBy      attentionActorResponse                  `json:"requested_by"`
	State            attentiondomain.InformationRequestState `json:"state"`
	Answer           *informationAnswerResponse              `json:"answer,omitempty"`
	CanceledBy       *attentionActorResponse                 `json:"canceled_by,omitempty"`
	Reason           string                                  `json:"reason,omitempty"`
	Version          uint64                                  `json:"version"`
	CreatedAt        time.Time                               `json:"created_at"`
	UpdatedAt        time.Time                               `json:"updated_at"`
}

func (response informationResponse) attentionVersion() uint64 { return response.Version }
func informationView(item attentiondomain.InformationRequest) informationResponse {
	response := informationResponse{ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, Requirement: requirementView(item.Requirement), Question: item.Question, RequestedBy: actorView(item.RequestedBy), State: item.State, Reason: item.Reason, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.AnswerRecord != nil {
		response.Answer = &informationAnswerResponse{FactID: item.AnswerRecord.Fact.ID, FactVersion: item.AnswerRecord.Fact.Version, AnsweredBy: actorView(item.AnswerRecord.AnsweredBy), AnsweredAt: item.AnswerRecord.AnsweredAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		response.CanceledBy = &value
	}
	return response
}

type informationSummaryResponse struct {
	ID               ids.InformationRequestID                `json:"id"`
	ParentWorkItemID ids.WorkItemID                          `json:"parent_work_item_id"`
	Requirement      attentionRequirementResponse            `json:"requirement"`
	Question         string                                  `json:"question"`
	RequestedBy      attentionActorResponse                  `json:"requested_by"`
	State            attentiondomain.InformationRequestState `json:"state"`
	AnsweredAt       *time.Time                              `json:"answered_at,omitempty"`
	Version          uint64                                  `json:"version"`
	CreatedAt        time.Time                               `json:"created_at"`
	UpdatedAt        time.Time                               `json:"updated_at"`
}

func informationSummaryView(item attentionapp.InformationSummary) informationSummaryResponse {
	return informationSummaryResponse{ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, Requirement: requirementView(item.Requirement), Question: item.Question, RequestedBy: actorView(item.RequestedBy), State: item.State, AnsweredAt: item.AnsweredAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

type reviewDecisionResponse struct {
	Decision  attentiondomain.WorkReviewDecision `json:"decision"`
	Reason    string                             `json:"reason"`
	DecidedBy attentionActorResponse             `json:"decided_by"`
	DecidedAt time.Time                          `json:"decided_at"`
}
type reviewResponse struct {
	ID             ids.WorkReviewID                `json:"id"`
	WorkItemID     ids.WorkItemID                  `json:"work_item_id"`
	WorkVersion    uint64                          `json:"work_version"`
	ProposalSHA256 string                          `json:"proposal_sha256"`
	Question       string                          `json:"question"`
	RequestedBy    attentionActorResponse          `json:"requested_by"`
	ReviewerID     ids.UserID                      `json:"reviewer_id"`
	State          attentiondomain.WorkReviewState `json:"state"`
	Decision       *reviewDecisionResponse         `json:"decision,omitempty"`
	CanceledBy     *attentionActorResponse         `json:"canceled_by,omitempty"`
	CancelReason   string                          `json:"cancel_reason,omitempty"`
	InvalidatedAt  *time.Time                      `json:"invalidated_at,omitempty"`
	Version        uint64                          `json:"version"`
	CreatedAt      time.Time                       `json:"created_at"`
	UpdatedAt      time.Time                       `json:"updated_at"`
}

func (response reviewResponse) attentionVersion() uint64 { return response.Version }
func reviewView(item attentiondomain.WorkReview) reviewResponse {
	response := reviewResponse{ID: item.ID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, ProposalSHA256: hex.EncodeToString(item.ProposalSHA256[:]), Question: item.Question, RequestedBy: actorView(item.RequestedBy), ReviewerID: item.ReviewerID, State: item.State, CancelReason: item.CancelReason, InvalidatedAt: item.InvalidatedAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.Decision != nil {
		response.Decision = &reviewDecisionResponse{Decision: item.Decision.Decision, Reason: item.Decision.Reason, DecidedBy: actorView(item.Decision.DecidedBy), DecidedAt: item.Decision.DecidedAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		response.CanceledBy = &value
	}
	return response
}

type reviewSummaryResponse struct {
	ID          ids.WorkReviewID                   `json:"id"`
	WorkItemID  ids.WorkItemID                     `json:"work_item_id"`
	WorkVersion uint64                             `json:"work_version"`
	Question    string                             `json:"question"`
	RequestedBy attentionActorResponse             `json:"requested_by"`
	ReviewerID  ids.UserID                         `json:"reviewer_id"`
	State       attentiondomain.WorkReviewState    `json:"state"`
	Decision    attentiondomain.WorkReviewDecision `json:"decision,omitempty"`
	Version     uint64                             `json:"version"`
	CreatedAt   time.Time                          `json:"created_at"`
	UpdatedAt   time.Time                          `json:"updated_at"`
}

func reviewSummaryView(item attentionapp.WorkReviewSummary) reviewSummaryResponse {
	return reviewSummaryResponse{ID: item.ID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, Question: item.Question, RequestedBy: actorView(item.RequestedBy), ReviewerID: item.ReviewerID, State: item.State, Decision: item.Decision, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func actorView(actor attentiondomain.Actor) attentionActorResponse {
	return attentionActorResponse{Kind: actor.Kind, ID: actor.ID}
}
func requirementView(requirement attentiondomain.FactRequirement) attentionRequirementResponse {
	return attentionRequirementResponse{Key: requirement.Key, Scope: requirement.Scope, ScopeID: requirement.ScopeID}
}

type approvalResponse struct {
	ID                       ids.ConsequentialApprovalID                `json:"id"`
	WorkItemID               ids.WorkItemID                             `json:"work_item_id,omitempty"`
	OperationID              string                                     `json:"operation_id"`
	InvocationID             ids.AgentInvocationID                      `json:"invocation_id"`
	Capability               string                                     `json:"capability"`
	Payload                  json.RawMessage                            `json:"payload"`
	InputSHA256              string                                     `json:"input_sha256"`
	HashVersion              uint16                                     `json:"hash_version"`
	EvidenceSHA256           string                                     `json:"evidence_sha256"`
	Proposer                 attentionActorResponse                     `json:"proposer"`
	PolicyVersion            uint64                                     `json:"policy_version"`
	RequireIndependentReview bool                                       `json:"require_independent_review"`
	ExpiresAt                time.Time                                  `json:"expires_at"`
	State                    attentiondomain.ConsequentialApprovalState `json:"state"`
	Decision                 *approvalDecisionResponse                  `json:"decision,omitempty"`
	CanceledBy               *attentionActorResponse                    `json:"canceled_by,omitempty"`
	Reason                   string                                     `json:"reason,omitempty"`
	CanceledAt               *time.Time                                 `json:"canceled_at,omitempty"`
	InvalidatedAt            *time.Time                                 `json:"invalidated_at,omitempty"`
	ExpiredAt                *time.Time                                 `json:"expired_at,omitempty"`
	Version                  uint64                                     `json:"version"`
	CreatedAt                time.Time                                  `json:"created_at"`
	UpdatedAt                time.Time                                  `json:"updated_at"`
}

func (response approvalResponse) attentionVersion() uint64 { return response.Version }

type approvalDecisionResponse struct {
	Decision  attentiondomain.ApprovalDecision `json:"decision"`
	Reason    string                           `json:"reason"`
	DecidedBy ids.UserID                       `json:"decided_by"`
	DecidedAt time.Time                        `json:"decided_at"`
}

func approvalView(item attentiondomain.ConsequentialApproval) approvalResponse {
	response := approvalResponse{ID: item.ID, WorkItemID: item.WorkItemID, OperationID: item.OperationID, InvocationID: item.InvocationID, Capability: item.Capability, Payload: item.CanonicalPayload, InputSHA256: hex.EncodeToString(item.InputSHA256[:]), HashVersion: item.HashVersion, EvidenceSHA256: hex.EncodeToString(item.EvidenceSHA256[:]), Proposer: actorView(item.Proposer), PolicyVersion: item.PolicyVersion, RequireIndependentReview: item.RequireIndependentReview, ExpiresAt: item.ExpiresAt, State: item.State, Reason: item.Reason, CanceledAt: item.CanceledAt, InvalidatedAt: item.InvalidatedAt, ExpiredAt: item.ExpiredAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.Decision != nil {
		response.Decision = &approvalDecisionResponse{Decision: item.Decision.Decision, Reason: item.Decision.Reason, DecidedBy: item.Decision.DecidedBy, DecidedAt: item.Decision.DecidedAt}
	}
	if item.CanceledBy != nil {
		value := actorView(*item.CanceledBy)
		response.CanceledBy = &value
	}
	return response
}

type approvalSummaryResponse struct {
	ID            ids.ConsequentialApprovalID                `json:"id"`
	WorkItemID    ids.WorkItemID                             `json:"work_item_id,omitempty"`
	OperationID   string                                     `json:"operation_id"`
	InvocationID  ids.AgentInvocationID                      `json:"invocation_id"`
	Capability    string                                     `json:"capability"`
	Proposer      attentionActorResponse                     `json:"proposer"`
	PolicyVersion uint64                                     `json:"policy_version"`
	ExpiresAt     time.Time                                  `json:"expires_at"`
	State         attentiondomain.ConsequentialApprovalState `json:"state"`
	Decision      attentiondomain.ApprovalDecision           `json:"decision,omitempty"`
	Version       uint64                                     `json:"version"`
	CreatedAt     time.Time                                  `json:"created_at"`
	UpdatedAt     time.Time                                  `json:"updated_at"`
}

func approvalSummaryView(item attentionapp.ApprovalSummary) approvalSummaryResponse {
	return approvalSummaryResponse{ID: item.ID, WorkItemID: item.WorkItemID, OperationID: item.OperationID, InvocationID: item.InvocationID, Capability: item.Capability, Proposer: actorView(item.Proposer), PolicyVersion: item.PolicyVersion, ExpiresAt: item.ExpiresAt, State: item.State, Decision: item.Decision, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func encodeAttentionCursor(kind string, updatedAt time.Time, id string) string {
	raw, _ := json.Marshal(attentionCursorEnvelope{Version: 1, Kind: kind, UpdatedAt: updatedAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeAttentionCursor(raw, kind string) (attentionCursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return attentionCursorEnvelope{}, attentionapp.ErrInvalidCommand
	}
	var cursor attentionCursorEnvelope
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 || cursor.Kind != kind || cursor.UpdatedAt.IsZero() || ids.Validate(cursor.ID) != nil {
		return attentionCursorEnvelope{}, attentionapp.ErrInvalidCommand
	}
	return cursor, nil
}

func parseInformationQuery(r *http.Request) (attentionapp.InformationListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "state", "parent_work_item_id", "cursor", "limit") {
		return attentionapp.InformationListQuery{}, attentionapp.ErrInvalidCommand
	}
	limit, err := parseLimit(r, attentionapp.DefaultLimit)
	if err != nil {
		return attentionapp.InformationListQuery{}, err
	}
	query := attentionapp.InformationListQuery{State: attentiondomain.InformationRequestState(values.Get("state")), ParentWorkItem: ids.WorkItemID(values.Get("parent_work_item_id")), Limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "information")
		if err != nil {
			return query, err
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.InformationRequestID(cursor.ID)
	}
	return query, nil
}
func parseReviewQuery(r *http.Request) (attentionapp.WorkReviewListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "state", "reviewer_id", "work_item_id", "cursor", "limit") {
		return attentionapp.WorkReviewListQuery{}, attentionapp.ErrInvalidCommand
	}
	limit, err := parseLimit(r, attentionapp.DefaultLimit)
	if err != nil {
		return attentionapp.WorkReviewListQuery{}, err
	}
	query := attentionapp.WorkReviewListQuery{State: attentiondomain.WorkReviewState(values.Get("state")), ReviewerID: ids.UserID(values.Get("reviewer_id")), WorkItemID: ids.WorkItemID(values.Get("work_item_id")), Limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "review")
		if err != nil {
			return query, err
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.WorkReviewID(cursor.ID)
	}
	return query, nil
}
func parseApprovalQuery(r *http.Request) (attentionapp.ApprovalListQuery, error) {
	values := r.URL.Query()
	if !allowedAttentionQuery(values, "state", "work_item_id", "cursor", "limit") {
		return attentionapp.ApprovalListQuery{}, attentionapp.ErrInvalidCommand
	}
	limit, err := parseLimit(r, attentionapp.DefaultLimit)
	if err != nil {
		return attentionapp.ApprovalListQuery{}, err
	}
	query := attentionapp.ApprovalListQuery{State: attentiondomain.ConsequentialApprovalState(values.Get("state")), WorkItemID: ids.WorkItemID(values.Get("work_item_id")), Limit: limit}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAttentionCursor(raw, "approval")
		if err != nil {
			return query, err
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, ids.ConsequentialApprovalID(cursor.ID)
	}
	return query, nil
}
func allowedAttentionQuery(values map[string][]string, allowed ...string) bool {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key, entries := range values {
		if !set[key] || len(entries) > 1 {
			return false
		}
	}
	return true
}

func (s *Server) writeAttentionError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, attentionapp.ErrInvalidCommand), errors.Is(err, attentiondomain.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_attention_command", "the Attention command is invalid")
	case errors.Is(err, attentionapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "attention_not_found", "the Attention item was not found")
	case errors.Is(err, attentionapp.ErrConflict), errors.Is(err, attentiondomain.ErrConflict), errors.Is(err, workapp.ErrConflict):
		writeProblem(w, http.StatusPreconditionFailed, "attention_version_conflict", "the Attention item changed; reload it before retrying")
	case errors.Is(err, attentionapp.ErrCompletionTooLarge):
		writeProblem(w, http.StatusConflict, "attention_completion_too_large", "the matching information cohort exceeds the synchronous limit")
	case errors.Is(err, attentionapp.ErrConstraint), errors.Is(err, attentiondomain.ErrState), errors.Is(err, attentiondomain.ErrReasonRequired), errors.Is(err, attentiondomain.ErrRequirementMismatch), errors.Is(err, attentiondomain.ErrReviewer), errors.Is(err, attentiondomain.ErrSelfApproval), errors.Is(err, attentiondomain.ErrExpired), errors.Is(err, workdomain.ErrTransition):
		writeProblem(w, http.StatusUnprocessableEntity, "attention_command_rejected", "the Attention command violates lifecycle or binding rules")
	case errors.Is(err, attentiondomain.ErrRole):
		writeProblem(w, http.StatusForbidden, string(access.DenialRole), "your Account role cannot perform this Attention command")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Attention command")
	default:
		s.logger.Error("Attention operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "attention_unavailable", "the Attention operation could not be completed")
	}
}
