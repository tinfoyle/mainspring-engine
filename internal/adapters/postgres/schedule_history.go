package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	app "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (r *ScheduleRepository) History(ctx context.Context, account ids.AccountID, schedule ids.ScheduleID) (app.HistoryPage, error) {
	page := app.HistoryPage{Items: []app.HistoryItem{}}
	err := r.cell.WithAccountTx(ctx, account, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT o.id,o.occurred_at,o.outcome,coalesce(o.run_id::text,''),coalesce(o.conversation_id::text,''),
 coalesce(r.boardroom_id::text,''),coalesce(r.state,''),coalesce(d.state,'not_requested'),coalesce(d.error_code,'')
 FROM spyglass.schedule_occurrences o
 LEFT JOIN spyglass.agent_runs r ON r.account_id=o.account_id AND r.id=o.run_id
 LEFT JOIN spyglass.schedule_report_deliveries d ON d.account_id=o.account_id AND d.id=o.id
 WHERE o.account_id=$1 AND o.schedule_id=$2 ORDER BY o.occurred_at DESC,o.id DESC LIMIT 50`, account, schedule)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v app.HistoryItem
			if err := rows.Scan(&v.ID, &v.OccurredAt, &v.Outcome, &v.RunID, &v.ConversationID, &v.BoardroomID, &v.RunState, &v.EmailState, &v.EmailError); err != nil {
				return err
			}
			page.Items = append(page.Items, v)
		}
		return rows.Err()
	})
	return page, classifySchedule(err)
}
