package s3objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
)

func TestS3StoreStreamsImmutableVersionedObjects(t *testing.T) {
	endpoint := os.Getenv("SPYGLASS_S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("SPYGLASS_S3_TEST_ENDPOINT is not configured")
	}
	store, err := New(Config{Endpoint: endpoint, Bucket: os.Getenv("SPYGLASS_S3_TEST_BUCKET"), AccessKey: os.Getenv("SPYGLASS_S3_TEST_ACCESS_KEY"), SecretKey: os.Getenv("SPYGLASS_S3_TEST_SECRET_KEY"), ServerSideEncryption: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte("exact immutable source bytes")
	write := knowledgeapp.SourceObjectWrite{AccountID: "f1000000-0000-4000-8000-000000000001", DocumentID: "f2000000-0000-4000-8000-000000000002", RevisionID: "f3000000-0000-4000-8000-000000000003", MediaType: "text/plain", Size: int64(len(body)), ContentSHA256: sha256.Sum256(body), Body: bytes.NewReader(body)}
	result, err := store.PutImmutable(ctx, write)
	identity := result.Identity
	if err != nil || !result.Created || identity.Version == "" {
		t.Fatalf("put result=%+v err=%v", result, err)
	}
	defer store.Delete(context.Background(), identity)
	write.Body = bytes.NewReader(body)
	replayed, err := store.PutImmutable(ctx, write)
	if err != nil || replayed.Created || replayed.Identity != identity {
		t.Fatalf("replay result=%+v want=%+v err=%v", replayed, identity, err)
	}
	reader, err := store.Open(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	readBody, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(readBody, body) {
		t.Fatalf("read=%q readErr=%v closeErr=%v", readBody, readErr, closeErr)
	}
	conflictBody := []byte("different immutable source")
	write.Body, write.Size, write.ContentSHA256 = bytes.NewReader(conflictBody), int64(len(conflictBody)), sha256.Sum256(conflictBody)
	if _, err := store.PutImmutable(ctx, write); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting immutable put err=%v", err)
	}
	extractedBody := []byte("normalized extracted text")
	extracted, err := store.PutExtractedImmutable(ctx, knowledgeapp.ExtractedObjectWrite{AccountID: write.AccountID, DocumentID: write.DocumentID, RevisionID: write.RevisionID, Size: int64(len(extractedBody)), ContentSHA256: sha256.Sum256(extractedBody), Body: bytes.NewReader(extractedBody)})
	if err != nil || !extracted.Created || extracted.Identity.Version == "" {
		t.Fatalf("put extracted result=%+v err=%v", extracted, err)
	}
	defer store.Delete(context.Background(), extracted.Identity)
	extractedReader, err := store.Open(ctx, extracted.Identity)
	if err != nil {
		t.Fatal(err)
	}
	readExtracted, readExtractedErr := io.ReadAll(extractedReader)
	closeExtractedErr := extractedReader.Close()
	if readExtractedErr != nil || closeExtractedErr != nil || !bytes.Equal(readExtracted, extractedBody) {
		t.Fatalf("read extracted=%q readErr=%v closeErr=%v", readExtracted, readExtractedErr, closeExtractedErr)
	}
	if err := store.Delete(ctx, identity); err != nil {
		t.Fatal(err)
	}
}

func TestS3StoreHonorsAdmissionAndWorkerCredentials(t *testing.T) {
	endpoint := os.Getenv("SPYGLASS_S3_TEST_ENDPOINT")
	appAccess := os.Getenv("SPYGLASS_S3_APP_TEST_ACCESS_KEY")
	workerAccess := os.Getenv("SPYGLASS_S3_WORKER_TEST_ACCESS_KEY")
	if endpoint == "" || appAccess == "" || workerAccess == "" {
		t.Skip("scoped S3 test credentials are not configured")
	}
	bucket := os.Getenv("SPYGLASS_S3_TEST_BUCKET")
	app, err := New(Config{Endpoint: endpoint, Bucket: bucket, AccessKey: appAccess, SecretKey: os.Getenv("SPYGLASS_S3_APP_TEST_SECRET_KEY"), ServerSideEncryption: true})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := New(Config{Endpoint: endpoint, Bucket: bucket, AccessKey: workerAccess, SecretKey: os.Getenv("SPYGLASS_S3_WORKER_TEST_SECRET_KEY"), ServerSideEncryption: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := app.Verify(ctx); err != nil {
		t.Fatalf("app credential verify: %v", err)
	}
	if err := worker.Verify(ctx); err != nil {
		t.Fatalf("worker credential verify: %v", err)
	}
	body := []byte("scoped immutable source")
	request := knowledgeapp.SourceObjectWrite{AccountID: "f4000000-0000-4000-8000-000000000004", DocumentID: "f5000000-0000-4000-8000-000000000005", RevisionID: "f6000000-0000-4000-8000-000000000006", MediaType: "text/plain", Size: int64(len(body)), ContentSHA256: sha256.Sum256(body), Body: bytes.NewReader(body)}
	source, err := app.PutImmutable(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Delete(context.Background(), source.Identity)
	reader, err := worker.Open(ctx, source.Identity)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("worker source read: %v", errors.Join(readErr, closeErr))
	}
	extractedBody := []byte("scoped extracted text")
	extractedRequest := knowledgeapp.ExtractedObjectWrite{AccountID: request.AccountID, DocumentID: request.DocumentID, RevisionID: request.RevisionID, Size: int64(len(extractedBody)), ContentSHA256: sha256.Sum256(extractedBody), Body: bytes.NewReader(extractedBody)}
	if _, err := app.PutExtractedImmutable(ctx, extractedRequest); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("app extracted write err=%v", err)
	}
	extractedRequest.Body = bytes.NewReader(extractedBody)
	extracted, err := worker.PutExtractedImmutable(ctx, extractedRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Delete(context.Background(), extracted.Identity)
}
