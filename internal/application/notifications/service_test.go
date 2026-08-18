package notifications_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/notifications"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type fakeQueue struct {
	entries    []notifications.Entry
	delivered  string
	failed     string
	failedCode string
	terminal   bool
	next       time.Time
}

func (q *fakeQueue) Enqueue(_ context.Context, entry notifications.Entry) error {
	q.entries = append(q.entries, entry)
	return nil
}
func (q *fakeQueue) Claim(_ context.Context, _ time.Time, _ time.Duration) (notifications.Entry, bool, error) {
	if len(q.entries) == 0 {
		return notifications.Entry{}, false, nil
	}
	entry := q.entries[0]
	q.entries = q.entries[1:]
	entry.AttemptCount++
	return entry, true, nil
}
func (q *fakeQueue) MarkDelivered(_ context.Context, id string, _ time.Time) error {
	q.delivered = id
	return nil
}
func (q *fakeQueue) MarkFailed(_ context.Context, id string, _ time.Time, next time.Time, code string, terminal bool) error {
	q.failed, q.failedCode, q.terminal, q.next = id, code, terminal, next
	return nil
}

type delivery struct {
	verification registration.VerificationMessage
	invitation   invitations.Message
	recovery     recovery.Message
	ownership    accountmembers.OwnershipTransferNotice
	err          error
	ownershipErr map[string]error
}

func (d *delivery) SendVerification(_ context.Context, message registration.VerificationMessage) error {
	d.verification = message
	return d.err
}
func (d *delivery) SendInvitation(_ context.Context, message invitations.Message) error {
	d.invitation = message
	return d.err
}
func (d *delivery) SendRecovery(_ context.Context, message recovery.Message) error {
	d.recovery = message
	return d.err
}
func (d *delivery) SendOwnershipTransfer(_ context.Context, message accountmembers.OwnershipTransferNotice) error {
	d.ownership = message
	if d.ownershipErr != nil && d.ownershipErr[message.Email] != nil {
		return d.ownershipErr[message.Email]
	}
	return d.err
}

type generator struct{ value string }

