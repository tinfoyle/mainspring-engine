package s3objects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrConfiguration = errors.New("S3 object store configuration is invalid")
	ErrUnavailable   = errors.New("S3 object store is unavailable")
	ErrConflict      = errors.New("S3 object identity conflicts with stored content")
	ErrIntegrity     = errors.New("S3 object integrity check failed")
)

type Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	Secure                                         bool
	ServerSideEncryption                           bool
	Transport                                      http.RoundTripper
}

type client interface {
	BucketExists(context.Context, string) (bool, error)
	GetBucketVersioning(context.Context, string) (minio.BucketVersioningConfiguration, error)
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	StatObject(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error)
	GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
}

type Store struct {
	client client
	bucket string
	sse    encrypt.ServerSide
}

func New(config Config) (*Store, error) {
	config.Endpoint, config.Region, config.Bucket = strings.TrimSpace(config.Endpoint), strings.TrimSpace(config.Region), strings.TrimSpace(config.Bucket)
	if config.Endpoint == "" || config.Bucket == "" || strings.Contains(config.Endpoint, "://") || config.AccessKey == "" || config.SecretKey == "" {
		return nil, ErrConfiguration
	}
	options := &minio.Options{Creds: credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""), Secure: config.Secure, Region: config.Region}
	if config.Transport != nil {
		options.Transport = config.Transport
	} else {
		transport, err := minio.DefaultTransport(config.Secure)
		if err != nil {
			return nil, fmt.Errorf("%w: transport: %v", ErrConfiguration, err)
		}
		options.Transport = transport
	}
	value, err := minio.New(config.Endpoint, options)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfiguration, err)
	}
	var sse encrypt.ServerSide
	if config.ServerSideEncryption {
		sse = encrypt.NewSSE()
	}
	return newStore(value, config.Bucket, sse)
}

func newStore(value client, bucket string, sse encrypt.ServerSide) (*Store, error) {
	if value == nil || strings.TrimSpace(bucket) == "" || strings.ContainsAny(bucket, "/\\") {
		return nil, ErrConfiguration
	}
	return &Store{client: value, bucket: strings.TrimSpace(bucket), sse: sse}, nil
}

func (store *Store) Verify(ctx context.Context) error {
	exists, err := store.client.BucketExists(ctx, store.bucket)
	if err != nil {
		return fmt.Errorf("%w: check bucket: %v", ErrUnavailable, err)
	}
	if !exists {
		return fmt.Errorf("%w: bucket does not exist", ErrConfiguration)
	}
	return store.VerifyReadOnly(ctx)
}

// VerifyReadOnly verifies the immutable-version prerequisite without requiring
// bucket-list authority. It is the startup check for exact-key content readers.
func (store *Store) VerifyReadOnly(ctx context.Context) error {
	versioning, err := store.client.GetBucketVersioning(ctx, store.bucket)
	if err != nil {
		return fmt.Errorf("%w: check versioning: %v", ErrUnavailable, err)
	}
	if !versioning.Enabled() {
		return fmt.Errorf("%w: bucket versioning is required", ErrConfiguration)
	}
	return nil
}

func (store *Store) PutImmutable(ctx context.Context, request knowledgeapp.SourceObjectWrite) (knowledgeapp.SourceObjectWriteResult, error) {
	key, err := request.Key()
	if err != nil || request.Body == nil || request.Size <= 0 || request.Size > knowledgedomain.MaximumDocumentBytes || request.ContentSHA256 == ([sha256.Size]byte{}) || normalizeMediaType(request.MediaType) == "" {
		return knowledgeapp.SourceObjectWriteResult{}, knowledgeapp.ErrInvalid
	}
	return store.putImmutable(ctx, key, request.AccountID, "source", normalizeMediaType(request.MediaType), request.Size, request.ContentSHA256, request.Body)
}

func (store *Store) PutExtractedImmutable(ctx context.Context, request knowledgeapp.ExtractedObjectWrite) (knowledgeapp.ExtractedObjectWriteResult, error) {
	key, err := request.Key()
	if err != nil || request.Body == nil || request.Size <= 0 || request.Size > knowledgedomain.MaximumExtractedTextBytes || request.ContentSHA256 == ([sha256.Size]byte{}) {
		return knowledgeapp.ExtractedObjectWriteResult{}, knowledgeapp.ErrInvalid
	}
	result, err := store.putImmutable(ctx, key, request.AccountID, "extracted-text", "text/plain; charset=utf-8", request.Size, request.ContentSHA256, request.Body)
	return knowledgeapp.ExtractedObjectWriteResult{Identity: result.Identity, Created: result.Created}, err
}

