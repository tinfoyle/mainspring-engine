package prototypebundle

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestLoadAcceptsExactBundleAndRejectsExtrasAndLinks(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	body := []byte("retained prototype text\n")
	digest := sha256.Sum256(body)
	bundle, err := prototypemigration.NewTransformer().Transform(ids.AccountID("b1000000-0000-4000-8000-000000000101"), prototypemigration.Snapshot{
		TenantID: "b2000000-0000-4000-8000-000000000102", Checkpoint: "00000016/B374D848",
		Inventory: prototypemigration.Inventory{DocumentRevisions: 1},
		Documents: []prototypemigration.LegacyDocumentRevision{{DocumentID: "b3000000-0000-4000-8000-000000000103", RevisionID: "b4000000-0000-4000-8000-000000000104", Revision: 1, Name: "record.txt", MediaType: "text/plain", Content: body, StoredSHA256: digest[:], Status: "ready", CreatedAt: now}},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "bundle")
	writeLoaderFixture(t, directory, bundle)
	loaded, err := Load(directory)
	if err != nil || loaded.Manifest.ContentSHA256 != bundle.Manifest.ContentSHA256 || string(loaded.Objects[bundle.Manifest.Documents[0].ObjectPath]) != string(body) {
		t.Fatalf("loaded=%+v err=%v", loaded.Manifest, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "operator-note.txt"), []byte("not sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory); !errors.Is(err, prototypemigration.ErrManifest) {
		t.Fatalf("extra file error=%v", err)
	}
	if err := os.Remove(filepath.Join(directory, "operator-note.txt")); err != nil {
		t.Fatal(err)
	}
	objectPath := filepath.Join(directory, filepath.FromSlash(bundle.Manifest.Documents[0].ObjectPath))
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, objectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(directory); !errors.Is(err, prototypemigration.ErrManifest) {
		t.Fatalf("linked object error=%v", err)
	}
}

func writeLoaderFixture(t *testing.T, directory string, bundle prototypemigration.Bundle) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(bundle.Manifest)
	unresolved, _ := json.Marshal(bundle.Manifest.Unresolved)
	checkpoint, _ := json.Marshal(rollbackCheckpoint{Version: bundle.Manifest.Version, AccountID: string(bundle.Manifest.AccountID), TenantID: bundle.Manifest.Source.TenantID, SourceCheckpoint: bundle.Manifest.Source.Checkpoint, ManifestSHA256: bundle.Manifest.ContentSHA256})
	for name, body := range map[string][]byte{"manifest.json": manifest, "unresolved.json": unresolved, "rollback-checkpoint.json": checkpoint} {
		if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range bundle.Objects {
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
