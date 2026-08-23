package s3objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

var testObjectTime = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func TestPutMarketingAssetBindsDerivedKeyVersionReferenceAndMetadata(t *testing.T) {
	body := []byte("immutable campaign copy")
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "marketing-version-1"}}
	store, _ := newStore(client, "spyglass-documents", nil)
	request := marketingAssetWrite(body)
	result, err := store.PutMarketingAssetImmutable(context.Background(), request)
	wantKey := "accounts/e1100000-0000-4000-8000-000000000001/marketing/campaigns/e1200000-0000-4000-8000-000000000002/assets/e1300000-0000-4000-8000-000000000003/revisions/e1400000-0000-4000-8000-000000000004/content"
	version, referenceErr := marketingdomain.ObjectVersionFromContentReference(result.Identity.Reference)
	if err != nil || referenceErr != nil || !result.Created || result.Identity.Key != wantKey || version != "marketing-version-1" ||
		client.putOptions.UserMetadata["spyglass-account-id"] != string(request.AccountID) ||
		client.putOptions.UserMetadata["spyglass-object-kind"] != "marketing-asset" || client.putOptions.ContentType != "text/plain" {
		t.Fatalf("result=%+v options=%+v err=%v referenceErr=%v", result, client.putOptions, err, referenceErr)
	}
}

func TestMarketingContentReaderRejectsReferenceOrMetadataDriftBeforeGet(t *testing.T) {
	body := []byte("immutable campaign copy")
	request := marketingAssetWrite(body)
	reference, _ := marketingdomain.ContentReferenceForObjectVersion("marketing-version-1")
	asset := marketingdomain.AssetRevision{ID: request.RevisionID, AccountID: request.AccountID, CampaignID: request.CampaignID, AssetID: request.AssetID,
		Revision: 1, Kind: marketingdomain.AssetCopy, Title: "Launch copy", MediaType: request.MediaType, ContentReference: reference,
		ContentSHA256: request.ContentSHA256, ContentBytes: uint64(request.Size), CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: "e1500000-0000-4000-8000-000000000005"},
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: testObjectTime}
	client := &fakeClient{statInfo: minio.ObjectInfo{Size: request.Size, VersionID: "marketing-version-1", UserMetadata: map[string]string{
		"spyglass-sha256": hexDigest(request.ContentSHA256), "spyglass-account-id": string(request.AccountID), "spyglass-object-kind": "knowledge-source"}}}
	store, _ := newStore(client, "spyglass-documents", nil)
	if _, err := store.OpenContent(context.Background(), asset); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("metadata drift err=%v", err)
	}
	asset.ContentReference = "arbitrary/path"
	if _, err := store.OpenContent(context.Background(), asset); err == nil {
		t.Fatal("arbitrary Marketing content reference was opened")
	}
}

func TestDeleteMarketingAssetRequiresExactCanonicalVersion(t *testing.T) {
	client := &fakeClient{}
	store, _ := newStore(client, "spyglass-documents", nil)
	reference, _ := marketingdomain.ContentReferenceForObjectVersion("version-2")
	identity := marketingapp.AssetObjectIdentity{Key: "accounts/e/marketing/campaigns/c/assets/a/revisions/r/content", Version: "version-2",
		Reference: reference, Size: 10, ContentSHA256: sha256.Sum256([]byte("0123456789"))}
	if err := store.DeleteMarketingAsset(context.Background(), identity); err != nil || client.removed.VersionID != "version-2" {
		t.Fatalf("removed=%+v err=%v", client.removed, err)
	}
	identity.Version = "changed"
	if err := store.DeleteMarketingAsset(context.Background(), identity); !errors.Is(err, marketingapp.ErrInvalid) {
		t.Fatalf("changed version err=%v", err)
	}
}

func marketingAssetWrite(body []byte) marketingapp.AssetObjectWrite {
	return marketingapp.AssetObjectWrite{AccountID: "e1100000-0000-4000-8000-000000000001", CampaignID: "e1200000-0000-4000-8000-000000000002",
		AssetID: "e1300000-0000-4000-8000-000000000003", RevisionID: "e1400000-0000-4000-8000-000000000004",
		MediaType: "text/plain", Size: int64(len(body)), ContentSHA256: sha256.Sum256(body), Body: bytes.NewReader(body)}
}
