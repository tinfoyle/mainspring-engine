package cellapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type marketingCampaignDefinitionRequest struct {
	Name      string                    `json:"name"`
	Objective string                    `json:"objective"`
	Audience  string                    `json:"audience"`
	Channels  []marketingdomain.Channel `json:"channels"`
}

const (
	maximumMarketingAssetUploadEnvelope = int64(marketingdomain.MaximumContentBytes) + int64(1<<20)
	maximumAgentMarketingCopyBytes      = 64 << 10
)

type marketingReleaseDefinitionRequest struct {
	CampaignVersion  uint64                         `json:"campaign_version"`
	Name             string                         `json:"name"`
	Channels         []marketingdomain.Channel      `json:"channels"`
	AssetRevisionIDs []ids.MarketingAssetRevisionID `json:"asset_revision_ids"`
}

type marketingReleaseSubmissionRequest struct {
	CampaignVersion uint64 `json:"campaign_version"`
}

type marketingReleaseApprovalRequest struct {
	ApprovalID ids.ConsequentialApprovalID `json:"approval_id"`
}

type marketingCampaignActivationRequest struct {
	ReleaseID ids.MarketingReleaseID `json:"release_id"`
}

type marketingAgentCampaignDefinitionRequest struct {
	RunID     ids.RunID                 `json:"run_id"`
	Name      string                    `json:"name"`
	Objective string                    `json:"objective"`
	Audience  string                    `json:"audience"`
	Channels  []marketingdomain.Channel `json:"channels"`
}

type marketingAgentAssetRevisionDefinitionRequest struct {
	RunID   ids.RunID            `json:"run_id"`
	AssetID ids.MarketingAssetID `json:"asset_id"`
	Title   string               `json:"title"`
	Content string               `json:"content"`
}

type marketingAgentReleaseDefinitionRequest struct {
	RunID            ids.RunID                      `json:"run_id"`
	CampaignVersion  uint64                         `json:"campaign_version"`
	Name             string                         `json:"name"`
	Channels         []marketingdomain.Channel      `json:"channels"`
	AssetRevisionIDs []ids.MarketingAssetRevisionID `json:"asset_revision_ids"`
}

func (s *Server) marketingCampaignCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return
	}
	var body marketingCampaignDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, created, err := s.marketingCommands.CreateCampaign(routecontext.WithClaims(r.Context(), claims), marketingapp.CreateCampaignCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, Name: body.Name, Objective: body.Objective, Audience: body.Audience, Channels: body.Channels,
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
	})
	if err != nil {
		s.writeMarketingError(w, "create campaign", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/marketing/campaigns/%s", accountID, value.ID))
	writeMarketingVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) marketingAgentCampaignDraft(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, invocationID, ok := s.marketingAgentDraftRequest(w, r)
	if !ok {
		return
	}
	var body marketingAgentCampaignDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, created, err := s.marketingCommands.CreateCampaign(routecontext.WithClaims(r.Context(), claims), marketingapp.CreateCampaignCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, Name: body.Name, Objective: body.Objective, Audience: body.Audience,
		Channels: body.Channels, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginAgent, RunID: body.RunID, InvocationID: invocationID},
	})
	if err != nil {
		s.writeMarketingError(w, "create Agent campaign draft", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/marketing/campaigns/%s", accountID, value.ID))
	writeMarketingVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) marketingCampaignRevise(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return
	}
	campaignID, version, ok := marketingVersionTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok {
		return
	}
	var body marketingCampaignDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, err := s.marketingCommands.ReviseCampaign(routecontext.WithClaims(r.Context(), claims), marketingapp.ReviseCampaignCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, ExpectedVersion: version,
		Name: body.Name, Objective: body.Objective, Audience: body.Audience, Channels: body.Channels,
	})
	s.writeMarketingAggregate(w, "revise campaign", value, err)
}

func (s *Server) marketingCampaignArchive(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return
	}
	campaignID, version, ok := marketingVersionTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok || !marketingNoBody(w, r) {
		return
	}
	value, err := s.marketingCommands.ArchiveCampaign(routecontext.WithClaims(r.Context(), claims), marketingapp.CampaignTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, ExpectedVersion: version,
	})
	s.writeMarketingAggregate(w, "archive campaign", value, err)
}

