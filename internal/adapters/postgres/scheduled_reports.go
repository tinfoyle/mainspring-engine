package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	app "github.com/tinfoyle/spyglass-engine/internal/application/scheduledreports"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"time"
)

type ScheduleReportRepository struct {
	cell, global *pgxpool.Pool
	cellID       ids.CellID
}

func NewScheduleReportRepository(cell, global *pgxpool.Pool, cellID ids.CellID) *ScheduleReportRepository {
	return &ScheduleReportRepository{cell, global, cellID}
}
func (r *ScheduleReportRepository) Claim(ctx context.Context, lease string, now time.Time) (app.Claim, bool, error) {
	v := app.Claim{LeaseID: lease}
	var attempt int
	err := r.cell.QueryRow(ctx, "SELECT account_id,id,recipient_user_id,attempt_count FROM public.spyglass_claim_schedule_report($1,$2)", lease, now).Scan(&v.AccountID, &v.ID, &v.UserID, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Claim{}, false, nil
	}
	return v, err == nil, err
}
func (r *ScheduleReportRepository) Resolve(ctx context.Context, c app.Claim) (string, error) {
	var email string
	err := r.global.QueryRow(ctx, "SELECT email FROM public.spyglass_schedule_report_recipient($1,$2,$3)", c.AccountID, c.UserID, r.cellID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", app.ErrDenied
	}
	return email, err
}
func (r *ScheduleReportRepository) Begin(ctx context.Context, c app.Claim, now time.Time) (app.Message, bool, error) {
	v := app.Message{ID: c.ID}
	err := r.cell.QueryRow(ctx, "SELECT subject,body,conversation_id,schedule_id,boardroom_id FROM public.spyglass_begin_schedule_report($1,$2,$3,$4)", c.AccountID, c.ID, c.LeaseID, now).Scan(&v.Subject, &v.Body, &v.ConversationID, &v.ScheduleID, &v.BoardroomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, false, nil
	}
	return v, err == nil, err
}
func (r *ScheduleReportRepository) Finish(ctx context.Context, c app.Claim, state, code string, now time.Time) error {
	var ok bool
	err := r.cell.QueryRow(ctx, "SELECT public.spyglass_finish_schedule_report($1,$2,$3,$4,$5,$6)", c.AccountID, c.ID, c.LeaseID, state, code, now).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("report delivery lease lost")
	}
	return nil
}
