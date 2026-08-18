package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
)

type NotificationOutbox struct{ pool *pgxpool.Pool }

func NewNotificationOutbox(pool *pgxpool.Pool) *NotificationOutbox {
	return &NotificationOutbox{pool: pool}
}

func (o *NotificationOutbox) Enqueue(ctx context.Context, entry notifications.Entry) error {
	_, err := o.pool.Exec(ctx, `
		INSERT INTO identity_notification_outbox
		(id,kind,ciphertext,nonce,key_version,processing_state,attempt_count,created_at)
		VALUES ($1,$2,$3,$4,$5,'queued',0,$6)`, entry.ID, entry.Kind, entry.Ciphertext, entry.Nonce, entry.KeyVersion, entry.CreatedAt.UTC())
	return err
}

func (o *NotificationOutbox) Claim(ctx context.Context, now time.Time, lease time.Duration) (notifications.Entry, bool, error) {
	var entry notifications.Entry
	err := o.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM identity_notification_outbox
			WHERE (processing_state IN ('queued','failed') AND COALESCE(next_attempt_at,created_at)<=$1)
			   OR (processing_state='processing' AND lease_expires_at<=$1)
			ORDER BY COALESCE(next_attempt_at,created_at),created_at,id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE identity_notification_outbox n
		SET processing_state='processing',attempt_count=n.attempt_count+1,
		    lease_expires_at=$1+($2*interval '1 second'),last_error_code=NULL
		FROM candidate c WHERE n.id=c.id
		RETURNING n.id,n.kind,n.ciphertext,n.nonce,n.key_version,n.created_at,n.attempt_count`, now.UTC(), int64(lease/time.Second)).Scan(
		&entry.ID, &entry.Kind, &entry.Ciphertext, &entry.Nonce, &entry.KeyVersion, &entry.CreatedAt, &entry.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return notifications.Entry{}, false, nil
	}
	if err != nil {
		return notifications.Entry{}, false, err
	}
	return entry, true, nil
}

func (o *NotificationOutbox) MarkDelivered(ctx context.Context, id string, now time.Time) error {
	command, err := o.pool.Exec(ctx, `
		UPDATE identity_notification_outbox
		SET processing_state='delivered',delivered_at=$2,lease_expires_at=NULL,next_attempt_at=NULL,last_error_code=NULL
		WHERE id=$1 AND processing_state='processing'`, id, now.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("notification outbox lease was lost")
	}
	return err
}

func (o *NotificationOutbox) MarkFailed(ctx context.Context, id string, _ time.Time, next time.Time, code string, terminal bool) error {
	state := "failed"
	if terminal {
		state = "dead_letter"
	}
	command, err := o.pool.Exec(ctx, `
		UPDATE identity_notification_outbox
		SET processing_state=$2,next_attempt_at=CASE WHEN $2='failed' THEN $3 ELSE NULL END,
		    lease_expires_at=NULL,last_error_code=$4
		WHERE id=$1 AND processing_state='processing'`, id, state, next.UTC(), code)
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("notification outbox lease was lost")
	}
	return err
}

var _ notifications.Queue = (*NotificationOutbox)(nil)