func (s *Server) marketingAssetRevisionCreate(w http.ResponseWriter, r *http.Request) {
	claims, captured, ok := s.acceptCaptured(w, r, maximumMarketingAssetUploadEnvelope)
	if !ok {
		return
	}
	defer captured.Close()
	claims, actor, accountID, requestID, ok := s.marketingCommandClaims(w, r, claims)
	if !ok {
		return
	}
	campaignID, ok := marketingCommandTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok {
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data;") {
		writeProblem(w, http.StatusUnsupportedMediaType, "multipart_required", "Marketing asset upload requires multipart/form-data")
		return
	}
	body, err := captured.Open()
	if err != nil {
		s.writeMarketingError(w, "open captured Marketing asset", err)
		return
	}
	defer body.Close()
	r.Body = body
	if err := r.ParseMultipartForm(64 << 10); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "the Marketing asset upload is invalid")
		return
	}
	defer r.MultipartForm.RemoveAll()
	fileHeader, fields, ok := marketingAssetMultipart(w, r.MultipartForm)
	if !ok {
		return
	}
	assetID := ids.MarketingAssetID(fields["asset_id"])
	if ids.Validate(string(assetID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "asset_id must be a UUID")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "the Marketing asset file could not be read")
		return
	}
	defer file.Close()
	value, created, err := s.marketingCommands.UploadAssetRevision(routecontext.WithClaims(r.Context(), claims), marketingapp.UploadAssetRevisionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, AssetID: assetID,
		Kind: marketingdomain.AssetKind(fields["kind"]), Title: fields["title"], MediaType: fields["media_type"],
		AlternativeText: fields["alternative_text"], Body: file, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
	})
	if err != nil {
		s.writeMarketingError(w, "create asset revision", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, marketingAssetRevisionDTO(value))
}

func marketingAssetMultipart(w http.ResponseWriter, form *multipart.Form) (*multipart.FileHeader, map[string]string, bool) {
	if form == nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "the Marketing asset upload is invalid")
		return nil, nil, false
	}
	allowed := map[string]bool{"asset_id": true, "kind": true, "title": true, "media_type": true, "alternative_text": false}
	fields := make(map[string]string, len(allowed))
	for name, values := range form.Value {
		required, exists := allowed[name]
		if !exists || len(values) != 1 || required && strings.TrimSpace(values[0]) == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "Marketing asset metadata fields are invalid")
			return nil, nil, false
		}
		fields[name] = values[0]
	}
	for name, required := range allowed {
		if required && strings.TrimSpace(fields[name]) == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "asset_id, kind, title and media_type are required")
			return nil, nil, false
		}
	}
	if len(form.File) != 1 || len(form.File["file"]) != 1 || form.File["file"][0].Filename == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_asset_upload", "exactly one Marketing asset file is required")
		return nil, nil, false
	}
	return form.File["file"][0], fields, true
}

func (s *Server) marketingAgentAssetRevisionDraft(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, invocationID, ok := s.marketingAgentDraftRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCommandTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok {
		return
	}
	var body marketingAgentAssetRevisionDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	if !validAgentMarketingCopy(body.Content) {
		s.writeMarketingError(w, "create Agent asset revision", marketingapp.ErrInvalid)
		return
	}
	value, created, err := s.marketingCommands.UploadAssetRevision(routecontext.WithClaims(r.Context(), claims), marketingapp.UploadAssetRevisionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, AssetID: body.AssetID,
		Kind: marketingdomain.AssetCopy, Title: body.Title, MediaType: "text/plain", Body: bytes.NewReader([]byte(body.Content)),
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginAgent, RunID: body.RunID, InvocationID: invocationID},
	})
	if err != nil {
		s.writeMarketingError(w, "create Agent asset revision", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, marketingAssetRevisionDTO(value))
}

