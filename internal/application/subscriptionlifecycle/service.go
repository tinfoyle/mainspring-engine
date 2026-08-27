// Package subscriptionlifecycle advances failed-payment and cancellation
// retention clocks without bypassing the reviewed Account-erasure workflow.
package subscriptionlifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNoticePreparationFailed = errors.New("subscription lifecycle notice preparation failed")
	ErrProviderTermination     = errors.New("subscription provider termination failed")
)

type State string
type TriggerKind string
type NoticeKind string

const (
	StateCancellationScheduled State = "cancellation_scheduled"
	StateGraceReadOnly         State = "grace_read_only"
	StateRestricted            State = "restricted"
	StateTerminationPending    State = "termination_pending"
	StateRecovered             State = "recovered"
	StateClosed                State = "closed"

	TriggerPaymentFailure TriggerKind = "payment_failure"
	TriggerCancellation   TriggerKind = "cancellation"
)

type Work struct {
	LifecycleID, LeaseID string
	AccountID            ids.AccountID
	State                State
	Trigger              TriggerKind
	EffectiveAt          time.Time
	RestrictionAt        time.Time
	DeleteAt             time.Time
}

type Recipient struct {
	Email, DisplayName string
}

type Notice struct {
	NoticeID, LeaseID, AccountName string
	AccountID                      ids.AccountID
	Kind                           NoticeKind
	DueAt, DeleteAt                time.Time
	Attempt                        int
	Recipients                     []Recipient
}

type PreparedNotification struct {
	ID         string
	AccountID  ids.AccountID
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	CreatedAt  time.Time
}

type Termination struct {
	LifecycleID, ProviderSubscriptionID, LeaseID string
	Attempt                                      int
}

type Repository interface {
	Claim(context.Context, time.Time, time.Duration) (Work, bool, error)
	Advance(context.Context, Work, time.Time, time.Duration) (State, error)
	ClaimNotice(context.Context, time.Time, time.Duration) (Notice, bool, error)
	EmitNotice(context.Context, Notice, []PreparedNotification, time.Time) error
	FailNotice(context.Context, Notice, time.Time, string, bool) error
	ClaimTermination(context.Context, time.Time, time.Duration) (Termination, bool, error)
	CompleteTermination(context.Context, Termination, time.Time) error
	FailTermination(context.Context, Termination, time.Time, string, bool) error
}

type Clock interface{ Now() time.Time }

type Processor struct {
	repository Repository
	clock      Clock
	lease      time.Duration
	retry      time.Duration
}

func NewProcessor(repository Repository, clock Clock, lease, retry time.Duration) (*Processor, error) {
	if repository == nil || clock == nil || lease <= 0 || lease > 30*time.Minute || retry < time.Minute || retry > 7*24*time.Hour {
		return nil, errors.New("subscription lifecycle processor dependencies and safe timing are required")
	}
	return &Processor{repository: repository, clock: clock, lease: lease, retry: retry}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	work, ok, err := p.repository.Claim(ctx, now, p.lease)
	if err != nil || !ok {
		return ok, err
	}
	_, err = p.repository.Advance(ctx, work, now, p.retry)
	return true, err
}

type Message struct {
	AccountID                       ids.AccountID
	Email, DisplayName, AccountName string
	Kind                            NoticeKind
	DueAt, DeleteAt                 time.Time
}

type Sender interface {
	SendSubscriptionLifecycle(context.Context, Message) error
}

type NotificationPreparer interface {
	PrepareSubscriptionLifecycle(string, Message) (PreparedNotification, error)
}

type NoticeProcessor struct {
	repository Repository
	preparer   NotificationPreparer
	ids        ids.Generator
	clock      Clock
	lease      time.Duration
}

func NewNoticeProcessor(repository Repository, preparer NotificationPreparer, generator ids.Generator, clock Clock, lease time.Duration) (*NoticeProcessor, error) {
	if repository == nil || preparer == nil || generator == nil || clock == nil || lease <= 0 || lease > 30*time.Minute {
		return nil, errors.New("subscription notice processor dependencies and safe lease are required")
	}
	return &NoticeProcessor{repository: repository, preparer: preparer, ids: generator, clock: clock, lease: lease}, nil
}

func (p *NoticeProcessor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	notice, ok, err := p.repository.ClaimNotice(ctx, now, p.lease)
	if err != nil || !ok {
		return ok, err
	}
	prepared := make([]PreparedNotification, 0, len(notice.Recipients))
	for _, recipient := range notice.Recipients {
		item, prepareErr := p.preparer.PrepareSubscriptionLifecycle(p.ids.New(), Message{
			AccountID: notice.AccountID, Email: recipient.Email, DisplayName: recipient.DisplayName,
			AccountName: notice.AccountName, Kind: notice.Kind, DueAt: notice.DueAt, DeleteAt: notice.DeleteAt,
		})
		if prepareErr != nil {
			if markErr := p.repository.FailNotice(ctx, notice, now.Add(retryDelay(notice.Attempt)), "prepare_failed", notice.Attempt >= 12); markErr != nil {
				return true, errors.Join(ErrNoticePreparationFailed, markErr)
			}
			return true, ErrNoticePreparationFailed
		}
		prepared = append(prepared, item)
	}
	return true, p.repository.EmitNotice(ctx, notice, prepared, now)
}

type SubscriptionTerminator interface {
	CancelSubscription(context.Context, string, string) error
}

type TerminationProcessor struct {
	repository Repository
	provider   SubscriptionTerminator
	clock      Clock
	lease      time.Duration
}

func NewTerminationProcessor(repository Repository, provider SubscriptionTerminator, clock Clock, lease time.Duration) (*TerminationProcessor, error) {
	if repository == nil || provider == nil || clock == nil || lease <= 0 || lease > 30*time.Minute {
		return nil, errors.New("subscription termination processor dependencies and safe lease are required")
	}
	return &TerminationProcessor{repository: repository, provider: provider, clock: clock, lease: lease}, nil
}

func (p *TerminationProcessor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	work, ok, err := p.repository.ClaimTermination(ctx, now, p.lease)
	if err != nil || !ok {
		return ok, err
	}
	err = p.provider.CancelSubscription(ctx, work.ProviderSubscriptionID, "subscription-lifecycle/"+work.LifecycleID)
	if err == nil {
		return true, p.repository.CompleteTermination(ctx, work, now)
	}
	if markErr := p.repository.FailTermination(ctx, work, now.Add(retryDelay(work.Attempt)), "provider_failed", work.Attempt >= 12); markErr != nil {
		return true, errors.Join(ErrProviderTermination, markErr)
	}
	return true, ErrProviderTermination
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute
	for i := 1; i < attempt && delay < 24*time.Hour; i++ {
		delay *= 2
	}
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}
