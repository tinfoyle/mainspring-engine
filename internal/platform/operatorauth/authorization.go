// Package operatorauth verifies short-lived authorization envelopes issued by
// the external Infinite Ocean operator identity plane. Environment strings
// describe an operator for audit; a valid envelope is the authorization proof.
package operatorauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	Type               = "SPYGLASS-OPERATOR-AUTH"
	Assurance          = "phishing_resistant"
	ModeStandard       = "standard"
	ModeBreakGlass     = "break_glass"
	MaximumLifetime    = 10 * time.Minute
	MaximumClockSkew   = 30 * time.Second
	MaximumTokenBytes  = 16 << 10
	MaximumIdentityLen = 254
)

var ErrInvalid = errors.New("operator authorization is invalid")

type Request struct {
	Token           string
	VerifyKeys      map[string][]byte
	Issuer          string
	Actor           string
	Action          string
	Environment     string
	Reason          string
	Scope           string
	AllowBreakGlass bool
	Now             time.Time
}

type Evidence struct {
	AuthorizationID string
	Issuer          string
	Actor           string
	Action          string
	Environment     string
	Mode            string
	KeyID           string
	IncidentID      string
	Approvers       []string
	ExpiresAt       time.Time
}

type header struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

type claims struct {
	Version         int      `json:"version"`
	Issuer          string   `json:"issuer"`
	AuthorizationID string   `json:"authorization_id"`
	Actor           string   `json:"actor"`
	Action          string   `json:"action"`
	Environment     string   `json:"environment"`
	ReasonSHA256    string   `json:"reason_sha256"`
	ScopeSHA256     string   `json:"scope_sha256"`
	Assurance       string   `json:"assurance"`
	Mode            string   `json:"mode"`
	IncidentID      string   `json:"incident_id,omitempty"`
	Approvers       []string `json:"approvers,omitempty"`
	IssuedAt        int64    `json:"issued_at"`
	ExpiresAt       int64    `json:"expires_at"`
}

func Verify(request Request) (Evidence, error) {
	if len(request.Token) == 0 || len(request.Token) > MaximumTokenBytes || request.Now.IsZero() ||
		!validText(request.Issuer, MaximumIdentityLen) || !validText(request.Actor, MaximumIdentityLen) ||
		!validText(request.Action, 160) || !validText(request.Environment, 80) || !validText(request.Reason, 500) ||
		!validText(request.Scope, 4096) || len(request.VerifyKeys) == 0 {
		return Evidence{}, ErrInvalid
	}
	segments := strings.Split(request.Token, ".")
	if len(segments) != 3 || segments[0] == "" || segments[1] == "" || segments[2] == "" {
		return Evidence{}, ErrInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil {
		return Evidence{}, ErrInvalid
	}
	var tokenHeader header
	if decodeStrict(headerBytes, &tokenHeader) != nil || tokenHeader.Algorithm != "EdDSA" || tokenHeader.Type != Type || !validText(tokenHeader.KeyID, 80) {
		return Evidence{}, ErrInvalid
	}
	key := request.VerifyKeys[tokenHeader.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return Evidence{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), []byte(segments[0]+"."+segments[1]), signature) {
		return Evidence{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return Evidence{}, ErrInvalid
	}
	var value claims
	if decodeStrict(payload, &value) != nil {
		return Evidence{}, ErrInvalid
	}
	if err := validateClaims(value, request); err != nil {
		return Evidence{}, err
	}
	return Evidence{
		AuthorizationID: value.AuthorizationID, Issuer: value.Issuer, Actor: value.Actor, Action: value.Action,
		Environment: value.Environment, Mode: value.Mode, KeyID: tokenHeader.KeyID, IncidentID: value.IncidentID,
		Approvers: append([]string(nil), value.Approvers...), ExpiresAt: time.Unix(value.ExpiresAt, 0).UTC(),
	}, nil
}

func Digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func validateClaims(value claims, request Request) error {
	issuedAt := time.Unix(value.IssuedAt, 0).UTC()
	expiresAt := time.Unix(value.ExpiresAt, 0).UTC()
	now := request.Now.UTC()
	if value.Version != 1 || value.Issuer != request.Issuer || value.Actor != request.Actor || value.Action != request.Action ||
		value.Environment != request.Environment || value.ReasonSHA256 != Digest(request.Reason) || value.ScopeSHA256 != Digest(request.Scope) ||
		value.Assurance != Assurance || ids.Validate(value.AuthorizationID) != nil ||
		issuedAt.After(now.Add(MaximumClockSkew)) || expiresAt.Before(now.Add(-MaximumClockSkew)) || !expiresAt.After(issuedAt) ||
		expiresAt.Sub(issuedAt) > MaximumLifetime || now.Sub(issuedAt) > MaximumLifetime+MaximumClockSkew {
		return ErrInvalid
	}
	switch value.Mode {
	case ModeStandard:
		if value.IncidentID != "" || len(value.Approvers) != 0 {
			return ErrInvalid
		}
	case ModeBreakGlass:
		if !request.AllowBreakGlass || !validText(value.IncidentID, 160) || len(value.Approvers) < 2 || len(value.Approvers) > 8 {
			return ErrInvalid
		}
		seen := map[string]struct{}{value.Actor: {}}
		for _, approver := range value.Approvers {
			if !validText(approver, MaximumIdentityLen) {
				return ErrInvalid
			}
			normalized := strings.ToLower(approver)
			for existing := range seen {
				if strings.ToLower(existing) == normalized {
					return ErrInvalid
				}
			}
			seen[approver] = struct{}{}
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validText(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing authorization data")
	}
	return nil
}
