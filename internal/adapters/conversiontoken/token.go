package conversiontoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalid = errors.New("analytics conversion token is invalid")

type Signer struct{ key []byte }

func New(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("analytics conversion signing key must contain at least 32 bytes")
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

func (s *Signer) Sign(claims analytics.HandoffReference, now time.Time) (string, error) {
	if claims.Validate(now) != nil {
		return "", ErrInvalid
	}
	payload := "v1." + string(claims.ReceiptEventID) + "." + string(claims.SubjectID) + "." + strconv.FormatInt(claims.ExpiresAt.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("spyglass/analytics-conversion/" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Signer) Verify(token string, now time.Time) (analytics.HandoffReference, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] != "v1" || len(token) > 256 {
		return analytics.HandoffReference{}, ErrInvalid
	}
	payload := strings.Join(parts[:4], ".")
	provided, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return analytics.HandoffReference{}, ErrInvalid
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("spyglass/analytics-conversion/" + payload))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return analytics.HandoffReference{}, ErrInvalid
	}
	expires, err := strconv.ParseInt(parts[3], 10, 64)
	claims := analytics.HandoffReference{ReceiptEventID: ids.AnalyticsEventID(parts[1]), SubjectID: ids.ConsentSubjectID(parts[2]), ExpiresAt: time.Unix(expires, 0).UTC()}
	if err != nil || claims.Validate(now) != nil {
		return analytics.HandoffReference{}, ErrInvalid
	}
	return claims, nil
}
