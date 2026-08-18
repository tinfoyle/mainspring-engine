// Package toolcontext signs one short-lived broker-to-router capability
// dispatch. It is intentionally not interchangeable with browser route
// context or runner Pod identity.
package toolcontext

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	HeaderName      = "X-Spyglass-Tool-Context"
	TokenType       = "SPYGLASS-TOOL"
	Algorithm       = "HS256"
	Version         = 1
	Audience        = "spyglass:tool-router"
	MinimumKeyBytes = 32
	DefaultLifetime = 10 * time.Second
	MaximumLifetime = 15 * time.Second
	MaxTokenBytes   = 16 << 10
)

var (
	ErrInvalid      = errors.New("tool context is invalid")
	ErrSignature    = errors.New("tool context signature is invalid")
	ErrExpired      = errors.New("tool context has expired")
	ErrRequest      = errors.New("tool context does not bind this request")
	ErrUnknownKey   = errors.New("tool context signing key is unknown")
	ErrReplay       = errors.New("tool context was already consumed")
	ErrReceiptStore = errors.New("tool context receipt store is unavailable")
	capability      = regexp.MustCompile(`^[a-z][a-z0-9.:/-]{0,127}$`)
)

type Clock interface{ Now() time.Time }

type Authority struct {
	RequestID    string        `json:"request_id"`
	AccountID    ids.AccountID `json:"account_id"`
	InvocationID string        `json:"invocation_id"`
	PodUID       string        `json:"pod_uid"`
	OperationID  string        `json:"operation_id"`
	Capability   string        `json:"capability"`
}

type Claims struct {
	Issuer    string               `json:"iss"`
	Audience  string               `json:"aud"`
	IssuedAt  int64                `json:"iat"`
	ExpiresAt int64                `json:"exp"`
	Authority Authority            `json:"authority"`
	Binding   routecontext.Binding `json:"binding"`
	KeyID     string               `json:"-"`
}

type header struct {
	Version int    `json:"v"`
	KeyID   string `json:"kid"`
	Type    string `json:"typ"`
	Alg     string `json:"alg"`
}

type Signer struct {
	issuer   string
	keyID    string
	key      []byte
	lifetime time.Duration
	clock    Clock
}

func NewSigner(issuer, keyID string, key []byte, lifetime time.Duration, clock Clock) (*Signer, error) {
	issuer, keyID = strings.TrimSpace(issuer), strings.TrimSpace(keyID)
	if issuer == "" || len(issuer) > 100 || keyID == "" || len(keyID) > 100 || len(key) < MinimumKeyBytes || lifetime <= 0 || lifetime > MaximumLifetime || clock == nil {
		return nil, ErrInvalid
	}
	return &Signer{issuer: issuer, keyID: keyID, key: append([]byte(nil), key...), lifetime: lifetime, clock: clock}, nil
}

