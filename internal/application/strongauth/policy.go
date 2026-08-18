// Package strongauth defines the assurance boundary for privileged Spyglass
// operations. It deliberately lives above the session module so application
// services, rather than individual transports, own the enforcement decision.
package strongauth

import (
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const MaximumAge = 10 * time.Minute

var ErrRequired = errors.New("recent user-verified cryptographic authentication is required")

// Require rejects missing, cross-user, stale, password-only, and otherwise
// unknown authentication evidence with the same public error.
func Require(session sessions.Session, actorUserID ids.UserID, now time.Time) error {
	if actorUserID == "" || session.UserID == "" || session.UserID != actorUserID ||
		!sessions.RecentlyReauthenticatedWithAssurance(session, now.UTC(), MaximumAge, sessions.AssuranceUserVerifiedCryptographic) {
		return ErrRequired
	}
	return nil
}
