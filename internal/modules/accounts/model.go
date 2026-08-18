package accounts

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountState string
type AccountType string
type MembershipRole string
type MembershipState string
type InvitationState string

const (
	AccountFree    AccountType  = "free"
	AccountPaid    AccountType  = "paid"
	AccountActive  AccountState = "active"
	AccountClosing AccountState = "closing"
	AccountClosed  AccountState = "closed"

	RoleOwner         MembershipRole = "owner"
	RoleAdministrator MembershipRole = "administrator"
	RoleBillingAdmin  MembershipRole = "billing_admin"
	RoleMember        MembershipRole = "member"
	RoleViewer        MembershipRole = "viewer"

	MembershipActive    MembershipState = "active"
	MembershipSuspended MembershipState = "suspended"
	MembershipRemoved   MembershipState = "removed"
	InvitationPending   InvitationState = "pending"
	InvitationAccepted  InvitationState = "accepted"
	InvitationRevoked   InvitationState = "revoked"
)

type Account struct {
	ID                  ids.AccountID
	Slug                string
	DisplayName         string
	Type                AccountType
	State               AccountState
	CellID              ids.CellID
	PlacementGeneration uint64
	EntitlementVersion  uint64
	Version             uint64
	CreatedByUserID     ids.UserID
	CreatedAt           time.Time
}

type Membership struct {
	ID        ids.MembershipID
	AccountID ids.AccountID
	UserID    ids.UserID
	Role      MembershipRole
	State     MembershipState
	Version   uint64
	CreatedAt time.Time
}

type Invitation struct {
	ID              ids.InvitationID
	AccountID       ids.AccountID
	Email           string
	Role            MembershipRole
	State           InvitationState
	InvitedByUserID ids.UserID
	TokenHash       [32]byte
	ExpiresAt       time.Time
	CreatedAt       time.Time
	AcceptedAt      *time.Time
	RevokedAt       *time.Time
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func NewAccount(id ids.AccountID, userID ids.UserID, cellID ids.CellID, displayName string, now time.Time) (Account, error) {
	displayName = strings.TrimSpace(displayName)
	if len(displayName) < 2 || len(displayName) > 140 {
		return Account{}, errors.New("account name must be between 2 and 140 characters")
	}
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(displayName), "-"), "-")
	if len(slug) < 2 {
		return Account{}, errors.New("account name cannot produce a valid slug")
	}
	return Account{ID: id, Slug: slug, DisplayName: displayName, Type: AccountFree, State: AccountActive, CellID: cellID, PlacementGeneration: 1, EntitlementVersion: 1, Version: 1, CreatedByUserID: userID, CreatedAt: now.UTC()}, nil
}

func NewOwnerMembership(id ids.MembershipID, accountID ids.AccountID, userID ids.UserID, now time.Time) Membership {
	return Membership{ID: id, AccountID: accountID, UserID: userID, Role: RoleOwner, State: MembershipActive, Version: 1, CreatedAt: now.UTC()}
}
