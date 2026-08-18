package sessions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type generator struct{ n int }

func (g *generator) New() string {
	g.n++
	return "00000000-0000-4000-8000-00000000000" + string(rune('0'+g.n))
}

func TestSessionRotationAndRevocation(t *testing.T) {
	clock := &clock{now: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	service, err := sessions.NewService(memory.NewSessionStore(), &generator{}, clock, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Issue(context.Background(), ids.UserID("user-a"), 1)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(16 * time.Minute)
	authenticated, err := service.Authenticate(context.Background(), issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.RotatedToken == "" {
		t.Fatal("expected rotated token")
	}
	if _, err := service.Authenticate(context.Background(), issued.Token); !errors.Is(err, sessions.ErrInvalidSession) {
		t.Fatalf("old token should be invalid: %v", err)
	}
	if err := service.RevokeAll(context.Background(), ids.UserID("user-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), authenticated.RotatedToken); !errors.Is(err, sessions.ErrExpiredSession) {
		t.Fatalf("revoked token should be expired: %v", err)
	}
}

func TestSessionInventoryOwnershipAndReauthentication(t *testing.T) {
	clock := &clock{now: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
	service, err := sessions.NewService(memory.NewSessionStore(), &generator{}, clock, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.IssueForClient(context.Background(), ids.UserID("user-a"), 1, "Firefox on Linux")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.IssueForClient(context.Background(), ids.UserID("user-a"), 1, "Safari on iPhone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.IssueForClient(context.Background(), ids.UserID("user-b"), 1, "Other user"); err != nil {
		t.Fatal(err)
	}
	active, err := service.Active(context.Background(), ids.UserID("user-a"), first.Session.ID)
	if err != nil || len(active) != 2 {
		t.Fatalf("active sessions = %d, %v", len(active), err)
	}
	if !active[0].Current && !active[1].Current {
		t.Fatal("current session was not identified")
	}
	if revoked, err := service.RevokeOwned(context.Background(), ids.UserID("user-b"), second.Session.ID); err != nil || revoked {
		t.Fatalf("cross-user revoke = %v, %v", revoked, err)
	}
	if revoked, err := service.RevokeOwned(context.Background(), ids.UserID("user-a"), second.Session.ID); err != nil || !revoked {
		t.Fatalf("owned revoke = %v, %v", revoked, err)
	}
	clock.now = clock.now.Add(20 * time.Minute)
	if sessions.RecentlyReauthenticated(first.Session, clock.now, 10*time.Minute) {
		t.Fatal("stale initial authentication should not satisfy recent-auth policy")
	}
	if err := service.MarkReauthenticated(context.Background(), ids.UserID("user-a"), first.Session.ID); err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.Authenticate(context.Background(), first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !service.RecentlyReauthenticated(refreshed.Session, 10*time.Minute) {
		t.Fatal("password confirmation did not refresh recent-auth policy")
	}
}
