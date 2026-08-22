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
