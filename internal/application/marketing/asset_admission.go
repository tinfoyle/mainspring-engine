package marketing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AssetAdmissionService struct {
	marketing *Service
	objects   AssetObjectStore
}

func NewAssetAdmissionService(marketing *Service, objects AssetObjectStore) (*AssetAdmissionService, error) {
	if marketing == nil || objects == nil {
		return nil, errors.New("Marketing asset admission dependencies are required")
	}
	return &AssetAdmissionService{marketing: marketing, objects: objects}, nil
}

type UploadAssetRevisionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	CampaignID      ids.MarketingCampaignID
	AssetID         ids.MarketingAssetID
	Kind            domain.AssetKind
	Title           string
	MediaType       string
	AlternativeText string
	Body            AssetSource
	Provenance      domain.Provenance
}

func (service *AssetAdmissionService) Upload(ctx context.Context, command UploadAssetRevisionCommand) (domain.AssetRevision, bool, error) {
	if ids.Validate(command.RequestID) != nil || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.CampaignID)) != nil ||
		ids.Validate(string(command.AssetID)) != nil || command.Body == nil {
		return domain.AssetRevision{}, false, ErrInvalid
	}
	_, err := service.marketing.authorizeDraft(ctx, command.Actor, command.AccountID)
	if err != nil {
		return domain.AssetRevision{}, false, err
	}
	actor, provenance, err := draftActor(command.Actor, command.Provenance)
	if err != nil {
		return domain.AssetRevision{}, false, err
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(command.MediaType, ";", 2)[0]))
	size, digest, err := verifyAssetSource(command.Body)
	if err != nil {
		return domain.AssetRevision{}, false, err
	}
	now := service.marketing.clock.Now().UTC()
	preflight := domain.AssetRevision{ID: ids.MarketingAssetRevisionID(command.RequestID), AccountID: command.AccountID, CampaignID: command.CampaignID,
		AssetID: command.AssetID, Revision: 1, Kind: command.Kind, Title: command.Title, MediaType: mediaType, ContentReference: "preflight",
		ContentSHA256: digest, ContentBytes: uint64(size), AlternativeText: command.AlternativeText, CreatedBy: actor, Provenance: provenance, CreatedAt: now}
	if _, err := domain.RestoreAssetRevision(preflight); err != nil {
		return domain.AssetRevision{}, false, ErrInvalid
	}
	if _, err := command.Body.Seek(0, io.SeekStart); err != nil {
		return domain.AssetRevision{}, false, ErrInvalid
	}
	write, err := service.objects.PutMarketingAssetImmutable(ctx, AssetObjectWrite{AccountID: command.AccountID, CampaignID: command.CampaignID,
		AssetID: command.AssetID, RevisionID: ids.MarketingAssetRevisionID(command.RequestID), MediaType: mediaType, Size: size,
		ContentSHA256: digest, Body: command.Body})
	if err != nil {
		return domain.AssetRevision{}, false, err
	}
	expectedKey, keyErr := domain.AssetObjectKey(command.AccountID, command.CampaignID, command.AssetID, ids.MarketingAssetRevisionID(command.RequestID))
	version, referenceErr := domain.ObjectVersionFromContentReference(write.Identity.Reference)
	if keyErr != nil || referenceErr != nil || write.Identity.Key != expectedKey || write.Identity.Version != version ||
		write.Identity.Size != size || write.Identity.ContentSHA256 != digest {
		if write.Created {
			if cleanupErr := service.objects.DeleteMarketingAsset(ctx, write.Identity); cleanupErr != nil {
				return domain.AssetRevision{}, false, errors.Join(ErrInvalid, cleanupErr)
			}
		}
		return domain.AssetRevision{}, false, ErrInvalid
	}
	value, created, err := service.marketing.createAssetRevision(ctx, createAssetRevisionCommand{Actor: command.Actor, AccountID: command.AccountID,
		RequestID: command.RequestID, CampaignID: command.CampaignID, AssetID: command.AssetID, Kind: command.Kind, Title: command.Title,
		MediaType: mediaType, ContentReference: write.Identity.Reference, ContentSHA256: write.Identity.ContentSHA256,
		ContentBytes: uint64(write.Identity.Size), AlternativeText: command.AlternativeText, Provenance: provenance})
	if err == nil || !write.Created {
		return value, created, err
	}
	if cleanupErr := service.objects.DeleteMarketingAsset(ctx, write.Identity); cleanupErr != nil {
		return domain.AssetRevision{}, false, fmt.Errorf("%w: asset-object cleanup failed: %v", err, cleanupErr)
	}
	return domain.AssetRevision{}, false, err
}

func verifyAssetSource(source AssetSource) (int64, [sha256.Size]byte, error) {
	if source == nil {
		return 0, [sha256.Size]byte{}, ErrInvalid
	}
	size, err := source.Seek(0, io.SeekEnd)
	if err != nil || size <= 0 || uint64(size) > domain.MaximumContentBytes {
		return 0, [sha256.Size]byte{}, ErrInvalid
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return 0, [sha256.Size]byte{}, ErrInvalid
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(source, int64(domain.MaximumContentBytes)+1))
	if err != nil || written != size {
		return 0, [sha256.Size]byte{}, ErrInvalid
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return size, digest, nil
}
