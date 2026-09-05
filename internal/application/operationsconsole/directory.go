package operationsconsole

import (
	"context"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"strings"
	"time"
)

func (s *Service) Directory(ctx context.Context, actor ids.UserID, query operations.DirectoryQuery) (operations.DirectoryPage, error) {
	staff, err := s.Staff(ctx, actor)
	if err != nil {
		return operations.DirectoryPage{}, err
	}
	if !staff.HasRole(operations.RoleAdministrator) {
		return operations.DirectoryPage{}, operations.ErrStaffUnauthorized
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 25
	}
	query.Audit.Ticket = strings.TrimSpace(query.Audit.Ticket)
	query.Audit.Reason = strings.TrimSpace(query.Audit.Reason)
	if err := query.Validate(); err != nil {
		return operations.DirectoryPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.repository.Directory(ctx, staff, query, s.eventID(), s.environment)
}
