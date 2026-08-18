// Package notifications provides encrypted, durable identity and Account
// governance notification enqueueing and leased asynchronous delivery.
package notifications

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Kind string

const (
	KindVerification Kind = "verification"
	KindInvitation   Kind = "invitation"
	KindRecovery     Kind = "recovery"
	KindDiscard      Kind = "discard"
	KindOwnership    Kind = "ownership_transfer"
)

var ErrDeliveryFailed = errors.New("notification delivery failed")

type Entry struct {
	ID           string
	Kind         Kind
	Ciphertext   []byte
	Nonce        []byte
	KeyVersion   int
	CreatedAt    time.Time
	AttemptCount int
}

type Queue interface {
	Enqueue(context.Context, Entry) error
	Claim(context.Context, time.Time, time.Duration) (Entry, bool, error)
	MarkDelivered(context.Context, string, time.Time) error
	MarkFailed(context.Context, string, time.Time, time.Time, string, bool) error
}

type Clock interface{ Now() time.Time }

type Cipher struct {
	aead       cipher.AEAD
	keyVersion int
}

func NewCipher(key []byte, keyVersion int) (*Cipher, error) {
	if len(key) != 32 || keyVersion <= 0 {
		return nil, errors.New("notification encryption requires a 32-byte key and positive key version")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *Cipher) Seal(id string, kind Kind, plaintext []byte) ([]byte, []byte, int, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, err
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, additionalData(id, kind))
	return sealed, nonce, c.keyVersion, nil
}

func (c *Cipher) Open(entry Entry) ([]byte, error) {
	if entry.KeyVersion != c.keyVersion || len(entry.Nonce) != c.aead.NonceSize() {
		return nil, errors.New("notification envelope key or nonce is invalid")
	}
	plaintext, err := c.aead.Open(nil, entry.Nonce, entry.Ciphertext, additionalData(entry.ID, entry.Kind))
	if err != nil {
		return nil, errors.New("notification envelope authentication failed")
	}
	return plaintext, nil
}

func additionalData(id string, kind Kind) []byte { return []byte(id + "/" + string(kind)) }

type payload struct {
	Email, DisplayName, Token, AccountName, Role, CounterpartDisplayName, RecipientRole string
	ExpiresAt, OccurredAt                                                               time.Time
}

type QueuedSender struct {
	queue  Queue
	cipher *Cipher
	ids    ids.Generator
	clock  Clock
}

func NewQueuedSender(queue Queue, envelopeCipher *Cipher, generator ids.Generator, clock Clock) (*QueuedSender, error) {
	if queue == nil || envelopeCipher == nil || generator == nil || clock == nil {
		return nil, errors.New("queued notification dependencies are required")
	}
	return &QueuedSender{queue: queue, cipher: envelopeCipher, ids: generator, clock: clock}, nil
}

func (s *QueuedSender) SendVerification(ctx context.Context, message registration.VerificationMessage) error {
	return s.enqueue(ctx, KindVerification, payload{Email: message.Email, DisplayName: message.DisplayName, Token: message.Token, ExpiresAt: message.ExpiresAt})
}

func (s *QueuedSender) SendInvitation(ctx context.Context, message invitations.Message) error {
	return s.enqueue(ctx, KindInvitation, payload{Email: message.Email, Token: message.Token, AccountName: message.AccountName, Role: string(message.Role), ExpiresAt: message.ExpiresAt})
}

func (s *QueuedSender) SendRecovery(ctx context.Context, message recovery.Message) error {
	if message.Suppress {
		return s.enqueue(ctx, KindDiscard, payload{Token: message.Token, ExpiresAt: message.ExpiresAt})
	}
	return s.enqueue(ctx, KindRecovery, payload{Email: message.Email, DisplayName: message.DisplayName, Token: message.Token, ExpiresAt: message.ExpiresAt})
}

func (s *QueuedSender) PrepareOwnershipTransfer(id string, message accountmembers.OwnershipTransferNotice) (accountmembers.PreparedNotification, error) {
	raw, err := json.Marshal(payload{Email: message.Email, DisplayName: message.DisplayName, AccountName: message.AccountName, CounterpartDisplayName: message.CounterpartDisplayName, RecipientRole: string(message.RecipientRole), OccurredAt: message.OccurredAt.UTC()})
	if err != nil {
		return accountmembers.PreparedNotification{}, err
	}
	ciphertext, nonce, keyVersion, err := s.cipher.Seal(id, KindOwnership, raw)
	if err != nil {
		return accountmembers.PreparedNotification{}, err
	}
	return accountmembers.PreparedNotification{ID: id, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: keyVersion, CreatedAt: s.clock.Now().UTC()}, nil
}

