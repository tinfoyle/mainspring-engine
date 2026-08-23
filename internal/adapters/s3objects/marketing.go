package s3objects

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

func (store *Store) PutMarketingAssetImmutable(ctx context.Context, request marketingapp.AssetObjectWrite) (marketingapp.AssetObjectWriteResult, error) {
	key, err := marketingdomain.AssetObjectKey(request.AccountID, request.CampaignID, request.AssetID, request.RevisionID)
	if err != nil || request.Body == nil || request.Size <= 0 || uint64(request.Size) > marketingdomain.MaximumContentBytes ||
		request.ContentSHA256 == ([sha256.Size]byte{}) || normalizeMediaType(request.MediaType) == "" {
		return marketingapp.AssetObjectWriteResult{}, marketingapp.ErrInvalid
	}
	result, err := store.putImmutable(ctx, key, request.AccountID, "marketing-asset", normalizeMediaType(request.MediaType), request.Size, request.ContentSHA256, request.Body)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return marketingapp.AssetObjectWriteResult{}, errors.Join(marketingapp.ErrConflict, err)
		}
		if errors.Is(err, knowledgeapp.ErrInvalid) {
			return marketingapp.AssetObjectWriteResult{}, errors.Join(marketingapp.ErrInvalid, err)
		}
		return marketingapp.AssetObjectWriteResult{}, err
	}
	reference, err := marketingdomain.ContentReferenceForObjectVersion(result.Identity.Version)
	if err != nil {
		if result.Created {
			store.cleanup(ctx, result.Identity)
		}
		return marketingapp.AssetObjectWriteResult{}, ErrIntegrity
	}
	return marketingapp.AssetObjectWriteResult{Identity: marketingapp.AssetObjectIdentity{Key: result.Identity.Key, Version: result.Identity.Version,
		Reference: reference, Size: result.Identity.Size, ContentSHA256: result.Identity.ContentSHA256}, Created: result.Created}, nil
}

func (store *Store) OpenContent(ctx context.Context, candidate marketingdomain.AssetRevision) (io.ReadCloser, error) {
	asset, err := marketingdomain.RestoreAssetRevision(candidate)
	if err != nil {
		return nil, integrationexecution.ErrInvalid
	}
	version, err := marketingdomain.ObjectVersionFromContentReference(asset.ContentReference)
	if err != nil {
		return nil, integrationexecution.ErrInvalid
	}
	key, err := marketingdomain.AssetObjectKey(asset.AccountID, asset.CampaignID, asset.AssetID, asset.ID)
	if err != nil {
		return nil, integrationexecution.ErrInvalid
	}
	options := minio.GetObjectOptions{VersionID: version, ServerSideEncryption: store.sse, Checksum: true}
	info, err := store.client.StatObject(ctx, store.bucket, key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: stat Marketing asset: %v", ErrUnavailable, err)
	}
	if info.VersionID != version || !matches(info, int64(asset.ContentBytes), asset.ContentSHA256) ||
		metadata(info, "spyglass-account-id") != string(asset.AccountID) || metadata(info, "spyglass-object-kind") != "marketing-asset" {
		return nil, ErrIntegrity
	}
	object, err := store.client.GetObject(ctx, store.bucket, key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: get Marketing asset: %v", ErrUnavailable, err)
	}
	return object, nil
}

func (store *Store) DeleteMarketingAsset(ctx context.Context, identity marketingapp.AssetObjectIdentity) error {
	version, err := marketingdomain.ObjectVersionFromContentReference(identity.Reference)
	if err != nil || version != identity.Version || identity.Key == "" || !strings.HasPrefix(identity.Key, "accounts/") ||
		!strings.Contains(identity.Key, "/marketing/campaigns/") || identity.Size <= 0 || uint64(identity.Size) > marketingdomain.MaximumContentBytes ||
		identity.ContentSHA256 == ([sha256.Size]byte{}) {
		return marketingapp.ErrInvalid
	}
	if err := store.client.RemoveObject(ctx, store.bucket, identity.Key, minio.RemoveObjectOptions{VersionID: identity.Version}); err != nil {
		return fmt.Errorf("%w: delete Marketing asset version: %v", ErrUnavailable, err)
	}
	return nil
}

func metadata(info minio.ObjectInfo, key string) string {
	value := info.Metadata.Get("X-Amz-Meta-" + key)
	if value != "" {
		return value
	}
	for candidate, current := range info.UserMetadata {
		if strings.EqualFold(candidate, key) {
			return current
		}
	}
	return ""
}

var _ marketingapp.AssetObjectStore = (*Store)(nil)
var _ integrationexecution.ContentSource = (*Store)(nil)
