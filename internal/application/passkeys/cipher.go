package passkeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Envelope struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type Cipher struct {
	keys          map[int]cipher.AEAD
	activeVersion int
}

func NewCipher(key []byte, keyVersion int) (*Cipher, error) {
	return NewCipherKeyring(map[int][]byte{keyVersion: key}, keyVersion)
}

// NewCipherKeyring constructs an envelope cipher that writes only with the
// active key while retaining explicitly configured older keys for decryption.
// Versions are persisted with each envelope and are never guessed.
func NewCipherKeyring(keys map[int][]byte, activeVersion int) (*Cipher, error) {
	if activeVersion <= 0 || len(keys) == 0 {
		return nil, errors.New("passkey encryption requires a positive active version and at least one key")
	}
	result := &Cipher{keys: make(map[int]cipher.AEAD, len(keys)), activeVersion: activeVersion}
	for version, key := range keys {
		if version <= 0 || len(key) != 32 {
			return nil, errors.New("passkey encryption key versions must be positive and keys must be 32 bytes")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		result.keys[version] = aead
	}
	if _, exists := result.keys[activeVersion]; !exists {
		return nil, errors.New("passkey encryption active version is not present in the keyring")
	}
	return result, nil
}

func (c *Cipher) Seal(label string, plaintext []byte) (Envelope, error) {
	aead := c.keys[c.activeVersion]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	return Envelope{Ciphertext: aead.Seal(nil, nonce, plaintext, []byte(label)), Nonce: nonce, KeyVersion: c.activeVersion}, nil
}

func (c *Cipher) Open(label string, envelope Envelope) ([]byte, error) {
	aead, exists := c.keys[envelope.KeyVersion]
	if !exists || len(envelope.Nonce) != aead.NonceSize() {
		return nil, errors.New("passkey envelope key or nonce is invalid")
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(label))
	if err != nil {
		return nil, errors.New("passkey envelope authentication failed")
	}
	return plaintext, nil
}

func (c *Cipher) ActiveVersion() int { return c.activeVersion }