func (s *Server) marketingReleaseCreate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCommandTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok {
		return
	}
	var body marketingReleaseDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, created, err := s.marketingCommands.CreateRelease(routecontext.WithClaims(r.Context(), claims), marketingapp.CreateReleaseCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, CampaignVersion: body.CampaignVersion,
		Name: body.Name, Channels: body.Channels, AssetRevisionIDs: body.AssetRevisionIDs, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
	})
	if err != nil {
		s.writeMarketingError(w, "create release", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/marketing/releases/%s", accountID, value.ID))
	writeMarketingVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) marketingAgentReleaseDraft(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, invocationID, ok := s.marketingAgentDraftRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCommandTarget[ids.MarketingCampaignID](w, r, "campaignID")
	if !ok {
		return
	}
	var body marketingAgentReleaseDefinitionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, created, err := s.marketingCommands.CreateRelease(routecontext.WithClaims(r.Context(), claims), marketingapp.CreateReleaseCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, CampaignVersion: body.CampaignVersion,
		Name: body.Name, Channels: body.Channels, AssetRevisionIDs: body.AssetRevisionIDs,
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginAgent, RunID: body.RunID, InvocationID: invocationID},
	})
	if err != nil {
		s.writeMarketingError(w, "create Agent release draft", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/accounts/%s/marketing/releases/%s", accountID, value.ID))
	writeMarketingVersion(w, value.Version)
	writeJSON(w, status, value)
}

func (s *Server) marketingReleaseSubmit(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, releaseID, version, ok := s.marketingReleaseTransitionRequest(w, r)
	if !ok {
		return
	}
	var body marketingReleaseSubmissionRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, err := s.marketingCommands.SubmitRelease(routecontext.WithClaims(r.Context(), claims), marketingapp.ReleaseTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ReleaseID: releaseID, ExpectedVersion: version, CampaignVersion: body.CampaignVersion,
	})
	s.writeMarketingAggregate(w, "submit release", value, err)
}

func (s *Server) marketingReleaseApprove(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, releaseID, version, ok := s.marketingReleaseTransitionRequest(w, r)
	if !ok {
		return
	}
	var body marketingReleaseApprovalRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, err := s.marketingCommands.ApproveRelease(routecontext.WithClaims(r.Context(), claims), marketingapp.ReleaseTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ReleaseID: releaseID, ExpectedVersion: version, ApprovalID: body.ApprovalID,
	})
	s.writeMarketingAggregate(w, "approve release", value, err)
}

func (s *Server) marketingReleaseCancel(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, releaseID, version, ok := s.marketingReleaseTransitionRequest(w, r)
	if !ok || !marketingNoBody(w, r) {
		return
	}
	value, err := s.marketingCommands.CancelRelease(routecontext.WithClaims(r.Context(), claims), marketingapp.ReleaseTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, ReleaseID: releaseID, ExpectedVersion: version,
	})
	s.writeMarketingAggregate(w, "cancel release", value, err)
}

func (s *Server) marketingCampaignActivate(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, requestID, campaignID, version, ok := s.marketingCampaignTransitionRequest(w, r)
	if !ok {
		return
	}
	var body marketingCampaignActivationRequest
	if !decodeMarketingJSON(w, r, &body) {
		return
	}
	value, err := s.marketingCommands.ActivateCampaign(routecontext.WithClaims(r.Context(), claims), marketingapp.CampaignTransitionCommand{
		Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, ReleaseID: body.ReleaseID, ExpectedVersion: version,
	})
	s.writeMarketingAggregate(w, "activate campaign", value, err)
}

func (s *Server) marketingCampaignPause(w http.ResponseWriter, r *http.Request) {
	s.marketingCampaignEmptyTransition(w, r, "pause")
}

func (s *Server) marketingCampaignComplete(w http.ResponseWriter, r *http.Request) {
	s.marketingCampaignEmptyTransition(w, r, "complete")
}

func (s *Server) marketingCampaignEmptyTransition(w http.ResponseWriter, r *http.Request, kind string) {
	claims, actor, accountID, requestID, campaignID, version, ok := s.marketingCampaignTransitionRequest(w, r)
	if !ok || !marketingNoBody(w, r) {
		return
	}
	command := marketingapp.CampaignTransitionCommand{Actor: actor, AccountID: accountID, RequestID: requestID, CampaignID: campaignID, ExpectedVersion: version}
	var value marketingdomain.Campaign
	var err error
	if kind == "pause" {
		value, err = s.marketingCommands.PauseCampaign(routecontext.WithClaims(r.Context(), claims), command)
	} else {
		value, err = s.marketingCommands.CompleteCampaign(routecontext.WithClaims(r.Context(), claims), command)
	}
	s.writeMarketingAggregate(w, kind+" campaign", value, err)
}

