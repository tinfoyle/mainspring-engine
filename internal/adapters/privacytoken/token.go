package privacytoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalid = errors.New("privacy preference token is invalid")

type Claims = privacy.PreferenceReference

type Signer struct{ key []byte }

func New(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("privacy preference signing key must contain at least 32 bytes")
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

func (s *Signer) Sign(claims Claims) (string, error) {
	if claims.Validate() != nil {
		return "", ErrInvalid
	}
	payload := fmt.Sprintf("v1.%s.%d.%s", claims.SubjectID, claims.PolicyVersion, claims.Surface)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Signer) Verify(token string, expected privacy.Surface) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] != "v1" || len(token) > 256 {
		return Claims{}, ErrInvalid
	}
	payload := strings.Join(parts[:4], ".")
	provided, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return Claims{}, ErrInvalid
	}
	version, err := strconv.ParseUint(parts[2], 10, 64)
	claims := privacy.PreferenceReference{SubjectID: ids.ConsentSubjectID(parts[1]), PolicyVersion: version, Surface: privacy.Surface(parts[3])}
	if err != nil || ids.Validate(parts[1]) != nil || version == 0 || claims.Surface != expected {
		return Claims{}, ErrInvalid
	}
	return claims, nil
}
