// Package operations defines the staff-only authority and safe projections
// used by the Infinite Ocean Operations Console. An operations actor is never
// represented as a customer User actor, even when a support grant permits the
// actor to inspect one exact User and Account.
package operations

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type StaffRole string
type StaffState string
type GrantState string
type LookupKind string

const (
	RoleAdministrator StaffRole = "operations_administrator"
	RoleSupport       StaffRole = "support"
	RoleBilling       StaffRole = "billing"
	RoleAnalytics     StaffRole = "analytics"
	RolePrivacy       StaffRole = "privacy"
	RoleAffiliate     StaffRole = "affiliate"

	StaffActive    StaffState = "active"
	StaffSuspended StaffState = "suspended"

	GrantActive  GrantState = "active"
	GrantRevoked GrantState = "revoked"

	LookupUserID             LookupKind = "user_id"
	LookupEmail              LookupKind = "email"
	LookupAccountID          LookupKind = "account_id"
	LookupStripeCustomer     LookupKind = "stripe_customer_id"
	LookupStripeSubscription LookupKind = "stripe_subscription_id"

	MinimumGrantLifetime = 5 * time.Minute
	DefaultGrantLifetime = 30 * time.Minute
	MaximumGrantLifetime = time.Hour
)

var (
	ErrInvalidInput      = errors.New("operations input is invalid")
	ErrStaffUnauthorized = errors.New("operations staff authorization is required")
	ErrGrantDenied       = errors.New("support grant is unavailable")
	ErrNotFound          = errors.New("operations target was not found")
	ticketPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{2,79}$`)
)

type Staff struct {
	UserID      ids.UserID  `json:"user_id"`
	DisplayName string      `json:"display_name"`
	State       StaffState  `json:"state"`
	Roles       []StaffRole `json:"roles"`
}

func (s Staff) HasRole(roles ...StaffRole) bool {
	if s.State != StaffActive || s.UserID == "" {
		return false
	}
	for _, assigned := range s.Roles {
		if assigned == RoleAdministrator {
			return true
		}
		for _, required := range roles {
			if assigned == required {
				return true
			}
		}
	}
	return false
}

type AuditReason struct {
	Ticket string `json:"ticket"`
	Reason string `json:"reason"`
}

func (value AuditReason) Validate() error {
	value.Ticket = strings.TrimSpace(value.Ticket)
	value.Reason = strings.TrimSpace(value.Reason)
	if !ticketPattern.MatchString(value.Ticket) || len(value.Reason) < 8 || len(value.Reason) > 500 || strings.ContainsAny(value.Reason, "\x00\r\n") {
		return ErrInvalidInput
	}
	return nil
}

type SupportGrant struct {
	ID           ids.OperationsSupportGrantID `json:"id"`
	StaffUserID  ids.UserID                   `json:"staff_user_id"`
	TargetUserID ids.UserID                   `json:"target_user_id"`
	AccountID    ids.AccountID                `json:"account_id"`
	State        GrantState                   `json:"state"`
	Ticket       string                       `json:"ticket"`
	Reason       string                       `json:"reason"`
	CreatedAt    time.Time                    `json:"created_at"`
	ExpiresAt    time.Time                    `json:"expires_at"`
	RevokedAt    *time.Time                   `json:"revoked_at,omitempty"`
	Version      uint64                       `json:"version"`
}

func (grant SupportGrant) AvailableTo(staff Staff, now time.Time) bool {
	return grant.ID != "" && grant.StaffUserID == staff.UserID && grant.State == GrantActive &&
		grant.RevokedAt == nil && grant.CreatedAt.Before(grant.ExpiresAt) && now.UTC().Before(grant.ExpiresAt.UTC()) &&
		staff.HasRole(RoleSupport)
}

type LookupQuery struct {
	Kind  LookupKind
	Value string
	Audit AuditReason
}

func (query LookupQuery) Validate() error {
	if err := query.Audit.Validate(); err != nil {
		return err
	}
	value := strings.TrimSpace(query.Value)
	if value == "" || len(value) > 320 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return ErrInvalidInput
	}
	switch query.Kind {
	case LookupUserID, LookupAccountID:
		if ids.Validate(value) != nil {
			return ErrInvalidInput
		}
	case LookupEmail:
		if value != strings.ToLower(value) || !strings.Contains(value, "@") {
			return ErrInvalidInput
		}
	case LookupStripeCustomer:
		if !strings.HasPrefix(value, "cus_") {
			return ErrInvalidInput
		}
	case LookupStripeSubscription:
		if !strings.HasPrefix(value, "sub_") {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

type LookupResult struct {
	UserID             ids.UserID    `json:"user_id"`
	DisplayName        string        `json:"display_name"`
	Email              string        `json:"email"`
	UserState          string        `json:"user_state"`
	EmailVerified      bool          `json:"email_verified"`
	AccountID          ids.AccountID `json:"account_id"`
	AccountName        string        `json:"account_name"`
	AccountState       string        `json:"account_state"`
	AccountType        string        `json:"account_type"`
	MembershipRole     string        `json:"membership_role"`
	MembershipState    string        `json:"membership_state"`
	StripeCustomerID   string        `json:"stripe_customer_id,omitempty"`
	StripeSubscription string        `json:"stripe_subscription_id,omitempty"`
}

type AccountView struct {
	Grant          SupportGrant    `json:"grant"`
	User           UserView        `json:"user"`
	Account        AccountSummary  `json:"account"`
	Membership     MembershipView  `json:"membership"`
	Billing        BillingView     `json:"billing"`
	Entitlements   EntitlementView `json:"entitlements"`
	AITokens       AITokenView     `json:"ai_tokens"`
	Lifecycle      LifecycleView   `json:"lifecycle"`
	SupportHistory []AccessEvent   `json:"support_history"`
}

type UserView struct {
	ID                     ids.UserID `json:"id"`
	DisplayName            string     `json:"display_name"`
	Email                  string     `json:"email"`
	State                  string     `json:"state"`
	EmailVerifiedAt        *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	PasskeyCount           int        `json:"passkey_count"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
}

