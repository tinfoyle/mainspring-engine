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
	aead       cipher.AEAD
	keyVersion int
}

func NewCipher(key []byte, keyVersion int) (*Cipher, error) {
	if len(key) != 32 || keyVersion <= 0 {
		return nil, errors.New("passkey encryption requires a 32-byte key and positive key version")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *Cipher) Seal(label string, plaintext []byte) (Envelope, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, err
	}
	return Envelope{Ciphertext: c.aead.Seal(nil, nonce, plaintext, []byte(label)), Nonce: nonce, KeyVersion: c.keyVersion}, nil
}

func (c *Cipher) Open(label string, envelope Envelope) ([]byte, error) {
	if envelope.KeyVersion != c.keyVersion || len(envelope.Nonce) != c.aead.NonceSize() {
		return nil, errors.New("passkey envelope key or nonce is invalid")
	}
	plaintext, err := c.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(label))
	if err != nil {
		return nil, errors.New("passkey envelope authentication failed")
	}
	return plaintext, nil
}
