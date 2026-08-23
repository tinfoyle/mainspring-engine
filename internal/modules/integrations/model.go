// Package integrations owns Account-scoped connector authorization,
// credential-binding lifecycle, health, and external-effect evidence. Provider
// credentials and payloads deliberately remain outside this package.
package integrations

import (
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumConnectionNameBytes = 160
	MaximumReferenceBytes      = 500
	MaximumProviderCodeBytes   = 64
	MaximumMachineCodeBytes    = 100
	MaximumAttempts            = uint16(3)
)

var (
	ErrInvalid    = errors.New("integration aggregate is invalid")
	ErrConflict   = errors.New("integration aggregate version conflict")
	ErrState      = errors.New("integration state change is not allowed")
	ErrRole       = errors.New("role cannot perform the integration command")
	ErrCapability = errors.New("integration capability is not authorized")
	ErrCredential = errors.New("integration credential is unavailable")
	ErrUncertain  = errors.New("integration outcome requires reconciliation")
	validCode     = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

type Actor struct {
	UserID ids.UserID `json:"user_id"`
}

func (actor Actor) valid() bool { return ids.Validate(string(actor.UserID)) == nil }

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

func validText(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && (!required || value != "") && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

func validCodeValue(value string) bool {
	return len(value) <= MaximumMachineCodeBytes && validCode.MatchString(value)
}

func validTime(at time.Time, notBefore time.Time) bool {
	return !at.IsZero() && !at.UTC().Before(notBefore.UTC())
}

func nonzeroDigest(value [sha256.Size]byte) bool {
	return value != [sha256.Size]byte{}
}
