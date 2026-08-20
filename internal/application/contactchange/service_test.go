package contactchange

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestBeginRequiresRecentPasskeyAndPreservesCurrentEmail(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	verified := now.Add(-time.Hour)
	repository := &fakeRepository{user: identity.User{ID: "00000000-0000-4000-8000-000000000001", PrimaryEmail: "owner@example.com", DisplayName: "Owner", State: identity.UserActive, EmailVerifiedAt: &verified, SecurityVersion: 4}}
	preparer := &fakePreparer{}
	service, err := NewService(repository, preparer, &sequenceGenerator{}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	session := sessions.Session{UserID: repository.user.ID, SecurityVersion: 4, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	if _, err := service.Begin(context.Background(), BeginCommand{Session: session, NewEmail: "new@example.com"}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("password-only begin error = %v", err)
	}
	session.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	result, err := service.Begin(context.Background(), BeginCommand{Session: session, NewEmail: " NEW@example.com "})
	if err != nil {
		t.Fatal(err)
	}
	if result.NewEmail != "new@example.com" || repository.user.PrimaryEmail != "owner@example.com" || repository.pending.NewEmail != result.NewEmail {
		t.Fatalf("begin result=%+v user=%+v pending=%+v", result, repository.user, repository.pending)
	}
	if len(preparer.messages) != 2 || preparer.messages[0].Action != ActionVerifyNew || preparer.messages[0].Email != "new@example.com" || len(preparer.messages[0].Token) != 43 || preparer.messages[1].Action != ActionRequested || preparer.messages[1].Email != "owner@example.com" {
		t.Fatalf("prepared messages = %+v", preparer.messages)
	}
}

func TestBeginRejectsCurrentEmail(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	verified := now.Add(-time.Hour)
	repository := &fakeRepository{user: identity.User{ID: "00000000-0000-4000-8000-000000000001", PrimaryEmail: "owner@example.com", DisplayName: "Owner", State: identity.UserActive, EmailVerifiedAt: &verified, SecurityVersion: 1}}
	service, _ := NewService(repository, &fakePreparer{}, &sequenceGenerator{}, fixedClock{now})
	session := sessions.Session{UserID: repository.user.ID, SecurityVersion: 1, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	if _, err := service.Begin(context.Background(), BeginCommand{Session: session, NewEmail: "OWNER@example.com"}); !errors.Is(err, ErrSameEmail) {
		t.Fatalf("same-email error = %v", err)
	}
}

func TestCompletePreparesBothNoticesAfterTokenValidation(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	token := "verification-token"
	repository := &fakeRepository{pending: Pending{ID: "00000000-0000-4000-8000-000000000010", UserID: "00000000-0000-4000-8000-000000000001", OldEmail: "old@example.com", NewEmail: "new@example.com", DisplayName: "Owner", SecurityVersion: 7, TokenHash: sha256.Sum256([]byte(token)), ExpiresAt: now.Add(time.Minute), CreatedAt: now.Add(-time.Minute)}}
	preparer := &fakePreparer{}
	service, _ := NewService(repository, preparer, &sequenceGenerator{}, fixedClock{now})
	result, err := service.Complete(context.Background(), CompleteCommand{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if result.NewEmail != "new@example.com" || result.SecurityVersion != 8 || len(preparer.messages) != 2 {
		t.Fatalf("result=%+v messages=%+v", result, preparer.messages)
	}
	if preparer.messages[0].Action != ActionCompleted || preparer.messages[0].Email != "old@example.com" || preparer.messages[1].Email != "new@example.com" {
		t.Fatalf("completion notices = %+v", preparer.messages)
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type sequenceGenerator struct{ next int }

func (g *sequenceGenerator) New() string {
	g.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", g.next)
}

type fakePreparer struct{ messages []Message }

func (p *fakePreparer) PrepareContactChange(id string, message Message) (PreparedNotification, error) {
	p.messages = append(p.messages, message)
	return PreparedNotification{ID: id, Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"), KeyVersion: 1, CreatedAt: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)}, nil
}

type fakeRepository struct {
	user    identity.User
	pending Pending
}

func (r *fakeRepository) User(context.Context, ids.UserID) (identity.User, error) { return r.user, nil }
func (r *fakeRepository) CreatePending(_ context.Context, pending Pending, _ []PreparedNotification) error {
	r.pending = pending
	return nil
}
func (r *fakeRepository) Complete(_ context.Context, hash [32]byte, now time.Time, prepare func(Pending) ([]PreparedNotification, error)) (Completed, error) {
	if r.pending.TokenHash != hash {
		return Completed{}, ErrNotFound
	}
	if _, err := prepare(r.pending); err != nil {
		return Completed{}, err
	}
	return Completed{UserID: r.pending.UserID, OldEmail: r.pending.OldEmail, NewEmail: r.pending.NewEmail, SecurityVersion: r.pending.SecurityVersion + 1, ChangedAt: now}, nil
}
