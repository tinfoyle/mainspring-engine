package operations

import (
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"time"
)

type DirectoryQuery struct {
	Kind     string
	Page     int
	PageSize int
	Audit    AuditReason
}

func (q DirectoryQuery) Validate() error {
	if (q.Kind != "users" && q.Kind != "teams") || q.Page < 1 || q.Page > 1000000 || q.PageSize < 1 || q.PageSize > 100 {
		return ErrInvalidInput
	}
	return q.Audit.Validate()
}

type DirectoryUser struct {
	ID            ids.UserID `json:"id"`
	DisplayName   string     `json:"display_name"`
	Email         string     `json:"email"`
	State         string     `json:"state"`
	EmailVerified bool       `json:"email_verified"`
	TeamCount     int64      `json:"team_count"`
	CreatedAt     time.Time  `json:"created_at"`
}
type DirectoryTeam struct {
	ID          ids.AccountID `json:"id"`
	DisplayName string        `json:"display_name"`
	Slug        string        `json:"slug"`
	State       string        `json:"state"`
	AccountType string        `json:"account_type"`
	MemberCount int64         `json:"member_count"`
	CreatedAt   time.Time     `json:"created_at"`
}
type DirectoryPage struct {
	Kind     string          `json:"kind"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
	Users    []DirectoryUser `json:"users"`
	Teams    []DirectoryTeam `json:"teams"`
}
