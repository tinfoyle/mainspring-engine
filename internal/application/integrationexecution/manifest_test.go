package integrationexecution_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

func TestBuildManifestIsCanonicalSortedAndStable(t *testing.T) {
	first := sha256.Sum256([]byte("first"))
	second := sha256.Sum256([]byte("second"))
	input := integrationexecution.ManifestInput{
		ReleaseID: "c1100000-0000-4000-8000-000000000001", ReleaseVersion: 3, Capability: domain.CapabilityEmailSend,
		ConnectionID: "c1200000-0000-4000-8000-000000000002", ConnectionRevisionID: "c1300000-0000-4000-8000-000000000003",
		ConnectionRevision: 2, CredentialID: "c1400000-0000-4000-8000-000000000004", CredentialGeneration: 4,
		Assets: []integrationexecution.ManifestAsset{
			{ID: "c1600000-0000-4000-8000-000000000006", SHA256: second},
			{ID: "c1500000-0000-4000-8000-000000000005", SHA256: first},
		},
	}
	encoded, digest, err := integrationexecution.BuildManifest(input)
	want := `{"release_id":"c1100000-0000-4000-8000-000000000001","release_version":3,"capability":"email.send","connection_id":"c1200000-0000-4000-8000-000000000002","connection_revision_id":"c1300000-0000-4000-8000-000000000003","connection_revision":2,"credential_id":"c1400000-0000-4000-8000-000000000004","credential_generation":4,"assets":[{"id":"c1500000-0000-4000-8000-000000000005","sha256":"a7937b64b8caa58f03721bb6bacf5c78cb235febe0e70b1b84cd99541461a08e"},{"id":"c1600000-0000-4000-8000-000000000006","sha256":"16367aacb67a4a017c8da8ab95682ccb390863780f7114dda0a0e0c55644c7c4"}]}`
	if err != nil || string(encoded) != want || digest != sha256.Sum256([]byte(want)) {
		t.Fatalf("manifest=%s digest=%x err=%v", encoded, digest, err)
	}
}

func TestBuildManifestRejectsDuplicateOrEmptyAssetAuthority(t *testing.T) {
	digest := sha256.Sum256([]byte("asset"))
	input := integrationexecution.ManifestInput{
		ReleaseID: "c2100000-0000-4000-8000-000000000001", ReleaseVersion: 1, Capability: domain.CapabilityWebPublish,
		ConnectionID: "c2200000-0000-4000-8000-000000000002", ConnectionRevisionID: "c2300000-0000-4000-8000-000000000003",
		ConnectionRevision: 1, CredentialID: "c2400000-0000-4000-8000-000000000004", CredentialGeneration: 1,
	}
	if _, _, err := integrationexecution.BuildManifest(input); !errors.Is(err, integrationexecution.ErrInvalid) {
		t.Fatalf("empty assets=%v", err)
	}
	input.Assets = []integrationexecution.ManifestAsset{{ID: "c2500000-0000-4000-8000-000000000005", SHA256: digest}, {ID: "c2500000-0000-4000-8000-000000000005", SHA256: digest}}
	if _, _, err := integrationexecution.BuildManifest(input); !errors.Is(err, integrationexecution.ErrInvalid) {
		t.Fatalf("duplicate assets=%v", err)
	}
}
