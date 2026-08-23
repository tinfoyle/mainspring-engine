package s3objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/encrypt"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
)

type fakeClient struct {
	exists     bool
	versioning minio.BucketVersioningConfiguration
	putInfo    minio.UploadInfo
	putErr     error
	statInfo   minio.ObjectInfo
	statErr    error
	putBody    []byte
	putKey     string
	putOptions minio.PutObjectOptions
	removed    minio.RemoveObjectOptions
}

func (client *fakeClient) BucketExists(context.Context, string) (bool, error) {
	return client.exists, nil
}
func (client *fakeClient) GetBucketVersioning(context.Context, string) (minio.BucketVersioningConfiguration, error) {
	return client.versioning, nil
}
func (client *fakeClient) PutObject(_ context.Context, _, key string, reader io.Reader, _ int64, options minio.PutObjectOptions) (minio.UploadInfo, error) {
	client.putKey = key
	client.putOptions = options
	client.putBody, _ = io.ReadAll(reader)
	return client.putInfo, client.putErr
}

func TestPutExtractedImmutableBindsDerivedIdentity(t *testing.T) {
	body := []byte("normalized extracted text")
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "extracted-version-1"}}
	store, _ := newStore(client, "spyglass-documents", nil)
	result, err := store.PutExtractedImmutable(context.Background(), knowledgeapp.ExtractedObjectWrite{
		AccountID: "a1000000-0000-4000-8000-000000000001", DocumentID: "a2000000-0000-4000-8000-000000000002", RevisionID: "a3000000-0000-4000-8000-000000000003",
		Size: int64(len(body)), ContentSHA256: sha256.Sum256(body), Body: bytes.NewReader(body),
	})
	if err != nil || !result.Created || result.Identity.Key != "accounts/a1000000-0000-4000-8000-000000000001/documents/a2000000-0000-4000-8000-000000000002/revisions/a3000000-0000-4000-8000-000000000003/extracted/text" || client.putKey != result.Identity.Key || client.putOptions.UserMetadata["spyglass-object-kind"] != "extracted-text" || client.putOptions.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("result=%+v key=%q options=%+v err=%v", result, client.putKey, client.putOptions, err)
	}
}
func (client *fakeClient) StatObject(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return client.statInfo, client.statErr
}
func (client *fakeClient) GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error) {
	return nil, errors.New("not implemented")
}
func (client *fakeClient) RemoveObject(_ context.Context, _, _ string, options minio.RemoveObjectOptions) error {
	client.removed = options
	return nil
}

func objectWrite(body []byte) knowledgeapp.SourceObjectWrite {
	return knowledgeapp.SourceObjectWrite{
		AccountID: "a1000000-0000-4000-8000-000000000001", DocumentID: "a2000000-0000-4000-8000-000000000002", RevisionID: "a3000000-0000-4000-8000-000000000003",
		MediaType: "text/plain", Size: int64(len(body)), ContentSHA256: sha256.Sum256(body), Body: bytes.NewReader(body),
	}
}

func TestStoreRequiresExistingVersionedBucket(t *testing.T) {
	client := &fakeClient{exists: true, versioning: minio.BucketVersioningConfiguration{Status: "Enabled"}}
	store, err := newStore(client, "spyglass-documents", nil)
	if err != nil || store.Verify(context.Background()) != nil {
		t.Fatalf("verify err=%v", err)
	}
	client.exists = false
	if err := store.VerifyReadOnly(context.Background()); err != nil {
		t.Fatalf("read-only verify unexpectedly required bucket listing: %v", err)
	}
	client.versioning.Status = "Suspended"
	if err := store.VerifyReadOnly(context.Background()); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("suspended versioning err=%v", err)
	}
}

func TestPutImmutableBindsDigestMetadataAndVersion(t *testing.T) {
	body := []byte("bounded source")
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "opaque-version-1"}}
	store, err := newStore(client, "spyglass-documents", encrypt.NewSSE())
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.PutImmutable(context.Background(), objectWrite(body))
	if err != nil || !result.Created || result.Identity.Version != "opaque-version-1" || result.Identity.Size != int64(len(body)) || !bytes.Equal(client.putBody, body) || client.putOptions.UserMetadata["spyglass-sha256"] == "" || client.putOptions.Header().Get("If-None-Match") != "*" || client.putOptions.ServerSideEncryption == nil {
		t.Fatalf("result=%+v options=%+v err=%v", result, client.putOptions, err)
	}
}

func TestPutImmutableCleansUpIntegrityFailureAndRejectsExtraBytes(t *testing.T) {
	body := []byte("bounded source")
	request := objectWrite(body)
	request.Body = bytes.NewReader(append(append([]byte(nil), body...), '!'))
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "bad-version"}}
	store, _ := newStore(client, "spyglass-documents", nil)
	if _, err := store.PutImmutable(context.Background(), request); !errors.Is(err, knowledgeapp.ErrInvalid) || client.removed.VersionID != "bad-version" {
		t.Fatalf("extra byte err=%v removed=%+v", err, client.removed)
	}
	client.removed = minio.RemoveObjectOptions{}
	request = objectWrite(body)
	request.ContentSHA256 = sha256.Sum256([]byte("different"))
	if _, err := store.PutImmutable(context.Background(), request); !errors.Is(err, ErrIntegrity) || client.removed.VersionID != "bad-version" {
		t.Fatalf("digest err=%v removed=%+v", err, client.removed)
	}
}

func TestPutImmutableReplaysOnlyMatchingExistingVersion(t *testing.T) {
	body := []byte("bounded source")
	digest := sha256.Sum256(body)
	client := &fakeClient{
		putErr:   minio.ErrorResponse{Code: "PreconditionFailed"},
		statInfo: minio.ObjectInfo{Size: int64(len(body)), VersionID: "existing-version", Metadata: map[string][]string{"X-Amz-Meta-Spyglass-Sha256": {"f53f2f27f4c70a6c4a15b3f1fd2f6e5d835161b619d6135361758d3bb88e8fcb"}}},
	}
	client.statInfo.Metadata.Set("X-Amz-Meta-Spyglass-Sha256", hexDigest(digest))
	store, _ := newStore(client, "spyglass-documents", nil)
	result, err := store.PutImmutable(context.Background(), objectWrite(body))
	if err != nil || result.Created || result.Identity.Version != "existing-version" {
		t.Fatalf("replay result=%+v err=%v", result, err)
	}
	client.statInfo.Size++
	if _, err := store.PutImmutable(context.Background(), objectWrite(body)); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
}

func TestDeleteRequiresAndUsesExactVersion(t *testing.T) {
	client := &fakeClient{}
	store, _ := newStore(client, "spyglass-documents", nil)
	digest := sha256.Sum256([]byte("source"))
	identity := knowledgeapp.SourceObjectIdentity{Key: "accounts/a/documents/b/revisions/c/source", Version: "version-2", Size: 6, ContentSHA256: digest}
	if err := store.Delete(context.Background(), identity); err != nil || client.removed.VersionID != "version-2" {
		t.Fatalf("delete removed=%+v err=%v", client.removed, err)
	}
	identity.Version = ""
	if err := store.Delete(context.Background(), identity); !errors.Is(err, knowledgeapp.ErrInvalid) {
		t.Fatalf("versionless delete err=%v", err)
	}
}

func hexDigest(value [sha256.Size]byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, sha256.Size*2)
	for index, current := range value {
		result[index*2], result[index*2+1] = alphabet[current>>4], alphabet[current&15]
	}
	return string(result)
}
