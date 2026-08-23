package s3objects

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/encrypt"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const exportReferencePrefix = "s3-export-v1."

// ExportStore is a private, version-aware Account-export artifact store. It is
// intentionally a distinct adapter from the customer document store so each
// workload can receive a separate bucket and least-privilege credential.
type ExportStore struct {
	client client
	bucket string
	sse    encrypt.ServerSide
}

func NewExport(config Config) (*ExportStore, error) {
	store, err := New(config)
	if err != nil {
		return nil, err
	}
	return &ExportStore{client: store.client, bucket: store.bucket, sse: store.sse}, nil
}

func newExportStore(value client, bucket string, sse encrypt.ServerSide) (*ExportStore, error) {
	store, err := newStore(value, bucket, sse)
	if err != nil {
		return nil, err
	}
	return &ExportStore{client: store.client, bucket: store.bucket, sse: store.sse}, nil
}

func (store *ExportStore) Verify(ctx context.Context) error {
	base := &Store{client: store.client, bucket: store.bucket, sse: store.sse}
	return base.Verify(ctx)
}

// VerifyReadOnly is suitable for an exact-key reader or deletion-only startup
// check; it deliberately requires no bucket-list permission.
func (store *ExportStore) VerifyReadOnly(ctx context.Context) error {
	base := &Store{client: store.client, bucket: store.bucket, sse: store.sse}
	return base.VerifyReadOnly(ctx)
}

func (store *ExportStore) Publish(ctx context.Context, request accountexport.ArtifactWrite) (accountexport.Artifact, error) {
	if ids.Validate(request.ExportID) != nil || request.Body == nil || request.Bytes <= 0 || request.Bytes > accountexport.MaximumArtifactBytes || request.SHA256 == ([sha256.Size]byte{}) {
		return accountexport.Artifact{}, accountexport.ErrInvalid
	}
	key := exportObjectKey(request.ExportID)
	digestHex := hex.EncodeToString(request.SHA256[:])
	options := minio.PutObjectOptions{
		ContentType:          "application/zip",
		SendContentMd5:       true,
		ServerSideEncryption: store.sse,
		UserMetadata: map[string]string{
			"spyglass-sha256":      digestHex,
			"spyglass-export-id":   request.ExportID,
			"spyglass-object-kind": "account-export",
		},
	}
	options.SetMatchETagExcept("*")
	hasher := sha256.New()
	limited := &io.LimitedReader{R: request.Body, N: request.Bytes}
	info, err := store.client.PutObject(ctx, store.bucket, key, io.TeeReader(limited, hasher), request.Bytes, options)
	if err != nil {
		if isPrecondition(err) {
			return store.existing(ctx, request.ExportID, request.Bytes, request.SHA256)
		}
		return accountexport.Artifact{}, fmt.Errorf("%w: publish export artifact: %v", ErrUnavailable, err)
	}
	artifact, identityErr := exportArtifact(request.ExportID, info.VersionID, info.Size, request.SHA256)
	if limited.N != 0 || info.Size != request.Bytes || !equalDigest(hasher.Sum(nil), request.SHA256) || identityErr != nil {
		store.cleanup(ctx, key, info.VersionID)
		if identityErr != nil {
			return accountexport.Artifact{}, identityErr
		}
		return accountexport.Artifact{}, errors.Join(ErrIntegrity, accountexport.ErrArtifactIntegrity)
	}
	var extra [1]byte
	count, readErr := request.Body.Read(extra[:])
	if count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		store.cleanup(ctx, key, info.VersionID)
		return accountexport.Artifact{}, accountexport.ErrInvalid
	}
	return artifact, nil
}

// Delete verifies the exact immutable version, size and digest before removing
// it. A missing version is success so the expiry processor can safely retry
// after an unknown database commit outcome.
func (store *ExportStore) Delete(ctx context.Context, artifact accountexport.Artifact) error {
	exportID, versionID, err := parseExportReference(artifact.Reference)
	if err != nil || artifact.Bytes <= 0 || artifact.Bytes > accountexport.MaximumArtifactBytes || artifact.SHA256 == ([sha256.Size]byte{}) {
		return accountexport.ErrInvalid
	}
	key := exportObjectKey(exportID)
	info, err := store.client.StatObject(ctx, store.bucket, key, minio.StatObjectOptions{VersionID: versionID, ServerSideEncryption: store.sse})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("%w: stat export artifact: %v", ErrUnavailable, err)
	}
	if info.VersionID != versionID || !matches(info, artifact.Bytes, artifact.SHA256) {
		return ErrIntegrity
	}
	if err := store.client.RemoveObject(ctx, store.bucket, key, minio.RemoveObjectOptions{VersionID: versionID}); err != nil && !isNotFound(err) {
		return fmt.Errorf("%w: delete export artifact: %v", ErrUnavailable, err)
	}
	return nil
}

