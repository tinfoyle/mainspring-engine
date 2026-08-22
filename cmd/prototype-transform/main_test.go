package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
)

func TestWriteBundlePublishesPrivateCompleteArtifact(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	content := []byte("prototype text")
	digest := sha256.Sum256(content)
	bundle, err := prototypemigration.NewTransformer().Transform("10000000-0000-4000-8000-000000000001", prototypemigration.Snapshot{
		TenantID: "20000000-0000-4000-8000-000000000002", Checkpoint: "checkpoint", Inventory: prototypemigration.Inventory{DocumentRevisions: 1},
		Documents: []prototypemigration.LegacyDocumentRevision{{DocumentID: "30000000-0000-4000-8000-000000000003", RevisionID: "40000000-0000-4000-8000-000000000004", Revision: 1, Name: "record.txt", MediaType: "text/plain", Content: content, StoredSHA256: digest[:], Status: "ready", CreatedAt: now}},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	if err := writeBundle(output, bundle); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "unresolved.json", "rollback-checkpoint.json", bundle.Manifest.Documents[0].ObjectPath} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(name))); err != nil {
			t.Fatalf("artifact %s: %v", name, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(output, "rollback-checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint rollbackCheckpoint
	if err := json.Unmarshal(body, &checkpoint); err != nil || checkpoint.DestinationWrites || checkpoint.ManifestSHA256 != bundle.Manifest.ContentSHA256 {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	if err := writeBundle(output, bundle); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate output error=%v", err)
	}
}

func TestRunRejectsMissingArgumentsAndCredentialWithoutDisclosure(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(nil, func(string) string { return "" }, &stdout, &stderr); err == nil || stdout.Len() != 0 {
		t.Fatalf("missing argument error=%v stdout=%q", err, stdout.String())
	}
	stdout.Reset()
	err := run([]string{"-tenant-id", "20000000-0000-4000-8000-000000000002", "-account-id", "10000000-0000-4000-8000-000000000001", "-output", "bundle"}, func(string) string { return "" }, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "SPYGLASS_PROTOTYPE_DATABASE_URL") || stdout.Len() != 0 {
		t.Fatalf("credential error=%v stdout=%q", err, stdout.String())
	}
}
