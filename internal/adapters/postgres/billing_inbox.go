package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
)

type BillingInbox struct{ pool *pgxpool.Pool }

func NewBillingInbox(pool *pgxpool.Pool) *BillingInbox { return &BillingInbox{pool: pool} }

func (i *BillingInbox) Accept(ctx context.Context, entry billing.InboxEntry, payload []byte) (bool, error) {
	command, err := i.pool.Exec(ctx, `
		INSERT INTO billing_event_inbox (
			provider_event_id, account_id, event_type, provider_created_at, provider_object_id,
			mode, payload_hash, payload_reference, payload, signature_verified_at,
			processing_state, attempt_count, created_at
		) VALUES ($1,NULLIF($2::text,'')::uuid,$3,$4,NULLIF($5,''),$6,$7,'postgres:inline',$8,$9,$10,$11,$12)
		ON CONFLICT (provider_event_id) DO NOTHING`,
		entry.ProviderEventID, entry.AccountID, entry.EventType, entry.ProviderCreatedAt, entry.ProviderObjectID,
		entry.Mode, entry.PayloadHash[:], payload, entry.SignatureVerifiedAt,
		entry.ProcessingState, entry.AttemptCount, entry.CreatedAt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (i *BillingInbox) Claim(ctx context.Context, now time.Time, lease time.Duration) (billing.WorkItem, bool, error) {
	var item billing.WorkItem
	var hash []byte
	err := i.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT provider_event_id FROM billing_event_inbox
			WHERE (
				processing_state IN ('accepted','failed') AND COALESCE(next_attempt_at,created_at) <= $1
			) OR (
				processing_state='processing' AND lease_expires_at <= $1
			)
			ORDER BY provider_created_at,provider_event_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE billing_event_inbox e
		SET processing_state='processing',attempt_count=e.attempt_count+1,
		    lease_expires_at=$1+($2 * interval '1 second'),last_error_code=NULL
		FROM candidate c WHERE e.provider_event_id=c.provider_event_id
		RETURNING e.provider_event_id,COALESCE(e.account_id::text,''),e.event_type,e.provider_created_at,
		          COALESCE(e.provider_object_id,''),e.mode,e.payload_hash,e.signature_verified_at,
		          e.processing_state,e.attempt_count,e.created_at,e.payload`, now.UTC(), int64(lease/time.Second)).Scan(
		&item.Entry.ProviderEventID, &item.Entry.AccountID, &item.Entry.EventType, &item.Entry.ProviderCreatedAt,
		&item.Entry.ProviderObjectID, &item.Entry.Mode, &hash, &item.Entry.SignatureVerifiedAt,
		&item.Entry.ProcessingState, &item.Entry.AttemptCount, &item.Entry.CreatedAt, &item.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return billing.WorkItem{}, false, nil
	}
	if err != nil {
		return billing.WorkItem{}, false, err
	}
	if len(hash) != 32 {
		return billing.WorkItem{}, false, errors.New("billing payload hash is corrupt")
	}
	copy(item.Entry.PayloadHash[:], hash)
	return item, true, nil
}

func (i *BillingInbox) MarkProcessed(ctx context.Context, eventID string, now time.Time) error {
	command, err := i.pool.Exec(ctx, `UPDATE billing_event_inbox SET processing_state='processed',processed_at=$2,lease_expires_at=NULL,next_attempt_at=NULL,last_error_code=NULL WHERE provider_event_id=$1 AND processing_state='processing'`, eventID, now.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing event lease was lost")
	}
	return err
}

func (i *BillingInbox) MarkFailed(ctx context.Context, eventID string, _ time.Time, next time.Time, code string) error {
	command, err := i.pool.Exec(ctx, `UPDATE billing_event_inbox SET processing_state='failed',lease_expires_at=NULL,next_attempt_at=$2,last_error_code=$3 WHERE provider_event_id=$1 AND processing_state='processing'`, eventID, next.UTC(), code)
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing event lease was lost")
	}
	return err
}

// Replay is an operator boundary: it requeues a durably stored, previously
// verified event without accepting a new payload or bypassing projection.
func (i *BillingInbox) Replay(ctx context.Context, eventID string, now time.Time) error {
	command, err := i.pool.Exec(ctx, `UPDATE billing_event_inbox SET processing_state='accepted',next_attempt_at=$2,lease_expires_at=NULL,processed_at=NULL,last_error_code=NULL WHERE provider_event_id=$1 AND processing_state IN ('processed','failed')`, eventID, now.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing event is missing or currently processing")
	}
	return err
}

var _ billing.WorkQueue = (*BillingInbox)(nil)
