package s3objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/encrypt"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
)

const testExportID = "e1000000-0000-4000-8000-000000000001"

func exportWrite(body []byte) accountexport.ArtifactWrite {
	return accountexport.ArtifactWrite{ExportID: testExportID, Bytes: int64(len(body)), SHA256: sha256.Sum256(body), Body: bytes.NewReader(body)}
}

func exportObjectInfo(body []byte, version string) minio.ObjectInfo {
	digest := sha256.Sum256(body)
	metadata := make(http.Header)
	metadata.Set("X-Amz-Meta-Spyglass-Sha256", hexDigest(digest))
	return minio.ObjectInfo{Size: int64(len(body)), VersionID: version, Metadata: metadata}
}

func TestExportStorePublishesExactImmutableArtifact(t *testing.T) {
	body := []byte("deterministic account export archive")
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "opaque/version+1"}}
	store, err := newExportStore(client, "spyglass-account-exports", encrypt.NewSSE())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Publish(context.Background(), exportWrite(body))
	if err != nil || artifact.Bytes != int64(len(body)) || artifact.SHA256 != sha256.Sum256(body) || !bytes.Equal(client.putBody, body) {
		t.Fatalf("artifact=%+v body=%q err=%v", artifact, client.putBody, err)
	}
	exportID, version, err := parseExportReference(artifact.Reference)
	if err != nil || exportID != testExportID || version != "opaque/version+1" || client.putKey != exportObjectKey(testExportID) {
		t.Fatalf("reference=%q export=%q version=%q key=%q err=%v", artifact.Reference, exportID, version, client.putKey, err)
	}
	if client.putOptions.ContentType != "application/zip" || client.putOptions.Header().Get("If-None-Match") != "*" || client.putOptions.ServerSideEncryption == nil ||
		client.putOptions.UserMetadata["spyglass-object-kind"] != "account-export" || client.putOptions.UserMetadata["spyglass-export-id"] != testExportID {
		t.Fatalf("put options=%+v", client.putOptions)
	}
}

func TestExportStoreReconcilesOnlyMatchingExistingArtifact(t *testing.T) {
	body := []byte("deterministic account export archive")
	client := &fakeClient{putErr: minio.ErrorResponse{Code: "PreconditionFailed"}, statInfo: exportObjectInfo(body, "existing-version")}
	store, _ := newExportStore(client, "spyglass-account-exports", nil)
	artifact, err := store.Publish(context.Background(), exportWrite(body))
	_, version, parseErr := parseExportReference(artifact.Reference)
	if err != nil || parseErr != nil || version != "existing-version" {
		t.Fatalf("artifact=%+v version=%q err=%v parse=%v", artifact, version, err, parseErr)
	}
	client.statInfo.Size++
	if _, err := store.Publish(context.Background(), exportWrite(body)); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
}

func TestExportStoreRejectsUnboundedOrCorruptPublish(t *testing.T) {
	body := []byte("archive")
	client := &fakeClient{putInfo: minio.UploadInfo{Size: int64(len(body)), VersionID: "bad-version"}}
	store, _ := newExportStore(client, "spyglass-account-exports", nil)
	request := exportWrite(body)
	request.Body = bytes.NewReader(append(append([]byte(nil), body...), '!'))
	if _, err := store.Publish(context.Background(), request); !errors.Is(err, accountexport.ErrInvalid) || client.removed.VersionID != "bad-version" {
		t.Fatalf("extra-byte err=%v removed=%+v", err, client.removed)
	}
	client.removed = minio.RemoveObjectOptions{}
	request = exportWrite(body)
	request.SHA256 = sha256.Sum256([]byte("different"))
	if _, err := store.Publish(context.Background(), request); !errors.Is(err, ErrIntegrity) || client.removed.VersionID != "bad-version" {
		t.Fatalf("digest err=%v removed=%+v", err, client.removed)
	}
}

func TestExportStoreDeletesExactVerifiedVersionIdempotently(t *testing.T) {
	body := []byte("archive")
	digest := sha256.Sum256(body)
	artifact, err := exportArtifact(testExportID, "version/2", int64(len(body)), digest)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{statInfo: exportObjectInfo(body, "version/2")}
	store, _ := newExportStore(client, "spyglass-account-exports", encrypt.NewSSE())
	if err := store.Delete(context.Background(), artifact); err != nil || client.statKey != exportObjectKey(testExportID) || client.statOptions.VersionID != "version/2" || client.removed.VersionID != "version/2" || client.removeKey != exportObjectKey(testExportID) {
		t.Fatalf("stat=%q options=%+v remove=%q/%+v err=%v", client.statKey, client.statOptions, client.removeKey, client.removed, err)
	}
	client.statErr = minio.ErrorResponse{Code: "NoSuchVersion"}
	client.removed = minio.RemoveObjectOptions{}
	if err := store.Delete(context.Background(), artifact); err != nil || client.removed.VersionID != "" {
		t.Fatalf("idempotent missing delete=%+v err=%v", client.removed, err)
	}
	client.statErr = nil
	client.statInfo.Size++
	if err := store.Delete(context.Background(), artifact); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("mismatched identity err=%v", err)
	}
}

func TestExportReferenceCannotSelectArbitraryObject(t *testing.T) {
	digest := sha256.Sum256([]byte("archive"))
	store, _ := newExportStore(&fakeClient{}, "spyglass-account-exports", nil)
	for _, reference := range []string{"", "s3-export-v1.not-an-id.dmVyc2lvbg", "s3-export-v1." + testExportID + ".", "s3-export-v1." + testExportID + ".dmVyc2lvbg.extra"} {
		if err := store.Delete(context.Background(), accountexport.Artifact{Reference: reference, SHA256: digest, Bytes: 7}); !errors.Is(err, accountexport.ErrInvalid) {
			t.Fatalf("reference=%q err=%v", reference, err)
		}
	}
}