func (store *Store) putImmutable(ctx context.Context, key string, accountID ids.AccountID, kind, mediaType string, size int64, digest [sha256.Size]byte, body io.Reader) (knowledgeapp.SourceObjectWriteResult, error) {
	digestHex := hex.EncodeToString(digest[:])
	options := minio.PutObjectOptions{ContentType: mediaType, SendContentMd5: true, ServerSideEncryption: store.sse, UserMetadata: map[string]string{"spyglass-sha256": digestHex, "spyglass-account-id": string(accountID), "spyglass-object-kind": kind}}
	options.SetMatchETagExcept("*")
	hasher := sha256.New()
	limited := &io.LimitedReader{R: body, N: size}
	info, err := store.client.PutObject(ctx, store.bucket, key, io.TeeReader(limited, hasher), size, options)
	if err != nil {
		if isPrecondition(err) {
			identity, existingErr := store.existing(ctx, key, size, digest)
			return knowledgeapp.SourceObjectWriteResult{Identity: identity}, existingErr
		}
		return knowledgeapp.SourceObjectWriteResult{}, fmt.Errorf("%w: put object: %v", ErrUnavailable, err)
	}
	identity := knowledgeapp.DocumentObjectIdentity{Key: key, Version: info.VersionID, Size: info.Size, ContentSHA256: digest}
	if limited.N != 0 || info.Size != size || !equalDigest(hasher.Sum(nil), digest) {
		store.cleanup(ctx, identity)
		return knowledgeapp.SourceObjectWriteResult{}, ErrIntegrity
	}
	var extra [1]byte
	count, readErr := body.Read(extra[:])
	if count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		store.cleanup(ctx, identity)
		return knowledgeapp.SourceObjectWriteResult{}, knowledgeapp.ErrInvalid
	}
	if info.VersionID == "" {
		store.cleanup(ctx, identity)
		return knowledgeapp.SourceObjectWriteResult{}, fmt.Errorf("%w: object version identity is required", ErrConfiguration)
	}
	return knowledgeapp.SourceObjectWriteResult{Identity: identity, Created: true}, nil
}

func (store *Store) Open(ctx context.Context, identity knowledgeapp.DocumentObjectIdentity) (io.ReadCloser, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	options := minio.GetObjectOptions{VersionID: identity.Version, ServerSideEncryption: store.sse, Checksum: true}
	info, err := store.client.StatObject(ctx, store.bucket, identity.Key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: stat object: %v", ErrUnavailable, err)
	}
	if !matches(info, identity.Size, identity.ContentSHA256) || info.VersionID != identity.Version {
		return nil, ErrIntegrity
	}
	object, err := store.client.GetObject(ctx, store.bucket, identity.Key, options)
	if err != nil {
		return nil, fmt.Errorf("%w: get object: %v", ErrUnavailable, err)
	}
	return object, nil
}

func (store *Store) Delete(ctx context.Context, identity knowledgeapp.DocumentObjectIdentity) error {
	if err := validateIdentity(identity); err != nil {
		return err
	}
	if err := store.client.RemoveObject(ctx, store.bucket, identity.Key, minio.RemoveObjectOptions{VersionID: identity.Version}); err != nil {
		return fmt.Errorf("%w: delete object version: %v", ErrUnavailable, err)
	}
	return nil
}

func (store *Store) existing(ctx context.Context, key string, size int64, digest [sha256.Size]byte) (knowledgeapp.DocumentObjectIdentity, error) {
	info, err := store.client.StatObject(ctx, store.bucket, key, minio.StatObjectOptions{ServerSideEncryption: store.sse})
	if err != nil {
		return knowledgeapp.DocumentObjectIdentity{}, fmt.Errorf("%w: stat existing object: %v", ErrUnavailable, err)
	}
	if info.VersionID == "" {
		return knowledgeapp.DocumentObjectIdentity{}, fmt.Errorf("%w: object version identity is required", ErrConfiguration)
	}
	if !matches(info, size, digest) {
		return knowledgeapp.DocumentObjectIdentity{}, ErrConflict
	}
	return knowledgeapp.DocumentObjectIdentity{Key: key, Version: info.VersionID, Size: info.Size, ContentSHA256: digest}, nil
}

func (store *Store) cleanup(ctx context.Context, identity knowledgeapp.DocumentObjectIdentity) {
	if identity.Version != "" {
		_ = store.client.RemoveObject(ctx, store.bucket, identity.Key, minio.RemoveObjectOptions{VersionID: identity.Version})
	}
}

func matches(info minio.ObjectInfo, size int64, digest [sha256.Size]byte) bool {
	stored := info.Metadata.Get("X-Amz-Meta-Spyglass-Sha256")
	if stored == "" {
		stored = info.UserMetadata["spyglass-sha256"]
	}
	return info.Size == size && strings.EqualFold(stored, hex.EncodeToString(digest[:]))
}

func validateIdentity(identity knowledgeapp.DocumentObjectIdentity) error {
	if identity.Key == "" || len(identity.Key) > knowledgedomain.MaximumObjectKey || !strings.HasPrefix(identity.Key, "accounts/") || strings.Contains(identity.Key, "..") || identity.Version == "" || identity.Size <= 0 || identity.Size > knowledgedomain.MaximumDocumentBytes || identity.ContentSHA256 == ([sha256.Size]byte{}) {
		return knowledgeapp.ErrInvalid
	}
	return nil
}

func equalDigest(value []byte, expected [sha256.Size]byte) bool {
	return len(value) == sha256.Size && string(value) == string(expected[:])
}

func isPrecondition(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "PreconditionFailed" || response.Code == "ConditionalRequestConflict"
}

func normalizeMediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

var _ knowledgeapp.DocumentObjectStore = (*Store)(nil)
