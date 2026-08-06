package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Box struct {
	aead cipher.AEAD
}

func New(masterKey []byte) (*Box, error) {
	if len(masterKey) < 32 {
		return nil, errors.New("credential encryption key must contain at least 32 bytes")
	}
	key := sha256.Sum256(masterKey)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential encryption: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plaintext string) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("credential encryption is not configured")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := b.aead.Seal(nil, nonce, []byte(plaintext), []byte("mainspring-credentials-v1"))
	payload := append(nonce, ciphertext...)
	return "v1:" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (b *Box) Decrypt(encoded string) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("credential encryption is not configured")
	}
	version, payloadText, found := strings.Cut(strings.TrimSpace(encoded), ":")
	if !found || version != "v1" {
		return "", errors.New("unsupported credential ciphertext version")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil || len(payload) < b.aead.NonceSize() {
		return "", errors.New("invalid credential ciphertext")
	}
	nonce, ciphertext := payload[:b.aead.NonceSize()], payload[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, []byte("mainspring-credentials-v1"))
	if err != nil {
		return "", errors.New("decrypt credentials: authentication failed")
	}
	return string(plaintext), nil
}
