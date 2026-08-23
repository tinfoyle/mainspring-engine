package marketing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type assetObjects struct {
	puts    int
	deleted *AssetObjectIdentity
}

func (*assetObjects) Verify(context.Context) error { return nil }

func (store *assetObjects) PutMarketingAssetImmutable(_ context.Context, value AssetObjectWrite) (AssetObjectWriteResult, error) {
	store.puts++
	body, err := io.ReadAll(value.Body)
	if err != nil || int64(len(body)) != value.Size || sha256.Sum256(body) != value.ContentSHA256 {
		return AssetObjectWriteResult{}, ErrInvalid
	}
	key, err := domain.AssetObjectKey(value.AccountID, value.CampaignID, value.AssetID, value.RevisionID)
	if err != nil {
		return AssetObjectWriteResult{}, err
	}
	reference, _ := domain.ContentReferenceForObjectVersion("asset-version-1")
	return AssetObjectWriteResult{Identity: AssetObjectIdentity{Key: key, Version: "asset-version-1", Reference: reference,
		Size: value.Size, ContentSHA256: value.ContentSHA256}, Created: true}, nil
}

func (store *assetObjects) DeleteMarketingAsset(_ context.Context, identity AssetObjectIdentity) error {
	store.deleted = &identity
	return nil
}

func TestAssetAdmissionWritesExactObjectAndPersistsOnlyCanonicalIdentity(t *testing.T) {
	authorizer := &authorizerStub{role: accounts.RoleMember}
	store := &storeStub{}
	objects := &assetObjects{}
	body := []byte("launch copy")
	revisionID := ids.MarketingAssetRevisionID("71000000-0000-4000-8000-000000000007")
	store.createAsset = func(input domain.AssetRevisionInput, role accounts.MembershipRole, _ Mutation) (domain.AssetRevision, bool, error) {
		if role != accounts.RoleMember || input.ID != revisionID || input.ContentSHA256 != sha256.Sum256(body) || input.ContentBytes != uint64(len(body)) {
			t.Fatalf("input=%+v role=%s", input, role)
		}
		if version, err := domain.ObjectVersionFromContentReference(input.ContentReference); err != nil || version != "asset-version-1" {
			t.Fatalf("reference=%q version=%q err=%v", input.ContentReference, version, err)
		}
		value, err := domain.NewAssetRevision(input, nil, role)
		return value, true, err
	}
	service, _ := New(authorizer, store, fixedClock{now: testNow})
	admission, err := NewAssetAdmissionService(service, objects)
	if err != nil {
		t.Fatal(err)
	}
	value, created, err := admission.Upload(context.Background(), UploadAssetRevisionCommand{Actor: access.Actor{UserID: testUserID},
		AccountID: testAccountID, RequestID: string(revisionID), CampaignID: testCampaignID, AssetID: testAssetID,
		Kind: domain.AssetCopy, Title: "Launch copy", MediaType: "text/plain; charset=utf-8", Body: bytes.NewReader(body),
		Provenance: domain.Provenance{Origin: domain.OriginHuman}})
	if err != nil || !created || value.ID != revisionID || objects.puts != 1 || objects.deleted != nil {
		t.Fatalf("value=%+v created=%t puts=%d deleted=%+v err=%v", value, created, objects.puts, objects.deleted, err)
	}
}

func TestAssetAdmissionCleansNewObjectWhenPersistenceFails(t *testing.T) {
	authorizer := &authorizerStub{role: accounts.RoleMember}
	store := &storeStub{createAsset: func(domain.AssetRevisionInput, accounts.MembershipRole, Mutation) (domain.AssetRevision, bool, error) {
		return domain.AssetRevision{}, false, errors.New("database unavailable")
	}}
	objects := &assetObjects{}
	service, _ := New(authorizer, store, fixedClock{now: testNow})
	admission, _ := NewAssetAdmissionService(service, objects)
	revisionID := "72000000-0000-4000-8000-000000000007"
	_, _, err := admission.Upload(context.Background(), UploadAssetRevisionCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID,
		RequestID: revisionID, CampaignID: testCampaignID, AssetID: testAssetID, Kind: domain.AssetImage, Title: "Hero",
		MediaType: "image/png", AlternativeText: "Product hero", Body: bytes.NewReader([]byte("synthetic image bytes")),
		Provenance: domain.Provenance{Origin: domain.OriginHuman}})
	if err == nil || objects.deleted == nil || objects.deleted.Version != "asset-version-1" {
		t.Fatalf("deleted=%+v err=%v", objects.deleted, err)
	}
}
