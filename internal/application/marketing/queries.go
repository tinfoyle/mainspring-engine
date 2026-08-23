package marketing

import (
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultPageSize = 50
	MaximumPageSize = 100
)

type CampaignCursor struct {
	UpdatedAt time.Time
	ID        ids.MarketingCampaignID
}

type CampaignListQuery struct {
	State domain.CampaignState
	After *CampaignCursor
	Limit int
}

func (query CampaignListQuery) normalized() (CampaignListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > MaximumPageSize || (query.State != "" && query.State != domain.CampaignDraft && query.State != domain.CampaignActive &&
		query.State != domain.CampaignPaused && query.State != domain.CampaignCompleted && query.State != domain.CampaignArchived) {
		return CampaignListQuery{}, ErrInvalid
	}
	if query.After != nil && (query.After.UpdatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil) {
		return CampaignListQuery{}, ErrInvalid
	}
	return query, nil
}

type CampaignPage struct {
	Items      []domain.Campaign
	NextCursor *CampaignCursor
}

type ReleaseCursor struct {
	CreatedAt time.Time
	ID        ids.MarketingReleaseID
}

type ReleaseListQuery struct {
	CampaignID ids.MarketingCampaignID
	After      *ReleaseCursor
	Limit      int
}

func (query ReleaseListQuery) normalized() (ReleaseListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > MaximumPageSize || ids.Validate(string(query.CampaignID)) != nil {
		return ReleaseListQuery{}, ErrInvalid
	}
	if query.After != nil && (query.After.CreatedAt.IsZero() || ids.Validate(string(query.After.ID)) != nil) {
		return ReleaseListQuery{}, ErrInvalid
	}
	return query, nil
}

type ReleasePage struct {
	Items      []domain.ReleasePlan
	NextCursor *ReleaseCursor
}

type AssetRevisionCursor struct {
	AssetID  ids.MarketingAssetID
	Revision uint64
}

type AssetRevisionListQuery struct {
	CampaignID ids.MarketingCampaignID
	AssetID    ids.MarketingAssetID
	After      *AssetRevisionCursor
	Limit      int
}

func (query AssetRevisionListQuery) normalized() (AssetRevisionListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultPageSize
	}
	if query.Limit < 1 || query.Limit > MaximumPageSize || ids.Validate(string(query.CampaignID)) != nil ||
		(query.AssetID != "" && ids.Validate(string(query.AssetID)) != nil) {
		return AssetRevisionListQuery{}, ErrInvalid
	}
	if query.After != nil && (ids.Validate(string(query.After.AssetID)) != nil || query.After.Revision == 0 || (query.AssetID != "" && query.After.AssetID != query.AssetID)) {
		return AssetRevisionListQuery{}, ErrInvalid
	}
	return query, nil
}

type AssetRevisionPage struct {
	Items      []domain.AssetRevision
	NextCursor *AssetRevisionCursor
}
