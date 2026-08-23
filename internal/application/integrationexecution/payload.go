package integrationexecution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sort"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

type DeliverySnapshot struct {
	ConnectorKind domain.ConnectorKind
	Revision      domain.ConnectionRevision
	Assets        []marketingdomain.AssetRevision
}

type DeliverySource interface {
	LoadDelivery(context.Context, Claim) (DeliverySnapshot, error)
}

type ContentSource interface {
	OpenContent(context.Context, marketingdomain.AssetRevision) (io.ReadCloser, error)
}

type PayloadAssembler struct {
	deliveries DeliverySource
	contents   ContentSource
}

func NewPayloadAssembler(deliveries DeliverySource, contents ContentSource) (*PayloadAssembler, error) {
	if deliveries == nil || contents == nil {
		return nil, ErrInvalid
	}
	return &PayloadAssembler{deliveries: deliveries, contents: contents}, nil
}

func (assembler *PayloadAssembler) Load(ctx context.Context, claim Claim) (Payload, error) {
	if assembler == nil || assembler.deliveries == nil || assembler.contents == nil {
		return Payload{}, ErrInvalid
	}
	snapshot, err := assembler.deliveries.LoadDelivery(ctx, claim)
	if err != nil {
		return Payload{}, err
	}
	revision, err := domain.RestoreConnectionRevision(snapshot.Revision, snapshot.ConnectorKind)
	if err != nil || revision.AccountID != claim.AccountID || revision.ID != claim.ConnectionRevisionID ||
		revision.ConnectionID != claim.ConnectionID || revision.Revision != claim.ConnectionRevision ||
		!containsCapability(revision.Capabilities, claim.Capability) {
		return Payload{}, ErrInvalid
	}
	assets := append([]marketingdomain.AssetRevision(nil), snapshot.Assets...)
	sort.Slice(assets, func(left, right int) bool { return assets[left].ID < assets[right].ID })
	manifestInput := ManifestInput{ReleaseID: claim.ReleaseID, ReleaseVersion: claim.ReleaseVersion, Capability: claim.Capability,
		ConnectionID: claim.ConnectionID, ConnectionRevisionID: claim.ConnectionRevisionID, ConnectionRevision: claim.ConnectionRevision,
		CredentialID: claim.CredentialID, CredentialGeneration: claim.CredentialGeneration, Assets: make([]ManifestAsset, 0, len(assets))}
	type providerAsset struct {
		ID              string                    `json:"id"`
		Kind            marketingdomain.AssetKind `json:"kind"`
		Title           string                    `json:"title"`
		MediaType       string                    `json:"media_type"`
		AlternativeText string                    `json:"alternative_text,omitempty"`
		Content         []byte                    `json:"content_base64"`
	}
	provider := struct {
		Version        uint64                 `json:"version"`
		Capability     domain.Capability      `json:"capability"`
		IdempotencyKey string                 `json:"idempotency_key"`
		Scope          domain.ConnectionScope `json:"scope"`
		Assets         []providerAsset        `json:"assets"`
	}{Version: 1, Capability: claim.Capability, IdempotencyKey: string(claim.IdempotencyKey), Scope: revision.Scope,
		Assets: make([]providerAsset, 0, len(assets))}
	var total uint64
	for _, candidate := range assets {
		asset, restoreErr := marketingdomain.RestoreAssetRevision(candidate)
		if restoreErr != nil || asset.AccountID != claim.AccountID {
			return Payload{}, ErrInvalid
		}
		if asset.ContentBytes > uint64(MaximumPayloadBytes) || total > uint64(MaximumPayloadBytes)-asset.ContentBytes {
			return Payload{}, ErrInvalid
		}
		total += asset.ContentBytes
		reader, openErr := assembler.contents.OpenContent(ctx, asset)
		if openErr != nil {
			return Payload{}, openErr
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, int64(asset.ContentBytes)+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return Payload{}, errors.Join(readErr, closeErr)
		}
		if uint64(len(body)) != asset.ContentBytes || sha256.Sum256(body) != asset.ContentSHA256 {
			return Payload{}, ErrInvalid
		}
		manifestInput.Assets = append(manifestInput.Assets, ManifestAsset{ID: asset.ID, SHA256: asset.ContentSHA256})
		provider.Assets = append(provider.Assets, providerAsset{ID: string(asset.ID), Kind: asset.Kind, Title: asset.Title,
			MediaType: asset.MediaType, AlternativeText: asset.AlternativeText, Content: body})
	}
	manifest, digest, err := BuildManifest(manifestInput)
	if err != nil || digest != claim.ManifestSHA256 {
		return Payload{}, ErrInvalid
	}
	encoded, err := json.Marshal(provider)
	if err != nil || len(encoded) == 0 || len(encoded) > MaximumPayloadBytes {
		return Payload{}, ErrInvalid
	}
	return Payload{CanonicalManifest: manifest, ProviderPayload: encoded}, nil
}

func containsCapability(values []domain.Capability, target domain.Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ PayloadSource = (*PayloadAssembler)(nil)
