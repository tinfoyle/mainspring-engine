package tools

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrInvalidToken = errors.New("invalid capability token")
	ErrExpiredToken = errors.New("expired capability token")
)

type Claims struct {
	TenantID     string   `json:"tenant_id"`
	BoardroomID  string   `json:"boardroom_id"`
	RunID        string   `json:"run_id"`
	PersonaID    string   `json:"persona_id"`
	InvocationID string   `json:"invocation_id"`
	Grants       []string `json:"grants"`
	IssuedAt     int64    `json:"iat"`
	ExpiresAt    int64    `json:"exp"`
}

type TokenIssuer struct {
	secret      []byte
	maxLifetime time.Duration
	now         func() time.Time
}

func NewTokenIssuer(secret []byte, maxLifetime time.Duration) (*TokenIssuer, error) {
	if len(secret) < 32 {
		return nil, errors.New("tool capability token secret must contain at least 32 bytes")
	}
	if maxLifetime <= 0 {
		maxLifetime = 15 * time.Minute
	}
	return &TokenIssuer{secret: append([]byte(nil), secret...), maxLifetime: maxLifetime, now: time.Now}, nil
}

func (i *TokenIssuer) Mint(invocation domain.InvocationContext) (string, error) {
	now := i.now().UTC()
	if invocation.ExpiresAt.IsZero() || !invocation.ExpiresAt.After(now) || invocation.ExpiresAt.Sub(now) > i.maxLifetime {
		return "", errors.New("capability token expiration is outside the permitted lifetime")
	}
	claims := Claims{
		TenantID: invocation.TenantID.String(), BoardroomID: invocation.BoardroomID.String(), RunID: invocation.RunID.String(),
		PersonaID: invocation.PersonaID.String(), InvocationID: invocation.InvocationID.String(),
		IssuedAt: now.Unix(), ExpiresAt: invocation.ExpiresAt.Unix(),
	}
	for _, grant := range invocation.Grants {
		claims.Grants = append(claims.Grants, string(grant.Capability))
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return "v1." + encoded + "." + i.signature(encoded), nil
}

func (i *TokenIssuer) Verify(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return Claims{}, ErrInvalidToken
	}
	expected := i.signature(parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrInvalidToken
	}
	now := i.now().UTC().Unix()
	if claims.ExpiresAt <= now {
		return Claims{}, ErrExpiredToken
	}
	if claims.IssuedAt > now+30 || claims.ExpiresAt <= claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > i.maxLifetime {
		return Claims{}, ErrInvalidToken
	}
	if _, err := domain.ParseTenantID(claims.TenantID); err != nil {
		return Claims{}, fmt.Errorf("%w: tenant", ErrInvalidToken)
	}
	if _, err := domain.ParseBoardroomID(claims.BoardroomID); err != nil {
		return Claims{}, fmt.Errorf("%w: boardroom", ErrInvalidToken)
	}
	if _, err := domain.ParseRunID(claims.RunID); err != nil {
		return Claims{}, fmt.Errorf("%w: run", ErrInvalidToken)
	}
	if _, err := domain.ParsePersonaID(claims.PersonaID); err != nil {
		return Claims{}, fmt.Errorf("%w: persona", ErrInvalidToken)
	}
	if _, err := domain.ParseInvocationID(claims.InvocationID); err != nil {
		return Claims{}, fmt.Errorf("%w: invocation", ErrInvalidToken)
	}
	return claims, nil
}

func (i *TokenIssuer) signature(encodedPayload string) string {
	mac := hmac.New(sha256.New, i.secret)
	_, _ = mac.Write([]byte("v1." + encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
