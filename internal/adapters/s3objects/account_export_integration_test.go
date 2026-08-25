package s3objects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestS3ExportStorePublishesAndDeletesExactVersion(t *testing.T) {
	endpoint := os.Getenv("SPYGLASS_S3_TEST_ENDPOINT")
	bucket := os.Getenv("SPYGLASS_EXPORT_S3_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("Account-export S3 integration environment is not configured")
	}
	store, err := NewExport(Config{
		Endpoint: endpoint, Bucket: bucket,
		AccessKey: os.Getenv("SPYGLASS_EXPORT_S3_TEST_ACCESS_KEY"), SecretKey: os.Getenv("SPYGLASS_EXPORT_S3_TEST_SECRET_KEY"),
		ServerSideEncryption: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.VerifyReadOnly(ctx); err != nil {
		t.Fatal(err)
	}
	body := []byte("exact deterministic account export archive")
	write := accountexport.ArtifactWrite{ExportID: ids.RandomGenerator{}.New(), Bytes: int64(len(body)), SHA256: sha256.Sum256(body), Body: bytes.NewReader(body)}
	artifact, err := store.Publish(ctx, write)
	if err != nil || artifact.Reference == "" {
		t.Fatalf("publish artifact=%+v err=%v", artifact, err)
	}
	defer store.Delete(context.Background(), artifact)
	write.Body = bytes.NewReader(body)
	replayed, err := store.Publish(ctx, write)
	if err != nil || replayed != artifact {
		t.Fatalf("replay artifact=%+v want=%+v err=%v", replayed, artifact, err)
	}
	opened, err := store.Open(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	read, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(read, body) {
		t.Fatalf("open body=%q read=%v close=%v", read, readErr, closeErr)
	}
	if err := store.Delete(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, artifact); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}
