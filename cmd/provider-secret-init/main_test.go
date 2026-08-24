package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeForOwnerCreatesAndPreservesRestrictedMaterial(t *testing.T) {
	root := filepath.Join(t.TempDir(), "provider-secret")
	if err := initializeForOwner(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}

	vaultInfo, err := os.Stat(filepath.Join(root, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if !vaultInfo.IsDir() || vaultInfo.Mode().Perm() != 0o700 {
		t.Fatalf("vault mode = %v", vaultInfo.Mode())
	}

	keyPath := filepath.Join(root, "key")
	first, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key length/mode = %d/%v", len(first), keyInfo.Mode())
	}

	if err := initializeForOwner(root, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("existing key was rotated")
	}
}

func TestInitializeForOwnerRejectsSymlinkKey(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "provider-secret")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "key")); err != nil {
		t.Fatal(err)
	}

	if err := initializeForOwner(root, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("expected symlink key rejection")
	}
}
