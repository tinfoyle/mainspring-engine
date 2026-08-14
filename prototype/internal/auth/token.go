package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewToken() (string, []byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	return raw, HashToken(raw), nil
}

func HashToken(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}

func CSRFToken(sessionToken string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func CheckCSRF(sessionToken, candidate string, secret []byte) bool {
	expected := CSRFToken(sessionToken, secret)
	return hmac.Equal([]byte(expected), []byte(candidate))
}
