// Package operationsauth implements Google plus an independently verified admin authenticator.
package operationsauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
)

// Derive independent encryption keys from the deployed versioned root keyring.
func NewCipher(keys map[int][]byte, active int) (*passkeys.Cipher, error) {
	derived := make(map[int][]byte, len(keys))
	for version, key := range keys {
		if len(key) != 32 {
			return nil, ErrDenied
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("spyglass/operations-auth/v1"))
		derived[version] = mac.Sum(nil)
	}
	return passkeys.NewCipherKeyring(derived, active)
}
func RandomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func Hash(token string) [32]byte { return sha256.Sum256([]byte(token)) }
func ValidToken(token string) bool {
	b, e := base64.RawURLEncoding.DecodeString(token)
	return e == nil && len(b) == 32 && len(token) == 43
}

// Code uses RFC 6238's SHA-1 profile for interoperability with authenticator apps.
func Code(secret []byte, step int64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(b[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	n := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000)
}
func Match(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step > last && subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func SetupURI(secret []byte, name string) (string, string) {
	key := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	return key, "otpauth://totp/" + url.PathEscape("Spyglass Admin:"+name) + "?" + url.Values{"secret": {key}, "issuer": {"Spyglass Admin"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
}
func RecoveryHash(code string) []byte {
	h := sha256.Sum256([]byte("spyglass/admin-recovery/v1/" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))))
	return h[:]
}
func recoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, 8)
	hashes := make([][]byte, 8)
	for i := range codes {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
		codes[i] = s[:6] + "-" + s[6:12] + "-" + s[12:18] + "-" + s[18:]
		hashes[i] = RecoveryHash(codes[i])
	}
	return codes, hashes, nil
}

type Ticket struct {
	Identifier, State, Audience string
	ExpiresAt                   time.Time
}

func SealTicket(cipher *passkeys.Cipher, ticket Ticket) (string, error) {
	raw, err := json.Marshal(ticket)
	if err != nil {
		return "", err
	}
	e, err := cipher.Seal("google-handoff", raw)
	if err != nil {
		return "", err
	}
	raw, err = json.Marshal(e)
	return base64.RawURLEncoding.EncodeToString(raw), err
}
func OpenTicket(cipher *passkeys.Cipher, raw, audience, state string, now time.Time) (Ticket, error) {
	if len(raw) > 8192 || !ValidToken(state) {
		return Ticket{}, ErrDenied
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Ticket{}, ErrDenied
	}
	var envelope passkeys.Envelope
	if json.Unmarshal(b, &envelope) != nil {
		return Ticket{}, ErrDenied
	}
	b, err = cipher.Open("google-handoff", envelope)
	if err != nil {
		return Ticket{}, ErrDenied
	}
	var t Ticket
	if json.Unmarshal(b, &t) != nil || t.Audience != audience || t.State != state || !t.ExpiresAt.After(now) || t.ExpiresAt.After(now.Add(2*time.Minute)) || !strings.HasPrefix(t.Identifier, "https://accounts.google.com\x1f") {
		return Ticket{}, ErrDenied
	}
	return t, nil
}
