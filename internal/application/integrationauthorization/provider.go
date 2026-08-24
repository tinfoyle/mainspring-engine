// Package integrationauthorization owns the product-controlled provider
// authorization lifecycle. Provider protocol adapters return only the refresh
// material needed by the credential store; access tokens and raw responses
// never cross this boundary.
package integrationauthorization

import (
	"context"
	"errors"
)

var (
	ErrInvalid             = errors.New("integration authorization request is invalid")
	ErrProviderUnavailable = errors.New("integration authorization provider is unavailable")
	ErrProviderRejected    = errors.New("integration authorization provider rejected the request")
	ErrScopeMismatch       = errors.New("integration authorization provider scope does not match")
)

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
