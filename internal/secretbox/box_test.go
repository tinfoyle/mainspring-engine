package secretbox

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBoxRoundTripAndRandomNonce(t *testing.T) {
	box, err := New([]byte("a-development-key-that-is-at-least-thirty-two-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	one, err := box.Encrypt("app-password")
	if err != nil {
		t.Fatal(err)
	}
	two, err := box.Encrypt("app-password")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("encryption reused a nonce")
	}
	plaintext, err := box.Decrypt(one)
	if err != nil || plaintext != "app-password" {
		t.Fatalf("Decrypt() = %q, %v", plaintext, err)
	}
}

func TestBoxRejectsTampering(t *testing.T) {
	box, _ := New([]byte("a-development-key-that-is-at-least-thirty-two-bytes"))
	ciphertext, _ := box.Encrypt("app-password")
	encoded := strings.TrimPrefix(ciphertext, "v1:")
	payload, _ := base64.RawURLEncoding.DecodeString(encoded)
	payload[len(payload)-1] ^= 1
	ciphertext = "v1:" + base64.RawURLEncoding.EncodeToString(payload)
	if _, err := box.Decrypt(ciphertext); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}