type AccountSummary struct {
	ID                  ids.AccountID `json:"id"`
	DisplayName         string        `json:"display_name"`
	State               string        `json:"state"`
	Type                string        `json:"type"`
	CellID              string        `json:"cell_id"`
	PlacementGeneration uint64        `json:"placement_generation"`
	EntitlementVersion  uint64        `json:"entitlement_version"`
	CreatedAt           time.Time     `json:"created_at"`
}

type MembershipView struct {
	Role    string `json:"role"`
	State   string `json:"state"`
	Version uint64 `json:"version"`
}

type BillingView struct {
	CustomerID       string     `json:"customer_id,omitempty"`
	BillingEmail     string     `json:"billing_email,omitempty"`
	SubscriptionID   string     `json:"subscription_id,omitempty"`
	ProviderMode     string     `json:"provider_mode,omitempty"`
	State            string     `json:"state,omitempty"`
	OfferCode        string     `json:"offer_code,omitempty"`
	OfferVersion     uint64     `json:"offer_version,omitempty"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
	CancelAt         *time.Time `json:"cancel_at,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
}

type EntitlementView struct {
	Version        uint64         `json:"version"`
	CatalogVersion uint64         `json:"catalog_version"`
	EvaluatedAt    time.Time      `json:"evaluated_at"`
	Packages       map[string]any `json:"packages"`
}

type AITokenView struct {
	Available uint64 `json:"available"`
	Reserved  uint64 `json:"reserved"`
	Consumed  uint64 `json:"consumed"`
}

type LifecycleView struct {
	RequestID    string     `json:"request_id,omitempty"`
	State        string     `json:"state,omitempty"`
	ExecuteAfter *time.Time `json:"execute_after,omitempty"`
	DeleteAfter  *time.Time `json:"delete_after,omitempty"`
	BlockerCode  string     `json:"blocker_code,omitempty"`
}

type AccessEvent struct {
	ID               ids.OperationsAuditEventID `json:"id"`
	StaffDisplayName string                     `json:"staff_display_name"`
	Action           string                     `json:"action"`
	Ticket           string                     `json:"ticket"`
	Reason           string                     `json:"reason"`
	OccurredAt       time.Time                  `json:"occurred_at"`
}
