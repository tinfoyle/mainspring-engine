package identity

import (
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type UserState string

const (
	UserPendingVerification UserState = "pending_verification"
	UserActive              UserState = "active"
	UserSuspended           UserState = "suspended"
)

type User struct {
	ID              ids.UserID
	PrimaryEmail    string
	DisplayName     string
	State           UserState
	EmailVerifiedAt *time.Time
	SecurityVersion uint64
	CreatedAt       time.Time
}

type LocalCredential struct {
	UserID       ids.UserID
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", errors.New("email must be a valid mailbox")
	}
	return value, nil
}

func NewPendingUser(id ids.UserID, email, displayName string, now time.Time) (User, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if len(displayName) < 2 || len(displayName) > 120 {
		return User{}, errors.New("display name must be between 2 and 120 characters")
	}
	return User{ID: id, PrimaryEmail: normalized, DisplayName: displayName, State: UserPendingVerification, SecurityVersion: 1, CreatedAt: now.UTC()}, nil
}

func (u User) VerifyEmail(now time.Time) (User, error) {
	if u.State != UserPendingVerification {
		return User{}, errors.New("user is not awaiting verification")
	}
	verified := now.UTC()
	u.State = UserActive
	u.EmailVerifiedAt = &verified
	return u, nil
}
