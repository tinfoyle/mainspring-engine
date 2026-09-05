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

type downloadStore struct {
	Store
	asset domain.AssetRevision
	calls int
}

func (store *downloadStore) GetAssetRevision(_ context.Context, account ids.AccountID, revision ids.MarketingAssetRevisionID) (domain.AssetRevision, error) {
	store.calls++
	if account != store.asset.AccountID || revision != store.asset.ID {
		return domain.AssetRevision{}, ErrNotFound
	}
	return store.asset, nil
}

type downloadObjects struct {
	AssetObjectStore
	body  []byte
	calls int
}

func (objects *downloadObjects) OpenContent(context.Context, domain.AssetRevision) (io.ReadCloser, error) {
	objects.calls++
	return io.NopCloser(bytes.NewReader(objects.body)), nil
}

type downloadAuthorizer struct{ denied bool }

func (auth *downloadAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	if auth.denied || requirement.Mutation || requirement.Package != PackageCode {
		return access.AccountContext{}, errors.New("access denied")
	}
	return access.AccountContext{Role: accounts.RoleViewer}, nil
}
func TestAssetDownloadChecksAuthorityOwnershipAndContent(t *testing.T) {
	content := []byte("synthetic campaign text")
	reference, _ := domain.ContentReferenceForObjectVersion("download-v1")
	asset, err := domain.NewAssetRevision(domain.AssetRevisionInput{ID: "71000000-0000-4000-8000-000000000007", AccountID: testAccountID, CampaignID: testCampaignID, AssetID: testAssetID, Kind: domain.AssetCopy, Title: "Audit copy", MediaType: "text/plain", ContentReference: reference, ContentSHA256: sha256.Sum256(content), ContentBytes: uint64(len(content)), CreatedBy: domain.Actor{Kind: domain.ActorUser, ID: string(testUserID)}, Provenance: domain.Provenance{Origin: domain.OriginHuman}, CreatedAt: testNow}, nil, accounts.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	store := &downloadStore{asset: asset}
	objects := &downloadObjects{body: content}
	auth := &downloadAuthorizer{}
	service, _ := New(auth, store, fixedClock{now: testNow})
	admission, _ := NewAssetAdmissionService(service, objects)
	actor := access.Actor{UserID: testUserID}
	_, body, err := admission.Download(context.Background(), actor, testAccountID, testCampaignID, asset.ID)
	if err != nil || !bytes.Equal(body, content) {
		t.Fatalf("download=%q %v", body, err)
	}
	objects.body = []byte("tampered campaign text")
	if _, body, err := admission.Download(context.Background(), actor, testAccountID, testCampaignID, asset.ID); !errors.Is(err, ErrCorrupt) || body != nil {
		t.Fatalf("tampered bytes exposed: %q %v", body, err)
	}
	before := objects.calls
	if _, _, err := admission.Download(context.Background(), actor, testAccountID, "81000000-0000-4000-8000-000000000008", asset.ID); !errors.Is(err, ErrNotFound) || objects.calls != before {
		t.Fatalf("cross-campaign access: %v", err)
	}
	auth.denied = true
	reads := store.calls
	if _, _, err := admission.Download(context.Background(), actor, testAccountID, testCampaignID, asset.ID); err == nil || store.calls != reads || objects.calls != before {
		t.Fatal("denied download accessed storage")
	}
}
