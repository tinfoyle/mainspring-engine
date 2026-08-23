package marketing

import (
	"context"
	"crypto/sha256"
	"io"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AssetSource interface {
	io.Reader
	io.Seeker
}

type AssetObjectWrite struct {
	AccountID     ids.AccountID
	CampaignID    ids.MarketingCampaignID
	AssetID       ids.MarketingAssetID
	RevisionID    ids.MarketingAssetRevisionID
	MediaType     string
	Size          int64
	ContentSHA256 [sha256.Size]byte
	Body          io.Reader
}

type AssetObjectIdentity struct {
	Key           string
	Version       string
	Reference     string
	Size          int64
	ContentSHA256 [sha256.Size]byte
}

type AssetObjectWriteResult struct {
	Identity AssetObjectIdentity
	Created  bool
}

type AssetObjectStore interface {
	Verify(context.Context) error
	PutMarketingAssetImmutable(context.Context, AssetObjectWrite) (AssetObjectWriteResult, error)
	DeleteMarketingAsset(context.Context, AssetObjectIdentity) error
}