func (s *Signer) Issue(authority Authority, binding routecontext.Binding) (string, error) {
	if !validAuthority(authority) || !validBinding(binding) {
		return "", ErrInvalid
	}
	now := s.clock.Now().UTC().Truncate(time.Second)
	claims := Claims{Issuer: s.issuer, Audience: Audience, IssuedAt: now.Unix(), ExpiresAt: now.Add(s.lifetime).Unix(), Authority: authority, Binding: binding}
	headerJSON, err := json.Marshal(header{Version: Version, KeyID: s.keyID, Type: TokenType, Alg: Algorithm})
	if err != nil {
		return "", ErrInvalid
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalid
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	input := headerPart + "." + claimsPart
	return input + "." + base64.RawURLEncoding.EncodeToString(sign(s.key, input)), nil
}

type Verifier struct {
	issuer      string
	keys        map[string][]byte
	maxLifetime time.Duration
	clockSkew   time.Duration
	clock       Clock
}

type ReceiptStore interface {
	Consume(context.Context, Claims, time.Time) error
}

type Acceptor struct {
	verifier *Verifier
	receipts ReceiptStore
	clock    Clock
}

func NewAcceptor(verifier *Verifier, receipts ReceiptStore, clock Clock) (*Acceptor, error) {
	if verifier == nil || receipts == nil || clock == nil {
		return nil, ErrInvalid
	}
	return &Acceptor{verifier: verifier, receipts: receipts, clock: clock}, nil
}

func (a *Acceptor) Accept(ctx context.Context, token string, binding routecontext.Binding) (Claims, error) {
	claims, err := a.verifier.Verify(token, binding)
	if err != nil {
		return Claims{}, err
	}
	if err := a.receipts.Consume(ctx, claims, a.clock.Now().UTC()); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func NewVerifier(issuer string, keys map[string][]byte, maxLifetime, clockSkew time.Duration, clock Clock) (*Verifier, error) {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" || len(keys) == 0 || maxLifetime <= 0 || maxLifetime > MaximumLifetime || clockSkew < 0 || clockSkew > 5*time.Second || clock == nil {
		return nil, ErrInvalid
	}
	copyKeys := make(map[string][]byte, len(keys))
	for keyID, key := range keys {
		keyID = strings.TrimSpace(keyID)
		if keyID == "" || len(keyID) > 100 || len(key) < MinimumKeyBytes {
			return nil, ErrInvalid
		}
		copyKeys[keyID] = append([]byte(nil), key...)
	}
	return &Verifier{issuer: issuer, keys: copyKeys, maxLifetime: maxLifetime, clockSkew: clockSkew, clock: clock}, nil
}

func (v *Verifier) Verify(token string, binding routecontext.Binding) (Claims, error) {
	if token == "" || len(token) > MaxTokenBytes || !validBinding(binding) {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var tokenHeader header
	if err := decodeStrict(headerBytes, &tokenHeader); err != nil || tokenHeader.Version != Version || tokenHeader.Type != TokenType || tokenHeader.Alg != Algorithm {
		return Claims{}, ErrInvalid
	}
	key, exists := v.keys[tokenHeader.KeyID]
	if !exists {
		return Claims{}, ErrUnknownKey
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size || !hmac.Equal(signature, sign(key, parts[0]+"."+parts[1])) {
		return Claims{}, ErrSignature
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decodeStrict(claimsBytes, &claims); err != nil || claims.Issuer != v.issuer || claims.Audience != Audience || !validAuthority(claims.Authority) || !validBinding(claims.Binding) {
		return Claims{}, ErrInvalid
	}
	now := v.clock.Now().UTC()
	issuedAt, expiresAt := time.Unix(claims.IssuedAt, 0), time.Unix(claims.ExpiresAt, 0)
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > v.maxLifetime || issuedAt.After(now.Add(v.clockSkew)) {
		return Claims{}, ErrInvalid
	}
	if !expiresAt.After(now) {
		return Claims{}, ErrExpired
	}
	if claims.Binding != binding {
		return Claims{}, ErrRequest
	}
	claims.KeyID = tokenHeader.KeyID
	return claims, nil
}

func validAuthority(value Authority) bool {
	return ids.Validate(value.RequestID) == nil && ids.Validate(string(value.AccountID)) == nil && ids.Validate(value.InvocationID) == nil && ids.Validate(value.PodUID) == nil && ids.Validate(value.OperationID) == nil && capability.MatchString(value.Capability)
}

func validBinding(value routecontext.Binding) bool {
	if _, err := routecontext.Bind(value.Method, value.Target, nil); err != nil || len(value.BodySHA256) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(value.BodySHA256); err != nil {
		return false
	}
	if value.HeadersSHA256 != "" {
		if len(value.HeadersSHA256) != sha256.Size*2 {
			return false
		}
		if _, err := hex.DecodeString(value.HeadersSHA256); err != nil {
			return false
		}
	}
	return true
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func sign(key []byte, input string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(input))
	return mac.Sum(nil)
}
