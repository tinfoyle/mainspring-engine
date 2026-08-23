package integrationexecution_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

type deliverySource struct {
	snapshot integrationexecution.DeliverySnapshot
	err      error
}

func (source deliverySource) LoadDelivery(context.Context, integrationexecution.Claim) (integrationexecution.DeliverySnapshot, error) {
	return source.snapshot, source.err
}

type contentSource map[string][]byte

func (source contentSource) OpenContent(_ context.Context, asset marketingdomain.AssetRevision) (io.ReadCloser, error) {
	value, exists := source[asset.ContentReference]
	if !exists {
		return nil, errors.New("content unavailable")
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func TestPayloadAssemblerReconstructsExactManifestAndRuntimeOnlyProviderPayload(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	body := []byte("approved launch copy")
	asset := payloadAsset(now, body)
	claim := payloadClaim(now, asset)
	assembler, err := integrationexecution.NewPayloadAssembler(deliverySource{snapshot: payloadSnapshot(now, claim, asset)}, contentSource{asset.ContentReference: body})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := assembler.Load(context.Background(), claim)
	if err != nil || sha256.Sum256(payload.CanonicalManifest) != claim.ManifestSHA256 || len(payload.ProviderPayload) == 0 {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	provider := string(payload.ProviderPayload)
	if strings.Contains(provider, asset.ContentReference) || !strings.Contains(provider, base64.StdEncoding.EncodeToString(body)) ||
		!strings.Contains(provider, string(claim.ExecutionID)) || !strings.Contains(provider, "launch@example.com") {
		t.Fatalf("provider payload violated runtime envelope: %s", provider)
	}
}

func TestPayloadAssemblerRejectsContentAndFrozenAuthorityDrift(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	body := []byte("approved launch copy")
	asset := payloadAsset(now, body)
	claim := payloadClaim(now, asset)
	for _, testCase := range []struct {
		name     string
		edit     func(*integrationexecution.DeliverySnapshot)
		contents contentSource
	}{
		{name: "content_digest", contents: contentSource{asset.ContentReference: []byte("changed launch copy")}},
		{name: "revision", contents: contentSource{asset.ContentReference: body}, edit: func(snapshot *integrationexecution.DeliverySnapshot) { snapshot.Revision.Revision++ }},
		{name: "asset_account", contents: contentSource{asset.ContentReference: body}, edit: func(snapshot *integrationexecution.DeliverySnapshot) {
			snapshot.Assets[0].AccountID = "d9900000-0000-4000-8000-000000000099"
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := payloadSnapshot(now, claim, asset)
			if testCase.edit != nil {
				testCase.edit(&snapshot)
			}
			assembler, _ := integrationexecution.NewPayloadAssembler(deliverySource{snapshot: snapshot}, testCase.contents)
			if _, err := assembler.Load(context.Background(), claim); !errors.Is(err, integrationexecution.ErrInvalid) {
				t.Fatalf("load error=%v", err)
			}
		})
	}
}

func payloadClaim(now time.Time, asset marketingdomain.AssetRevision) integrationexecution.Claim {
	input := integrationexecution.ManifestInput{ReleaseID: "d1100000-0000-4000-8000-000000000001", ReleaseVersion: 3, Capability: domain.CapabilityEmailSend,
		ConnectionID: "d1200000-0000-4000-8000-000000000002", ConnectionRevisionID: "d1300000-0000-4000-8000-000000000003",
		ConnectionRevision: 2, CredentialID: "d1400000-0000-4000-8000-000000000004", CredentialGeneration: 4,
		Assets: []integrationexecution.ManifestAsset{{ID: asset.ID, SHA256: asset.ContentSHA256}}}
	_, digest, _ := integrationexecution.BuildManifest(input)
	return integrationexecution.Claim{AccountID: asset.AccountID, ExecutionID: "d1500000-0000-4000-8000-000000000005",
		Capability: input.Capability, ReleaseID: input.ReleaseID, ReleaseVersion: input.ReleaseVersion, ConnectionID: input.ConnectionID,
		ConnectionRevisionID: input.ConnectionRevisionID, ConnectionRevision: input.ConnectionRevision, CredentialID: input.CredentialID,
		CredentialGeneration: input.CredentialGeneration, ManifestSHA256: digest, IdempotencyKey: "d1500000-0000-4000-8000-000000000005", LeaseExpiresAt: now.Add(time.Minute)}
}

func payloadSnapshot(now time.Time, claim integrationexecution.Claim, asset marketingdomain.AssetRevision) integrationexecution.DeliverySnapshot {
	return integrationexecution.DeliverySnapshot{ConnectorKind: domain.ConnectorEmail, Revision: domain.ConnectionRevision{
		ID: claim.ConnectionRevisionID, AccountID: claim.AccountID, ConnectionID: claim.ConnectionID, Revision: claim.ConnectionRevision,
		Capabilities: []domain.Capability{domain.CapabilityEmailSend}, Scope: domain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v1"},
		CreatedBy: domain.Actor{UserID: "d1600000-0000-4000-8000-000000000006"}, CreatedAt: now.Add(-time.Hour),
	}, Assets: []marketingdomain.AssetRevision{asset}}
}

func payloadAsset(now time.Time, body []byte) marketingdomain.AssetRevision {
	return marketingdomain.AssetRevision{ID: "d1700000-0000-4000-8000-000000000007", AccountID: "d1800000-0000-4000-8000-000000000008",
		CampaignID: "d1900000-0000-4000-8000-000000000009", AssetID: "d1a00000-0000-4000-8000-00000000000a", Revision: 1,
		Kind: marketingdomain.AssetCopy, Title: "Launch copy", MediaType: "text/plain", ContentReference: "objects/launch-copy-v1",
		ContentSHA256: sha256.Sum256(body), ContentBytes: uint64(len(body)), CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: "d1b00000-0000-4000-8000-00000000000b"},
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: now.Add(-time.Hour)}
}