// Open verifies the exact immutable object version, byte count, and digest
// before returning the artifact stream. It requires only exact-key read access.
func (store *ExportStore) Open(ctx context.Context, artifact accountexport.Artifact) (io.ReadCloser, error) {
	exportID, versionID, err := parseExportReference(artifact.Reference)
	if err != nil || !validExportArtifact(artifact) {
		return nil, accountexport.ErrInvalid
	}
	key := exportObjectKey(exportID)
	options := minio.GetObjectOptions{VersionID: versionID, ServerSideEncryption: store.sse, Checksum: true}
	info, err := store.client.StatObject(ctx, store.bucket, key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: stat export artifact: %v", ErrUnavailable, err)
	}
	if info.VersionID != versionID || !matches(info, artifact.Bytes, artifact.SHA256) {
		return nil, errors.Join(ErrIntegrity, accountexport.ErrArtifactIntegrity)
	}
	object, err := store.client.GetObject(ctx, store.bucket, key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: get export artifact: %v", ErrUnavailable, err)
	}
	return object, nil
}

func validExportArtifact(artifact accountexport.Artifact) bool {
	return artifact.Bytes > 0 && artifact.Bytes <= accountexport.MaximumArtifactBytes && artifact.SHA256 != ([sha256.Size]byte{})
}

func (store *ExportStore) existing(ctx context.Context, exportID string, size int64, digest [sha256.Size]byte) (accountexport.Artifact, error) {
	key := exportObjectKey(exportID)
	info, err := store.client.StatObject(ctx, store.bucket, key, minio.StatObjectOptions{ServerSideEncryption: store.sse})
	if err != nil {
		return accountexport.Artifact{}, fmt.Errorf("%w: stat existing export artifact: %v", ErrUnavailable, err)
	}
	if !matches(info, size, digest) {
		return accountexport.Artifact{}, errors.Join(ErrConflict, accountexport.ErrArtifactConflict)
	}
	return exportArtifact(exportID, info.VersionID, info.Size, digest)
}

func (store *ExportStore) cleanup(ctx context.Context, key, versionID string) {
	if versionID != "" {
		_ = store.client.RemoveObject(ctx, store.bucket, key, minio.RemoveObjectOptions{VersionID: versionID})
	}
}

func exportArtifact(exportID, versionID string, size int64, digest [sha256.Size]byte) (accountexport.Artifact, error) {
	if ids.Validate(exportID) != nil || versionID == "" || strings.ContainsAny(versionID, "\x00\r\n") || len(versionID) > 512 || size <= 0 || size > accountexport.MaximumArtifactBytes || digest == ([sha256.Size]byte{}) {
		return accountexport.Artifact{}, errors.Join(
			fmt.Errorf("%w: immutable export object version identity is required", ErrConfiguration),
			accountexport.ErrArtifactIntegrity,
		)
	}
	encodedVersion := base64.RawURLEncoding.EncodeToString([]byte(versionID))
	return accountexport.Artifact{Reference: exportReferencePrefix + exportID + "." + encodedVersion, SHA256: digest, Bytes: size}, nil
}

func parseExportReference(reference string) (string, string, error) {
	if !strings.HasPrefix(reference, exportReferencePrefix) || len(reference) > 1000 || strings.TrimSpace(reference) != reference || strings.ContainsAny(reference, "\x00\r\n") {
		return "", "", accountexport.ErrInvalid
	}
	remainder := strings.TrimPrefix(reference, exportReferencePrefix)
	exportID, encodedVersion, found := strings.Cut(remainder, ".")
	if !found || ids.Validate(exportID) != nil || encodedVersion == "" || strings.Contains(encodedVersion, ".") {
		return "", "", accountexport.ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encodedVersion)
	if err != nil || len(decoded) == 0 || len(decoded) > 512 || strings.ContainsAny(string(decoded), "\x00\r\n") {
		return "", "", accountexport.ErrInvalid
	}
	return exportID, string(decoded), nil
}

func exportObjectKey(exportID string) string {
	return "exports/" + exportID + "/artifact.zip"
}

func isNotFound(err error) bool {
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "NoSuchKey", "NoSuchObject", "NoSuchVersion", "NotFound", "XMinioInvalidObjectName":
		return true
	default:
		return false
	}
}

var (
	_ accountexport.ArtifactPublisher = (*ExportStore)(nil)
	_ accountexport.ArtifactDeleter   = (*ExportStore)(nil)
	_ accountexport.ArtifactReader    = (*ExportStore)(nil)
)
