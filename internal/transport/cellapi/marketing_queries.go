package cellapi

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type marketingCursorEnvelope struct {
	Version  int       `json:"v"`
	Kind     string    `json:"kind"`
	Date     time.Time `json:"date,omitempty"`
	ID       string    `json:"id,omitempty"`
	AssetID  string    `json:"asset_id,omitempty"`
	Revision uint64    `json:"revision,omitempty"`
}

type marketingAssetRevisionResponse struct {
	ID               ids.MarketingAssetRevisionID `json:"id"`
	AccountID        ids.AccountID                `json:"account_id"`
	CampaignID       ids.MarketingCampaignID      `json:"campaign_id"`
	AssetID          ids.MarketingAssetID         `json:"asset_id"`
	Revision         uint64                       `json:"revision"`
	Kind             marketingdomain.AssetKind    `json:"kind"`
	Title            string                       `json:"title"`
	MediaType        string                       `json:"media_type"`
	ContentReference string                       `json:"content_reference"`
	ContentSHA256    string                       `json:"content_sha256"`
	ContentBytes     uint64                       `json:"content_bytes"`
	AlternativeText  string                       `json:"alternative_text,omitempty"`
	CreatedBy        marketingdomain.Actor        `json:"created_by"`
	Provenance       marketingdomain.Provenance   `json:"provenance"`
	CreatedAt        time.Time                    `json:"created_at"`
}

func (s *Server) marketingCampaignList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "state", "cursor", "limit") {
		s.writeMarketingError(w, "list campaigns", marketingapp.ErrInvalid)
		return
	}
	limit, err := parseLimit(r, marketingapp.DefaultPageSize)
	if err != nil {
		s.writeMarketingError(w, "list campaigns", marketingapp.ErrInvalid)
		return
	}
	query := marketingapp.CampaignListQuery{State: marketingdomain.CampaignState(r.URL.Query().Get("state")), Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeMarketingCursor(raw, "campaign")
		if err != nil {
			s.writeMarketingError(w, "decode campaign cursor", err)
			return
		}
		query.After = &marketingapp.CampaignCursor{UpdatedAt: cursor.Date, ID: ids.MarketingCampaignID(cursor.ID)}
	}
	page, err := s.marketing.ListCampaigns(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeMarketingError(w, "list campaigns", err)
		return
	}
	items := page.Items
	if items == nil {
		items = []marketingdomain.Campaign{}
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeMarketingCursor(marketingCursorEnvelope{Version: 1, Kind: "campaign", Date: page.NextCursor.UpdatedAt, ID: string(page.NextCursor.ID)})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) marketingCampaignGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCampaignTarget(w, r)
	if !ok || len(r.URL.Query()) != 0 {
		if ok {
			s.writeMarketingError(w, "get campaign", marketingapp.ErrInvalid)
		}
		return
	}
	value, err := s.marketing.GetCampaign(routecontext.WithClaims(r.Context(), claims), actor, accountID, campaignID)
	if err != nil {
		s.writeMarketingError(w, "get campaign", err)
		return
	}
	writeMarketingVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) marketingAssetRevisionList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCampaignTarget(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "asset_id", "cursor", "limit") {
		s.writeMarketingError(w, "list asset revisions", marketingapp.ErrInvalid)
		return
	}
	limit, err := parseLimit(r, marketingapp.DefaultPageSize)
	if err != nil {
		s.writeMarketingError(w, "list asset revisions", marketingapp.ErrInvalid)
		return
	}
	query := marketingapp.AssetRevisionListQuery{CampaignID: campaignID, AssetID: ids.MarketingAssetID(r.URL.Query().Get("asset_id")), Limit: limit}
	if query.AssetID != "" && ids.Validate(string(query.AssetID)) != nil {
		s.writeMarketingError(w, "list asset revisions", marketingapp.ErrInvalid)
		return
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeMarketingCursor(raw, "asset_revision")
		if err != nil {
			s.writeMarketingError(w, "decode asset-revision cursor", err)
			return
		}
		query.After = &marketingapp.AssetRevisionCursor{AssetID: ids.MarketingAssetID(cursor.AssetID), Revision: cursor.Revision}
	}
	page, err := s.marketing.ListAssetRevisions(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeMarketingError(w, "list asset revisions", err)
		return
	}
	items := make([]marketingAssetRevisionResponse, len(page.Items))
	for index, item := range page.Items {
		items[index] = marketingAssetRevisionDTO(item)
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeMarketingCursor(marketingCursorEnvelope{Version: 1, Kind: "asset_revision", AssetID: string(page.NextCursor.AssetID), Revision: page.NextCursor.Revision})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) marketingReleaseList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCampaignTarget(w, r)
	if !ok {
		return
	}
	if !allowedAttentionQuery(r.URL.Query(), "cursor", "limit") {
		s.writeMarketingError(w, "list releases", marketingapp.ErrInvalid)
		return
	}
	limit, err := parseLimit(r, marketingapp.DefaultPageSize)
	if err != nil {
		s.writeMarketingError(w, "list releases", marketingapp.ErrInvalid)
		return
	}
	query := marketingapp.ReleaseListQuery{CampaignID: campaignID, Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeMarketingCursor(raw, "release")
		if err != nil {
			s.writeMarketingError(w, "decode release cursor", err)
			return
		}
		query.After = &marketingapp.ReleaseCursor{CreatedAt: cursor.Date, ID: ids.MarketingReleaseID(cursor.ID)}
	}
	page, err := s.marketing.ListReleases(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeMarketingError(w, "list releases", err)
		return
	}
	items := page.Items
	if items == nil {
		items = []marketingdomain.ReleasePlan{}
	}
	response := map[string]any{"items": items}
	if page.NextCursor != nil {
		response["next_cursor"] = encodeMarketingCursor(marketingCursorEnvelope{Version: 1, Kind: "release", Date: page.NextCursor.CreatedAt, ID: string(page.NextCursor.ID)})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) marketingReleaseGet(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	releaseID := ids.MarketingReleaseID(r.PathValue("releaseID"))
	if len(r.URL.Query()) != 0 || ids.Validate(string(releaseID)) != nil {
		s.writeMarketingError(w, "get release", marketingapp.ErrInvalid)
		return
	}
	value, err := s.marketing.GetRelease(routecontext.WithClaims(r.Context(), claims), actor, accountID, releaseID)
	if err != nil {
		s.writeMarketingError(w, "get release", err)
		return
	}
	writeMarketingVersion(w, value.Version)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) marketingReadRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.marketing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "marketing_unavailable", "Marketing is not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, true
}

