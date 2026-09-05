package marketing

import (
	"context"
	"crypto/sha256"
	"io"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AssetContentSource interface {
	OpenContent(context.Context, domain.AssetRevision) (io.ReadCloser, error)
}

// Download authorizes the account before loading metadata or object bytes. It
// verifies the complete immutable content before making any bytes available.
func (service *AssetAdmissionService) Download(ctx context.Context, actor access.Actor, accountID ids.AccountID, campaignID ids.MarketingCampaignID, revisionID ids.MarketingAssetRevisionID) (domain.AssetRevision, []byte, error) {
	if _, err := service.marketing.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.AssetRevision{}, nil, err
	}
	if ids.Validate(string(campaignID)) != nil || ids.Validate(string(revisionID)) != nil {
		return domain.AssetRevision{}, nil, ErrInvalid
	}
	asset, err := service.marketing.store.GetAssetRevision(ctx, accountID, revisionID)
	if err != nil {
		return domain.AssetRevision{}, nil, err
	}
	if asset.AccountID != accountID || asset.CampaignID != campaignID {
		return domain.AssetRevision{}, nil, ErrNotFound
	}
	if _, err := domain.RestoreAssetRevision(asset); err != nil {
		return domain.AssetRevision{}, nil, ErrCorrupt
	}
	source, ok := service.objects.(AssetContentSource)
	if !ok {
		return domain.AssetRevision{}, nil, ErrCorrupt
	}
	reader, err := source.OpenContent(ctx, asset)
	if err != nil {
		return domain.AssetRevision{}, nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, int64(asset.ContentBytes)+1))
	if err != nil {
		return domain.AssetRevision{}, nil, err
	}
	if uint64(len(body)) != asset.ContentBytes || sha256.Sum256(body) != asset.ContentSHA256 {
		return domain.AssetRevision{}, nil, ErrCorrupt
	}
	return asset, body, nil
}