func (s *Server) marketingCommandRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", false
	}
	return s.marketingCommandClaims(w, r, claims)
}

func (s *Server) marketingCommandClaims(w http.ResponseWriter, r *http.Request, claims routecontext.Claims) (routecontext.Claims, access.Actor, ids.AccountID, string, bool) {
	if s.marketingCommands == nil {
		writeProblem(w, http.StatusServiceUnavailable, "marketing_unavailable", "Marketing commands are not available in this cell")
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

func (s *Server) marketingAgentDraftRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, ids.AgentInvocationID, bool) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", "", false
	}
	invocationRaw := strings.TrimPrefix(actor.WorkloadID, "runner-invocation:")
	if claims.Authority.ActorKind != "workload" || actor.UserID != "" || invocationRaw == actor.WorkloadID || ids.Validate(invocationRaw) != nil {
		writeProblem(w, http.StatusForbidden, "marketing_agent_draft_denied", "the Marketing Agent draft boundary is unavailable")
		return routecontext.Claims{}, access.Actor{}, "", "", "", false
	}
	return claims, actor, accountID, requestID, ids.AgentInvocationID(invocationRaw), true
}

func validAgentMarketingCopy(value string) bool {
	return value != "" && len(value) <= maximumAgentMarketingCopyBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, func(current rune) bool { return current == 0 || current == 0x7f }) == -1
}

func (s *Server) marketingReleaseTransitionRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, ids.MarketingReleaseID, uint64, bool) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	releaseID, version, ok := marketingVersionTarget[ids.MarketingReleaseID](w, r, "releaseID")
	return claims, actor, accountID, requestID, releaseID, version, ok
}

func (s *Server) marketingCampaignTransitionRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, string, ids.MarketingCampaignID, uint64, bool) {
	claims, actor, accountID, requestID, ok := s.marketingCommandRequest(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", "", "", 0, false
	}
	campaignID, version, ok := marketingVersionTarget[ids.MarketingCampaignID](w, r, "campaignID")
	return claims, actor, accountID, requestID, campaignID, version, ok
}

func marketingCommandTarget[T ~string](w http.ResponseWriter, r *http.Request, pathName string) (T, bool) {
	var zero T
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "Marketing commands do not accept query parameters")
		return zero, false
	}
	value := T(r.PathValue(pathName))
	if ids.Validate(string(value)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "the Marketing target is invalid")
		return zero, false
	}
	return value, true
}

func marketingVersionTarget[T ~string](w http.ResponseWriter, r *http.Request, pathName string) (T, uint64, bool) {
	value, ok := marketingCommandTarget[T](w, r, pathName)
	if !ok {
		return value, 0, false
	}
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		writeProblem(w, http.StatusPreconditionRequired, "marketing_version_required", "If-Match with the current Marketing version is required")
		return value, 0, false
	}
	version, err := parseAttentionVersion(values)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_version", "If-Match must contain exactly one weak Marketing version ETag")
		return value, 0, false
	}
	return value, version, true
}

func decodeMarketingJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "Marketing commands do not accept query parameters")
		return false
	}
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "Marketing commands require application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "the Marketing command body is invalid")
		return false
	}
	return true
}

func marketingNoBody(w http.ResponseWriter, r *http.Request) bool {
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "Marketing commands do not accept query parameters")
		return false
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(strings.TrimSpace(string(raw))) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_command", "this Marketing command does not accept a body")
		return false
	}
	return true
}

func (s *Server) writeMarketingAggregate(w http.ResponseWriter, operation string, value any, err error) {
	if err != nil {
		s.writeMarketingError(w, operation, err)
		return
	}
	switch aggregate := value.(type) {
	case marketingdomain.Campaign:
		writeMarketingVersion(w, aggregate.Version)
	case marketingdomain.ReleasePlan:
		writeMarketingVersion(w, aggregate.Version)
	}
	writeJSON(w, http.StatusOK, value)
}