func marketingCampaignTarget(w http.ResponseWriter, r *http.Request) (ids.MarketingCampaignID, bool) {
	campaignID := ids.MarketingCampaignID(r.PathValue("campaignID"))
	if ids.Validate(string(campaignID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_query", "the Marketing target is invalid")
		return "", false
	}
	return campaignID, true
}

func encodeMarketingCursor(value marketingCursorEnvelope) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMarketingCursor(raw, kind string) (marketingCursorEnvelope, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return marketingCursorEnvelope{}, marketingapp.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value marketingCursorEnvelope
	if decoder.Decode(&value) != nil || value.Version != 1 || value.Kind != kind {
		return marketingCursorEnvelope{}, marketingapp.ErrInvalid
	}
	switch kind {
	case "campaign", "release":
		if value.Date.IsZero() || ids.Validate(value.ID) != nil || value.AssetID != "" || value.Revision != 0 {
			return marketingCursorEnvelope{}, marketingapp.ErrInvalid
		}
	case "asset_revision":
		if ids.Validate(value.AssetID) != nil || value.Revision == 0 || !value.Date.IsZero() || value.ID != "" {
			return marketingCursorEnvelope{}, marketingapp.ErrInvalid
		}
	default:
		return marketingCursorEnvelope{}, marketingapp.ErrInvalid
	}
	return value, nil
}

func marketingAssetRevisionDTO(value marketingdomain.AssetRevision) marketingAssetRevisionResponse {
	return marketingAssetRevisionResponse{ID: value.ID, AccountID: value.AccountID, CampaignID: value.CampaignID, AssetID: value.AssetID,
		Revision: value.Revision, Kind: value.Kind, Title: value.Title, MediaType: value.MediaType, ContentReference: value.ContentReference,
		ContentSHA256: hex.EncodeToString(value.ContentSHA256[:]), ContentBytes: value.ContentBytes, AlternativeText: value.AlternativeText,
		CreatedBy: value.CreatedBy, Provenance: value.Provenance, CreatedAt: value.CreatedAt}
}

func writeMarketingVersion(w http.ResponseWriter, version uint64) {
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, version))
}

func (s *Server) writeMarketingError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, marketingapp.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_marketing_query", "the Marketing query is invalid")
	case errors.Is(err, marketingapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "marketing_record_not_found", "the Marketing record was not found")
	case errors.Is(err, marketingapp.ErrConflict):
		writeProblem(w, http.StatusConflict, "marketing_operation_conflict", "the operation conflicts with durable Marketing state")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "current Account access does not allow this Marketing operation")
	default:
		s.logger.Error("Marketing operation failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "marketing_unavailable", "the Marketing operation could not be completed")
	}
}

var _ MarketingQueryService = (*marketingapp.Service)(nil)
