package aescursor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	testAccount = "c1000000-0000-4000-8000-000000000001"
	testGrant   = "c2000000-0000-4000-8000-000000000002"
)

func TestCipherBindsCursorToAccountAndGrant(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "cursor.key")
	if err := os.WriteFile(keyFile, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := New(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("provider-page-token")
	ciphertext, digest, err := value.Seal(context.Background(), testAccount, testGrant, plaintext)
	if err != nil || string(ciphertext) == string(plaintext) {
		t.Fatalf("seal ciphertext=%q err=%v", ciphertext, err)
	}
	opened, err := value.Open(context.Background(), testAccount, testGrant, ciphertext, digest)
	if err != nil || string(opened) != string(plaintext) {
		t.Fatalf("open plaintext=%q err=%v", opened, err)
	}
	if _, err := value.Open(context.Background(), testAccount, "c3000000-0000-4000-8000-000000000003", ciphertext, digest); err == nil {
		t.Fatal("cursor opened under another source grant")
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := value.Open(context.Background(), testAccount, testGrant, tampered, digest); err == nil {
		t.Fatal("tampered cursor opened")
	}
}

func TestCipherRejectsUnsafeKeyFile(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "cursor.key")
	if err := os.WriteFile(keyFile, []byte("0123456789abcdef0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(keyFile); err == nil {
		t.Fatal("world-readable key was accepted")
	}
}
