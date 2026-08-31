package strongauth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestRequireAcceptsRecentStrongEvidenceForTheActor(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	actor := ids.UserID("11111111-1111-4111-8111-111111111111")
	base := sessions.Session{UserID: actor, ReauthenticatedAt: now.Add(-strongauth.MaximumAge), ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	if err := strongauth.Require(base, actor, now); err != nil {
		t.Fatalf("recent passkey evidence was rejected: %v", err)
	}
	for _, method := range []sessions.AuthenticationMethod{sessions.AuthenticationMethodSMSOTP, sessions.AuthenticationMethodEmailOTP} {
		if err := strongauth.Require(sessions.Session{UserID: actor, ReauthenticatedAt: now, ReauthenticationMethod: method}, actor, now); err != nil {
			t.Fatalf("recent %s evidence was rejected: %v", method, err)
		}
	}

	cases := map[string]sessions.Session{
		"password":   {UserID: actor, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword},
		"stale":      {UserID: actor, ReauthenticatedAt: now.Add(-strongauth.MaximumAge - time.Nanosecond), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		"future":     {UserID: actor, ReauthenticatedAt: now.Add(time.Second), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		"other user": {UserID: ids.UserID("22222222-2222-4222-8222-222222222222"), ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
	}
	for name, session := range cases {
		t.Run(name, func(t *testing.T) {
			if err := strongauth.Require(session, actor, now); !errors.Is(err, strongauth.ErrRequired) {
				t.Fatalf("error = %v, want strong authentication required", err)
			}
		})
	}
}