func (g generator) New() string { return g.value }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestQueuedNotificationIsEncryptedAndDelivered(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	envelopeCipher, err := notifications.NewCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sender, err := notifications.NewQueuedSender(queue, envelopeCipher, generator{"10000000-0000-4000-8000-000000000001"}, clock{now})
	if err != nil {
		t.Fatal(err)
	}
	message := registration.VerificationMessage{Email: "owner@example.com", DisplayName: "Owner", Token: "secret-verification-token", OfferCode: "team-monthly-v1", ExpiresAt: now.Add(time.Hour)}
	if err := sender.SendVerification(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if len(queue.entries) != 1 || bytes.Contains(queue.entries[0].Ciphertext, []byte(message.Email)) || bytes.Contains(queue.entries[0].Ciphertext, []byte(message.Token)) || bytes.Contains(queue.entries[0].Ciphertext, []byte(message.OfferCode)) {
		t.Fatalf("notification envelope leaked plaintext: %+v", queue.entries)
	}
	delivery := &delivery{}
	processor, err := notifications.NewProcessor(queue, envelopeCipher, delivery, clock{now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || delivery.verification.Token != message.Token || delivery.verification.OfferCode != message.OfferCode || queue.delivered == "" {
		t.Fatalf("delivery result: worked=%v message=%+v delivered=%q err=%v", worked, delivery.verification, queue.delivered, err)
	}
}

func TestDiscardEnvelopeEqualizesRecoveryWithoutDelivery(t *testing.T) {
	envelopeCipher, _ := notifications.NewCipher(bytes.Repeat([]byte{0x24}, 32), 1)
	queue := &fakeQueue{}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sender, _ := notifications.NewQueuedSender(queue, envelopeCipher, generator{"20000000-0000-4000-8000-000000000002"}, clock{now})
	if err := sender.SendRecovery(context.Background(), recovery.Message{Token: "synthetic-token", ExpiresAt: now.Add(time.Hour), Suppress: true}); err != nil {
		t.Fatal(err)
	}
	delivery := &delivery{}
	processor, _ := notifications.NewProcessor(queue, envelopeCipher, delivery, clock{now}, time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || delivery.recovery.Token != "" || queue.delivered == "" {
		t.Fatalf("discard result: worked=%v delivery=%+v err=%v", worked, delivery.recovery, err)
	}
}

func TestOwnershipTransferPreparationEncryptsAndDeliversOneRecipient(t *testing.T) {
	envelopeCipher, _ := notifications.NewCipher(bytes.Repeat([]byte{0x26}, 32), 1)
	queue := &fakeQueue{}
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	sender, _ := notifications.NewQueuedSender(queue, envelopeCipher, generator{"26000000-0000-4000-8000-000000000002"}, clock{now})
	message := accountmembers.OwnershipTransferNotice{AccountID: "11111111-1111-4111-8111-111111111111", Email: "successor@example.com", DisplayName: "Successor", AccountName: "Northstar", CounterpartDisplayName: "Original Owner", RecipientRole: accountmembers.OwnershipNoticeNewOwner, OccurredAt: now}
	prepared, err := sender.PrepareOwnershipTransfer("27000000-0000-4000-8000-000000000002", message)
	if err != nil || prepared.ID == "" || bytes.Contains(prepared.Ciphertext, []byte(message.Email)) || bytes.Contains(prepared.Ciphertext, []byte(message.AccountName)) {
		t.Fatalf("prepared ownership envelope=%+v err=%v", prepared, err)
	}
	queue.entries = append(queue.entries, notifications.Entry{ID: prepared.ID, AccountID: prepared.AccountID, Kind: notifications.KindOwnership, Ciphertext: prepared.Ciphertext, Nonce: prepared.Nonce, KeyVersion: prepared.KeyVersion, CreatedAt: prepared.CreatedAt})
	delivery := &delivery{}
	processor, _ := notifications.NewProcessor(queue, envelopeCipher, delivery, clock{now}, time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || delivery.ownership != message || queue.delivered != prepared.ID {
		t.Fatalf("ownership delivery=%+v worked=%v delivered=%q err=%v", delivery.ownership, worked, queue.delivered, err)
	}
}

func TestOwnershipRecipientsRetryIndependently(t *testing.T) {
	envelopeCipher, _ := notifications.NewCipher(bytes.Repeat([]byte{0x27}, 32), 1)
	queue := &fakeQueue{}
	now := time.Date(2026, 8, 18, 19, 0, 0, 0, time.UTC)
	sender, _ := notifications.NewQueuedSender(queue, envelopeCipher, generator{"unused"}, clock{now})
	for index, email := range []string{"previous@example.com", "new@example.com"} {
		role := accountmembers.OwnershipNoticePreviousOwner
		if index == 1 {
			role = accountmembers.OwnershipNoticeNewOwner
		}
		prepared, err := sender.PrepareOwnershipTransfer([]string{"28000000-0000-4000-8000-000000000001", "28000000-0000-4000-8000-000000000002"}[index], accountmembers.OwnershipTransferNotice{AccountID: "11111111-1111-4111-8111-111111111111", Email: email, DisplayName: "Recipient", AccountName: "Northstar", CounterpartDisplayName: "Counterpart", RecipientRole: role, OccurredAt: now})
		if err != nil {
			t.Fatal(err)
		}
		queue.entries = append(queue.entries, notifications.Entry{ID: prepared.ID, AccountID: prepared.AccountID, Kind: notifications.KindOwnership, Ciphertext: prepared.Ciphertext, Nonce: prepared.Nonce, KeyVersion: prepared.KeyVersion, CreatedAt: prepared.CreatedAt})
	}
	delivery := &delivery{ownershipErr: map[string]error{"previous@example.com": errors.New("mailbox unavailable")}}
	processor, _ := notifications.NewProcessor(queue, envelopeCipher, delivery, clock{now}, time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if !worked || !errors.Is(err, notifications.ErrDeliveryFailed) || queue.failed == "" || queue.terminal {
		t.Fatalf("first recipient worked=%v failed=%q terminal=%v err=%v", worked, queue.failed, queue.terminal, err)
	}
	worked, err = processor.ProcessOne(context.Background())
	if err != nil || !worked || delivery.ownership.Email != "new@example.com" || queue.delivered == "" {
		t.Fatalf("second recipient worked=%v notice=%+v delivered=%q err=%v", worked, delivery.ownership, queue.delivered, err)
	}
}

func TestExpiredNotificationIsNotDelivered(t *testing.T) {
	envelopeCipher, _ := notifications.NewCipher(bytes.Repeat([]byte{0x25}, 32), 1)
	queue := &fakeQueue{}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sender, _ := notifications.NewQueuedSender(queue, envelopeCipher, generator{"25000000-0000-4000-8000-000000000002"}, clock{now})
	if err := sender.SendRecovery(context.Background(), recovery.Message{Email: "owner@example.com", Token: "expired-token", ExpiresAt: now}); err != nil {
		t.Fatal(err)
	}
	delivery := &delivery{}
	processor, _ := notifications.NewProcessor(queue, envelopeCipher, delivery, clock{now}, time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || delivery.recovery.Token != "" || !queue.terminal || queue.failedCode != "expired" {
		t.Fatalf("expired result: worked=%v delivery=%+v terminal=%v code=%q err=%v", worked, delivery.recovery, queue.terminal, queue.failedCode, err)
	}
}

func TestTamperDeadLettersAndDeliveryFailureRetries(t *testing.T) {
	envelopeCipher, _ := notifications.NewCipher(bytes.Repeat([]byte{0x33}, 32), 1)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	queue := &fakeQueue{}
	sender, _ := notifications.NewQueuedSender(queue, envelopeCipher, generator{"30000000-0000-4000-8000-000000000003"}, clock{now})
	_ = sender.SendRecovery(context.Background(), recovery.Message{Email: "owner@example.com", Token: "secret", ExpiresAt: now.Add(time.Hour)})
	queue.entries[0].Ciphertext[0] ^= 0xff
	processor, _ := notifications.NewProcessor(queue, envelopeCipher, &delivery{}, clock{now}, time.Minute)
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || !queue.terminal || queue.failedCode != "envelope_invalid" {
		t.Fatalf("tamper result: worked=%v terminal=%v code=%q err=%v", worked, queue.terminal, queue.failedCode, err)
	}

	queue = &fakeQueue{}
	sender, _ = notifications.NewQueuedSender(queue, envelopeCipher, generator{"40000000-0000-4000-8000-000000000004"}, clock{now})
	_ = sender.SendRecovery(context.Background(), recovery.Message{Email: "owner@example.com", Token: "secret", ExpiresAt: now.Add(time.Hour)})
	processor, _ = notifications.NewProcessor(queue, envelopeCipher, &delivery{err: errors.New("SMTP unavailable")}, clock{now}, time.Minute)
	worked, err = processor.ProcessOne(context.Background())
	if !worked || !errors.Is(err, notifications.ErrDeliveryFailed) || queue.terminal || queue.failedCode != "delivery_failed" || !queue.next.After(now) {
		t.Fatalf("retry result: worked=%v terminal=%v code=%q next=%v err=%v", worked, queue.terminal, queue.failedCode, queue.next, err)
	}
}
