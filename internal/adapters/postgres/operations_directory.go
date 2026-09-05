package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (r *OperationsConsoleRepository) Directory(ctx context.Context, staff operations.Staff, query operations.DirectoryQuery, eventID ids.OperationsAuditEventID, environment string) (operations.DirectoryPage, error) {
	var data []byte
	var result operations.DirectoryPage
	err := r.pool.QueryRow(ctx, `SELECT spyglass_operations_directory($1,$2,$3,$4,$5,$6,$7,$8)`,
		eventID, staff.UserID, query.Kind, query.Page, query.PageSize, query.Audit.Ticket, query.Audit.Reason, environment).Scan(&data)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "42501" {
			return result, operations.ErrStaffUnauthorized
		}
		return result, classifyOperationsError(err)
	}
	err = json.Unmarshal(data, &result)
	return result, err
}
