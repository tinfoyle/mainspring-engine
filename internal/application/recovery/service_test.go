package recovery_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type repository struct {
	recipient recovery.Recipient
	exists    bool
	pending   recovery.Pending
	deleted   ids.RecoveryID
	completed [32]byte
}

func (r *repository) Create(_ context.Context, pending recovery.Pending) (recovery.Recipient, bool, error) {
	r.pending = pending
	return r.recipient, r.exists, nil
}
func (r *repository) Delete(_ context.Context, id ids.RecoveryID) error { r.deleted = id; return nil }
func (r *repository) Complete(_ context.Context, hash [32]byte, _ string, _ time.Time) (ids.UserID, error) {
	r.completed = hash
	return r.recipient.UserID, nil
}

type sender struct {
	message recovery.Message
	err     error
}

func (s *sender) SendRecovery(_ context.Context, message recovery.Message) error {
	s.message = message
	return s.err
}

type limiter struct {
	blocked  bool
	attempts int
}

func (l *limiter) Blocked(context.Context, [32]byte, time.Time) (bool, error) { return l.blocked, nil }
func (l *limiter) Failure(context.Context, [32]byte, time.Time, int, time.Duration) error {
	l.attempts++
	return nil
}

type passwords struct{ err error }

func (p passwords) Hash(value string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return "encoded:" + value, nil
}

type generator struct{}

func (generator) New() string { return "10000000-0000-4000-8000-000000000001" }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type networkLimiter struct{ allowed bool }

func (l networkLimiter) Consume(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return l.allowed, nil
}

func networkGuard(allowed bool) *abuse.Guard {
	guard, _ := abuse.NewGuard(networkLimiter{allowed: allowed})
	return guard
}

var testActor = [32]byte{1}

func TestBeginIsGenericForUnknownIdentityAndSendsKnownIdentity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repo := &repository{}
	delivery := &sender{}
	limits := &limiter{}
	service, err := recovery.NewService(repo, delivery, limits, networkGuard(true), passwords{}, generator{}, clock{now})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := service.Begin(context.Background(), recovery.BeginCommand{Email: "missing@example.com", NetworkActor: testActor})
	if err != nil || unknown.Delivered || !delivery.message.Suppress || delivery.message.Token == "" || limits.attempts != 1 {
		t.Fatalf("unknown recovery = %+v, message=%+v attempts=%d err=%v", unknown, delivery.message, limits.attempts, err)
	}
	repo.exists = true
	repo.recipient = recovery.Recipient{UserID: ids.UserID("user-a"), Email: "owner@example.com", DisplayName: "Owner"}
	returnTo := "/app/checkout?offer=team-monthly-v1&ref=IO-PARTNER1"
	started, err := service.Begin(context.Background(), recovery.BeginCommand{Email: " OWNER@example.com ", NetworkActor: testActor, ReturnTo: returnTo})
	if err != nil || !started.Delivered || delivery.message.Token == "" || delivery.message.Email != "owner@example.com" || delivery.message.ReturnTo != returnTo {
		t.Fatalf("known recovery = %+v, message=%+v err=%v", started, delivery.message, err)
	}
	if repo.pending.Email != "owner@example.com" || repo.pending.TokenHash != sha256.Sum256([]byte(delivery.message.Token)) {
		t.Fatal("recovery challenge was not normalized and bound to the delivered token")
	}
}

func TestDeliveryFailureDeletesChallengeAndCompletionHashesCredential(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repo := &repository{exists: true, recipient: recovery.Recipient{UserID: ids.UserID("user-a"), Email: "owner@example.com"}}
	delivery := &sender{err: errors.New("mail unavailable")}
	service, _ := recovery.NewService(repo, delivery, &limiter{}, networkGuard(true), passwords{}, generator{}, clock{now})
	if _, err := service.Begin(context.Background(), recovery.BeginCommand{Email: "owner@example.com", NetworkActor: testActor}); err == nil || repo.deleted == "" {
		t.Fatalf("delivery failure = %v, deleted=%q", err, repo.deleted)
	}
	delivery.err = nil
	started, err := service.Begin(context.Background(), recovery.BeginCommand{Email: "owner@example.com", NetworkActor: testActor})
	if err != nil || !started.Delivered {
		t.Fatal(err)
	}
	if err := service.Complete(context.Background(), recovery.CompleteCommand{Token: delivery.message.Token, Password: "new password material"}); err != nil {
		t.Fatal(err)
	}
	if repo.completed != sha256.Sum256([]byte(delivery.message.Token)) {
		t.Fatal("completion did not hash the opaque token")
	}
}

func TestNetworkBudgetProducesDiscardForKnownIdentity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repo := &repository{exists: true, recipient: recovery.Recipient{UserID: ids.UserID("user-a"), Email: "owner@example.com"}}
	delivery := &sender{}
	service, _ := recovery.NewService(repo, delivery, &limiter{}, networkGuard(false), passwords{}, generator{}, clock{now})
	result, err := service.Begin(context.Background(), recovery.BeginCommand{Email: "owner@example.com", NetworkActor: testActor})
	if err != nil || result.Delivered || !delivery.message.Suppress || repo.pending.ID != "" {
		t.Fatalf("network-limited recovery = %+v, message=%+v pending=%+v err=%v", result, delivery.message, repo.pending, err)
	}
}

func TestInvalidPasswordIsClassified(t *testing.T) {
	service, _ := recovery.NewService(&repository{}, &sender{}, &limiter{}, networkGuard(true), passwords{err: errors.New("short")}, generator{}, clock{time.Now()})
	err := service.Complete(context.Background(), recovery.CompleteCommand{Token: "token", Password: "short"})
	if !errors.Is(err, recovery.ErrInvalidPassword) {
		t.Fatalf("password result = %v", err)
	}
}
