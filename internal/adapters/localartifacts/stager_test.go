package localartifacts

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
)

const stagedExportID = "ea000000-0000-4000-8000-000000000001"

func TestStagerCreatesPrivateBoundedLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := stager.Create(context.Background(), stagedExportID)
	if err != nil {
		t.Fatal(err)
	}
	stage := value.(*fileStage)
	if filepath.Dir(stage.path) != root {
		t.Fatalf("stage escaped root: %q", stage.path)
	}
	info, err := os.Lstat(stage.path)
	if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("stage info=%+v err=%v", info, err)
	}
	if _, err := stage.Write([]byte("private archive")); err != nil {
		t.Fatal(err)
	}
	reader, err := stage.Rewind()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "private archive" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if _, err := stage.Write([]byte("mutation")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("post-seal write err=%v", err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage survived cleanup: %v", err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatalf("cleanup replay: %v", err)
	}
}

func TestStagerRejectsUnsafeRootIdentityAndInput(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("group-readable root err=%v", err)
	}
	if _, err := New("relative/path"); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("relative root err=%v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := stager.Create(context.Background(), stagedExportID); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("replaced root err=%v", err)
	}
	if _, err := stager.Create(context.Background(), "not-an-id"); !errors.Is(err, accountexport.ErrInvalid) {
		t.Fatalf("invalid export ID err=%v", err)
	}
}

func TestStagerHonorsCanceledContextWithoutResidue(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stager.Create(ctx, stagedExportID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create err=%v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging residue=%v err=%v", entries, err)
	}
}