func (s *QueuedSender) enqueue(ctx context.Context, kind Kind, value payload) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	id := s.ids.New()
	ciphertext, nonce, keyVersion, err := s.cipher.Seal(id, kind, raw)
	if err != nil {
		return err
	}
	return s.queue.Enqueue(ctx, Entry{ID: id, Kind: kind, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: keyVersion, CreatedAt: s.clock.Now().UTC()})
}

type Delivery interface {
	registration.VerificationSender
	invitations.Sender
	recovery.Sender
	accountmembers.OwnershipTransferSender
}

type Processor struct {
	queue    Queue
	cipher   *Cipher
	delivery Delivery
	clock    Clock
	lease    time.Duration
}

func NewProcessor(queue Queue, envelopeCipher *Cipher, delivery Delivery, clock Clock, lease time.Duration) (*Processor, error) {
	if queue == nil || envelopeCipher == nil || delivery == nil || clock == nil || lease <= 0 {
		return nil, errors.New("notification processor dependencies are required")
	}
	return &Processor{queue: queue, cipher: envelopeCipher, delivery: delivery, clock: clock, lease: lease}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	entry, ok, err := p.queue.Claim(ctx, now, p.lease)
	if err != nil || !ok {
		return ok, err
	}
	raw, err := p.cipher.Open(entry)
	if err != nil {
		return true, p.queue.MarkFailed(ctx, entry.ID, now, now, "envelope_invalid", true)
	}
	var value payload
	if err := json.Unmarshal(raw, &value); err != nil {
		return true, p.queue.MarkFailed(ctx, entry.ID, now, now, "payload_invalid", true)
	}
	if entry.Kind == KindOwnership && !validOwnershipPayload(value) {
		return true, p.queue.MarkFailed(ctx, entry.ID, now, now, "payload_invalid", true)
	}
	if entry.Kind == KindDiscard {
		return true, p.queue.MarkDelivered(ctx, entry.ID, now)
	}
	if entry.Kind != KindOwnership && !value.ExpiresAt.After(now) {
		return true, p.queue.MarkFailed(ctx, entry.ID, now, now, "expired", true)
	}
	err = p.deliver(ctx, entry.Kind, value)
	if err == nil {
		return true, p.queue.MarkDelivered(ctx, entry.ID, now)
	}
	terminal := entry.AttemptCount >= 12
	next := now.Add(retryDelay(entry.AttemptCount))
	if markErr := p.queue.MarkFailed(ctx, entry.ID, now, next, "delivery_failed", terminal); markErr != nil {
		return true, errors.Join(ErrDeliveryFailed, markErr)
	}
	// SMTP responses can echo recipient addresses. Keep provider text out of the
	// worker log and expose only the stable outbox error code to operators.
	return true, ErrDeliveryFailed
}

func (p *Processor) deliver(ctx context.Context, kind Kind, value payload) error {
	switch kind {
	case KindVerification:
		return p.delivery.SendVerification(ctx, registration.VerificationMessage{Email: value.Email, DisplayName: value.DisplayName, Token: value.Token, ExpiresAt: value.ExpiresAt})
	case KindInvitation:
		return p.delivery.SendInvitation(ctx, invitations.Message{Email: value.Email, Token: value.Token, AccountName: value.AccountName, Role: accounts.MembershipRole(value.Role), ExpiresAt: value.ExpiresAt})
	case KindRecovery:
		return p.delivery.SendRecovery(ctx, recovery.Message{Email: value.Email, DisplayName: value.DisplayName, Token: value.Token, ExpiresAt: value.ExpiresAt})
	case KindOwnership:
		role := accountmembers.OwnershipNoticeRole(value.RecipientRole)
		return p.delivery.SendOwnershipTransfer(ctx, accountmembers.OwnershipTransferNotice{Email: value.Email, DisplayName: value.DisplayName, AccountName: value.AccountName, CounterpartDisplayName: value.CounterpartDisplayName, RecipientRole: role, OccurredAt: value.OccurredAt})
	default:
		return fmt.Errorf("unsupported notification kind %q", kind)
	}
}

func validOwnershipPayload(value payload) bool {
	role := accountmembers.OwnershipNoticeRole(value.RecipientRole)
	return value.Email != "" && value.DisplayName != "" && value.AccountName != "" && value.CounterpartDisplayName != "" && !value.OccurredAt.IsZero() && (role == accountmembers.OwnershipNoticePreviousOwner || role == accountmembers.OwnershipNoticeNewOwner)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 15 * time.Second
	for index := 1; index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

var _ registration.VerificationSender = (*QueuedSender)(nil)
var _ invitations.Sender = (*QueuedSender)(nil)
var _ recovery.Sender = (*QueuedSender)(nil)
var _ accountmembers.OwnershipNotificationPreparer = (*QueuedSender)(nil)
