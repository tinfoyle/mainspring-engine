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
