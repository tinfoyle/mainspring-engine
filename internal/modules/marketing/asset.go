package marketing

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AssetKind string

const (
	AssetCopy     AssetKind = "copy"
	AssetImage    AssetKind = "image"
	AssetDocument AssetKind = "document"
)

type AssetRevision struct {
	ID               ids.MarketingAssetRevisionID `json:"id"`
	AccountID        ids.AccountID                `json:"account_id"`
	CampaignID       ids.MarketingCampaignID      `json:"campaign_id"`
	AssetID          ids.MarketingAssetID         `json:"asset_id"`
	Revision         uint64                       `json:"revision"`
	Kind             AssetKind                    `json:"kind"`
	Title            string                       `json:"title"`
	MediaType        string                       `json:"media_type"`
	ContentReference string                       `json:"content_reference"`
	ContentSHA256    [32]byte                     `json:"content_sha256"`
	ContentBytes     uint64                       `json:"content_bytes"`
	AlternativeText  string                       `json:"alternative_text,omitempty"`
	CreatedBy        Actor                        `json:"created_by"`
	Provenance       Provenance                   `json:"provenance"`
	CreatedAt        time.Time                    `json:"created_at"`
}

type AssetRevisionInput struct {
	ID               ids.MarketingAssetRevisionID
	AccountID        ids.AccountID
	CampaignID       ids.MarketingCampaignID
	AssetID          ids.MarketingAssetID
	Kind             AssetKind
	Title            string
	MediaType        string
	ContentReference string
	ContentSHA256    [32]byte
	ContentBytes     uint64
	AlternativeText  string
	CreatedBy        Actor
	Provenance       Provenance
	CreatedAt        time.Time
}

func NewAssetRevision(input AssetRevisionInput, previous *AssetRevision, role accounts.MembershipRole) (AssetRevision, error) {
	if !canDraft(role) || !input.CreatedBy.valid() || !input.Provenance.valid(input.CreatedBy) {
		return AssetRevision{}, ErrRole
	}
	revision := uint64(1)
	if previous != nil {
		if _, err := RestoreAssetRevision(*previous); err != nil || previous.AccountID != input.AccountID || previous.CampaignID != input.CampaignID || previous.AssetID != input.AssetID || !input.CreatedAt.UTC().After(previous.CreatedAt) {
			return AssetRevision{}, ErrInvalid
		}
		revision = previous.Revision + 1
		if revision == 0 {
			return AssetRevision{}, ErrInvalid
		}
	}
	value := AssetRevision{ID: input.ID, AccountID: input.AccountID, CampaignID: input.CampaignID, AssetID: input.AssetID, Revision: revision,
		Kind: input.Kind, Title: strings.TrimSpace(input.Title), MediaType: strings.ToLower(strings.TrimSpace(input.MediaType)),
		ContentReference: strings.TrimSpace(input.ContentReference), ContentSHA256: input.ContentSHA256, ContentBytes: input.ContentBytes,
		AlternativeText: strings.TrimSpace(input.AlternativeText), CreatedBy: input.CreatedBy, Provenance: input.Provenance, CreatedAt: input.CreatedAt.UTC()}
	return RestoreAssetRevision(value)
}

func RestoreAssetRevision(value AssetRevision) (AssetRevision, error) {
	value.Title, value.MediaType, value.ContentReference, value.AlternativeText = strings.TrimSpace(value.Title), strings.ToLower(strings.TrimSpace(value.MediaType)), strings.TrimSpace(value.ContentReference), strings.TrimSpace(value.AlternativeText)
	value.CreatedAt = value.CreatedAt.UTC()
	validKind := value.Kind == AssetCopy || value.Kind == AssetImage || value.Kind == AssetDocument
	validMediaType := validText(value.MediaType, MaximumMediaTypeBytes, true) && strings.Count(value.MediaType, "/") == 1 && !strings.ContainsAny(value.MediaType, " \t\r\n")
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.CampaignID)) != nil || ids.Validate(string(value.AssetID)) != nil ||
		value.Revision == 0 || !validKind || !validText(value.Title, MaximumAssetTitleBytes, true) || !validMediaType ||
		!validText(value.ContentReference, MaximumContentReferenceBytes, true) || value.ContentSHA256 == ([32]byte{}) || value.ContentBytes == 0 ||
		!validText(value.AlternativeText, MaximumAlternativeTextBytes, false) || (value.Kind == AssetImage && value.AlternativeText == "") ||
		!value.CreatedBy.valid() || !value.Provenance.valid(value.CreatedBy) || value.CreatedAt.IsZero() {
		return AssetRevision{}, ErrInvalid
	}
	return value, nil
}
