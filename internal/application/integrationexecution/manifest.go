package integrationexecution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ManifestAsset struct {
	ID     ids.MarketingAssetRevisionID
	SHA256 [sha256.Size]byte
}

type ManifestInput struct {
	ReleaseID            ids.MarketingReleaseID
	ReleaseVersion       uint64
	Capability           domain.Capability
	ConnectionID         ids.IntegrationConnectionID
	ConnectionRevisionID ids.IntegrationConnectionRevisionID
	ConnectionRevision   uint64
	CredentialID         ids.IntegrationCredentialID
	CredentialGeneration uint64
	Assets               []ManifestAsset
}

// BuildManifest is the single byte-level contract used at preparation and
// connector-runtime reconstruction. Its JSON field order is intentionally
// fixed because the SHA-256 digest is durable execution authority.
func BuildManifest(input ManifestInput) ([]byte, [sha256.Size]byte, error) {
	if ids.Validate(string(input.ReleaseID)) != nil || ids.Validate(string(input.ConnectionID)) != nil ||
		ids.Validate(string(input.ConnectionRevisionID)) != nil || ids.Validate(string(input.CredentialID)) != nil ||
		input.ReleaseVersion == 0 || input.ConnectionRevision == 0 || input.CredentialGeneration == 0 ||
		(input.Capability != domain.CapabilityEmailSend && input.Capability != domain.CapabilityWebPublish) ||
		len(input.Assets) == 0 || len(input.Assets) > marketingdomain.MaximumReleaseAssets {
		return nil, [sha256.Size]byte{}, ErrInvalid
	}
	assets := append([]ManifestAsset(nil), input.Assets...)
	sort.Slice(assets, func(left, right int) bool { return assets[left].ID < assets[right].ID })
	type canonicalAsset struct {
		ID     ids.MarketingAssetRevisionID `json:"id"`
		SHA256 string                       `json:"sha256"`
	}
	manifest := struct {
		ReleaseID            ids.MarketingReleaseID              `json:"release_id"`
		ReleaseVersion       uint64                              `json:"release_version"`
		Capability           domain.Capability                   `json:"capability"`
		ConnectionID         ids.IntegrationConnectionID         `json:"connection_id"`
		ConnectionRevisionID ids.IntegrationConnectionRevisionID `json:"connection_revision_id"`
		ConnectionRevision   uint64                              `json:"connection_revision"`
		CredentialID         ids.IntegrationCredentialID         `json:"credential_id"`
		CredentialGeneration uint64                              `json:"credential_generation"`
		Assets               []canonicalAsset                    `json:"assets"`
	}{
		ReleaseID: input.ReleaseID, ReleaseVersion: input.ReleaseVersion, Capability: input.Capability,
		ConnectionID: input.ConnectionID, ConnectionRevisionID: input.ConnectionRevisionID, ConnectionRevision: input.ConnectionRevision,
		CredentialID: input.CredentialID, CredentialGeneration: input.CredentialGeneration, Assets: make([]canonicalAsset, 0, len(assets)),
	}
	for index, asset := range assets {
		if ids.Validate(string(asset.ID)) != nil || asset.SHA256 == ([sha256.Size]byte{}) || (index > 0 && assets[index-1].ID == asset.ID) {
			return nil, [sha256.Size]byte{}, ErrInvalid
		}
		manifest.Assets = append(manifest.Assets, canonicalAsset{ID: asset.ID, SHA256: hex.EncodeToString(asset.SHA256[:])})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return encoded, sha256.Sum256(encoded), nil
}
