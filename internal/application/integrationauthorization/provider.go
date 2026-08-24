// Package integrationauthorization owns the product-controlled provider
// authorization lifecycle. Provider protocol adapters return only the refresh
// material needed by the credential store; access tokens and raw responses
// never cross this boundary.
package integrationauthorization

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

var (
	ErrInvalid             = errors.New("integration authorization request is invalid")
	ErrNotFound            = errors.New("integration authorization session was not found")
	ErrConflict            = errors.New("integration authorization request conflicts with durable state")
	ErrRepository          = errors.New("integration authorization repository is unavailable")
	ErrProviderUnavailable = errors.New("integration authorization provider is unavailable")
	ErrProviderRejected    = errors.New("integration authorization provider rejected the request")
	ErrScopeMismatch       = errors.New("integration authorization provider scope does not match")
)

type SecretGenerator interface{ New() ([]byte, error) }

type RandomSecrets struct{}

func (RandomSecrets) New() ([]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(encoded, raw[:])
	for index := range raw {
		raw[index] = 0
	}
	return encoded, nil
}

type ExchangeRequest struct {
	Code         []byte
	PKCEVerifier []byte
	RedirectURI  string
}

type RefreshCredential struct {
	RefreshToken []byte
}

func (value *RefreshCredential) Close() {
	for index := range value.RefreshToken {
		value.RefreshToken[index] = 0
	}
	value.RefreshToken = nil
}

type Provider interface {
	AuthorizationURL(state, pkceChallenge []byte, redirectURI string) (string, error)
	Exchange(context.Context, ExchangeRequest) (RefreshCredential, error)
	Revoke(context.Context, []byte) error
}
