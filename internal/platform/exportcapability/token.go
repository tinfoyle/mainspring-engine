// Package exportcapability signs short-lived, revocable-by-state bearer
// capabilities for downloading one exact Account export artifact.
package exportcapability

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	Audience        = "spyglass:account-export-download"
	TokenType       = "SPYGLASS-ACCOUNT-EXPORT"
	Algorithm       = "HS256"
	Version         = 1
	MinimumKeyBytes = 32
	MaximumLifetime = 5 * time.Minute
	DefaultLifetime = 2 * time.Minute
	MaximumToken    = 8 << 10
)

var (
	ErrInvalid    = errors.New("Account export download capability is invalid")
	ErrSignature  = errors.New("Account export download capability signature is invalid")
	ErrExpired    = errors.New("Account export download capability has expired")
	ErrUnknownKey = errors.New("Account export download capability key is unknown")
	keyIDPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
)

type Clock interface{ Now() time.Time }

type Authority struct {
	AccountID      ids.AccountID `json:"account_id"`
	UserID         ids.UserID    `json:"user_id"`
	ExportID       string        `json:"export_id"`
	RequestVersion uint64        `json:"request_version"`
	ArtifactSHA256 string        `json:"artifact_sha256"`
	ArtifactBytes  int64         `json:"artifact_bytes"`
}

type Claims struct {
	Issuer    string    `json:"iss"`
	Audience  string    `json:"aud"`
	IssuedAt  int64     `json:"iat"`
	ExpiresAt int64     `json:"exp"`
	Authority Authority `json:"authority"`
	KeyID     string    `json:"-"`
}

type header struct {
	Version int    `json:"v"`
	KeyID   string `json:"kid"`
	Type    string `json:"typ"`
	Alg     string `json:"alg"`
}

type Signer struct {
	issuer, keyID string
	key           []byte
	lifetime      time.Duration
	clock         Clock
}

func NewSigner(issuer, keyID string, key []byte, lifetime time.Duration, clock Clock) (*Signer, error) {
	issuer, keyID = strings.TrimSpace(issuer), strings.TrimSpace(keyID)
	if !validIssuer(issuer) || !keyIDPattern.MatchString(keyID) || len(key) < MinimumKeyBytes || lifetime <= 0 || lifetime > MaximumLifetime || clock == nil {
		return nil, ErrInvalid
	}
	return &Signer{issuer: issuer, keyID: keyID, key: append([]byte(nil), key...), lifetime: lifetime, clock: clock}, nil
}

func (signer *Signer) Issue(authority Authority, notAfter time.Time) (string, time.Time, error) {
	if !validAuthority(authority) || notAfter.IsZero() {
		return "", time.Time{}, ErrInvalid
	}
	now := signer.clock.Now().UTC().Truncate(time.Second)
	expiresAt := now.Add(signer.lifetime)
	if notAfter.UTC().Before(expiresAt) {
		expiresAt = notAfter.UTC().Truncate(time.Second)
	}
	if !expiresAt.After(now) {
		return "", time.Time{}, ErrExpired
	}
	tokenHeader, err := json.Marshal(header{Version: Version, KeyID: signer.keyID, Type: TokenType, Alg: Algorithm})
	if err != nil {
		return "", time.Time{}, ErrInvalid
	}
	claimsJSON, err := json.Marshal(Claims{Issuer: signer.issuer, Audience: Audience, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), Authority: authority})
	if err != nil {
		return "", time.Time{}, ErrInvalid
	}
	headerPart := base64.RawURLEncoding.EncodeToString(tokenHeader)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	input := headerPart + "." + claimsPart
	return input + "." + base64.RawURLEncoding.EncodeToString(sign(signer.key, input)), expiresAt, nil
}

type Verifier struct {
	issuer      string
	keys        map[string][]byte
	maxLifetime time.Duration
	clockSkew   time.Duration
	clock       Clock
}

func NewVerifier(issuer string, keys map[string][]byte, maxLifetime, clockSkew time.Duration, clock Clock) (*Verifier, error) {
	if !validIssuer(strings.TrimSpace(issuer)) || len(keys) == 0 || maxLifetime <= 0 || maxLifetime > MaximumLifetime || clockSkew < 0 || clockSkew > 5*time.Second || clock == nil {
		return nil, ErrInvalid
	}
	copyKeys := make(map[string][]byte, len(keys))
	for keyID, key := range keys {
		if !keyIDPattern.MatchString(keyID) || len(key) < MinimumKeyBytes {
			return nil, ErrInvalid
		}
		copyKeys[keyID] = append([]byte(nil), key...)
	}
	return &Verifier{issuer: strings.TrimSpace(issuer), keys: copyKeys, maxLifetime: maxLifetime, clockSkew: clockSkew, clock: clock}, nil
}

func (verifier *Verifier) Verify(token string) (Claims, error) {
	if token == "" || len(token) > MaximumToken {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var tokenHeader header
	if decodeStrict(headerJSON, &tokenHeader) != nil || tokenHeader.Version != Version || tokenHeader.Type != TokenType || tokenHeader.Alg != Algorithm || !keyIDPattern.MatchString(tokenHeader.KeyID) {
		return Claims{}, ErrInvalid
	}
	key, ok := verifier.keys[tokenHeader.KeyID]
	if !ok {
		return Claims{}, ErrUnknownKey
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size || !hmac.Equal(signature, sign(key, parts[0]+"."+parts[1])) {
		return Claims{}, ErrSignature
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if decodeStrict(claimsJSON, &claims) != nil || claims.Issuer != verifier.issuer || claims.Audience != Audience || !validAuthority(claims.Authority) {
		return Claims{}, ErrInvalid
	}
	issuedAt, expiresAt := time.Unix(claims.IssuedAt, 0), time.Unix(claims.ExpiresAt, 0)
	now := verifier.clock.Now().UTC()
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > verifier.maxLifetime || issuedAt.After(now.Add(verifier.clockSkew)) {
		return Claims{}, ErrInvalid
	}
	if !expiresAt.After(now) {
		return Claims{}, ErrExpired
	}
	claims.KeyID = tokenHeader.KeyID
	return claims, nil
}

func validIssuer(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.String() == value
}

func validAuthority(value Authority) bool {
	if ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.UserID)) != nil || ids.Validate(value.ExportID) != nil || value.RequestVersion == 0 || value.ArtifactBytes <= 0 || len(value.ArtifactSHA256) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value.ArtifactSHA256)
	return err == nil
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
