// Package aescursor seals incremental provider cursors with a cell-scoped
// mounted AES-256 key. Associated data prevents replay across Accounts or
// source grants; the key and plaintext never enter PostgreSQL.
package aescursor

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const formatVersion byte = 1

var (
	ErrConfiguration = errors.New("integration cursor cipher configuration is invalid")
	ErrInvalid       = errors.New("integration cursor ciphertext is invalid")
)

type Cipher struct{ aead cipher.AEAD }

func New(keyFile string) (*Cipher, error) {
	keyFile = strings.TrimSpace(keyFile)
	if keyFile == "" || !filepath.IsAbs(keyFile) {
		return nil, ErrConfiguration
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(keyFile))
	if err != nil || resolved != filepath.Clean(keyFile) {
		return nil, ErrConfiguration
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: open key", ErrConfiguration)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("%w: key permissions", ErrConfiguration)
	}
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil || len(key) != 32 {
		wipe(key)
		return nil, fmt.Errorf("%w: key must be exactly 32 bytes", ErrConfiguration)
	}
	return newFromKey(key)
}

// NewLocalFixture returns the stable cipher used only with the network-free
// mock connector. Production composition always requires New and a restrictive
// mounted key file.
func NewLocalFixture() (*Cipher, error) {
	key := sha256.Sum256([]byte("spyglass/local/mock-integration-source-cursor/v1"))
	return newFromKey(key[:])
}

func newFromKey(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	wipe(key)
	if err != nil {
		return nil, ErrConfiguration
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &Cipher{aead: aead}, nil
}

func (value *Cipher) Open(ctx context.Context, accountID ids.AccountID, grantID ids.BaselineSourceGrantID, ciphertext []byte, digest [sha256.Size]byte) ([]byte, error) {
	if value == nil || value.aead == nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(grantID)) != nil || ctx.Err() != nil {
		return nil, ErrInvalid
	}
	if len(ciphertext) == 0 && digest == [sha256.Size]byte{} {
		return nil, nil
	}
	minimum := 1 + value.aead.NonceSize() + value.aead.Overhead()
	if len(ciphertext) < minimum || len(ciphertext) > integrationsync.MaximumCursorCiphertextBytes || ciphertext[0] != formatVersion || digest == [sha256.Size]byte{} {
		return nil, ErrInvalid
	}
	nonce := ciphertext[1 : 1+value.aead.NonceSize()]
	plaintext, err := value.aead.Open(nil, nonce, ciphertext[1+value.aead.NonceSize():], associatedData(accountID, grantID))
	if err != nil || len(plaintext) == 0 || len(plaintext) > integrationsync.MaximumCursorBytes || sha256.Sum256(plaintext) != digest {
		wipe(plaintext)
		return nil, ErrInvalid
	}
	return plaintext, nil
}

func (value *Cipher) Seal(ctx context.Context, accountID ids.AccountID, grantID ids.BaselineSourceGrantID, plaintext []byte) ([]byte, [sha256.Size]byte, error) {
	if value == nil || value.aead == nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(grantID)) != nil ||
		ctx.Err() != nil || len(plaintext) == 0 || len(plaintext) > integrationsync.MaximumCursorBytes {
		return nil, [sha256.Size]byte{}, ErrInvalid
	}
	nonce := make([]byte, value.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, [sha256.Size]byte{}, ErrInvalid
	}
	ciphertext := make([]byte, 1, 1+len(nonce)+len(plaintext)+value.aead.Overhead())
	ciphertext[0] = formatVersion
	ciphertext = append(ciphertext, nonce...)
	ciphertext = value.aead.Seal(ciphertext, nonce, plaintext, associatedData(accountID, grantID))
	if len(ciphertext) > integrationsync.MaximumCursorCiphertextBytes {
		wipe(ciphertext)
		return nil, [sha256.Size]byte{}, ErrInvalid
	}
	return ciphertext, sha256.Sum256(plaintext), nil
}

func associatedData(accountID ids.AccountID, grantID ids.BaselineSourceGrantID) []byte {
	return []byte("spyglass/integration-cursor/v1/" + string(accountID) + "/" + string(grantID))
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ integrationsync.CursorCipher = (*Cipher)(nil)
