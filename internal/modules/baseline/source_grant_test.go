package baseline

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestSourceGrantFreezesNarrowReadOnlyScopeAndRevokes(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	since, until := now.Add(-30*24*time.Hour), now
	actor := Actor{UserID: "f1000000-0000-4000-8000-000000000001"}
	grant, err := NewSourceGrant(SourceGrantDraft{ID: "f2000000-0000-4000-8000-000000000002", AccountID: "f3000000-0000-4000-8000-000000000003", AssessmentID: "f4000000-0000-4000-8000-000000000004", ConnectionID: "f5000000-0000-4000-8000-000000000005", Kind: SourceEmail, Scope: SourceScope{Folders: []string{" Receipts ", "INBOX"}, Since: &since, Until: &until}, GrantedBy: actor}, now)
	if err != nil || grant.Scope.Folders[0] != "INBOX" || grant.State != SourceGrantActive {
		t.Fatalf("grant=%+v err=%v", grant, err)
	}
	if _, err := grant.Revoke(actor, accounts.RoleMember, "No longer required", grant.Version, now.Add(time.Minute)); !errors.Is(err, ErrRole) {
		t.Fatalf("member revoke error=%v", err)
	}
	revoked, err := grant.Revoke(actor, accounts.RoleOwner, "Connection is no longer required", grant.Version, now.Add(time.Minute))
	if err != nil || revoked.State != SourceGrantRevoked || revoked.Version != 2 || revoked.RevokedAt == nil {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	grant.Scope.Folders[0] = "changed"
	if revoked.Scope.Folders[0] != "INBOX" {
		t.Fatal("grant scope shared caller storage")
	}
}

func TestSourceGrantRejectsBroadOrMismatchedScope(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	draft := SourceGrantDraft{ID: ids.BaselineSourceGrantID("f2000000-0000-4000-8000-000000000002"), AccountID: "f3000000-0000-4000-8000-000000000003", AssessmentID: "f4000000-0000-4000-8000-000000000004", ConnectionID: "f5000000-0000-4000-8000-000000000005", Kind: SourceGoogleDrive, Scope: SourceScope{Folders: []string{"folder-a"}}, GrantedBy: Actor{UserID: "f1000000-0000-4000-8000-000000000001"}}
	if _, err := NewSourceGrant(draft, now); err != nil {
		t.Fatal(err)
	}
	since := now.Add(-time.Hour)
	draft.Scope.Since = &since
	if _, err := NewSourceGrant(draft, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Drive date scope error=%v", err)
	}
	draft.Kind, draft.Scope = SourceEmail, SourceScope{Folders: []string{"INBOX", "INBOX"}}
	if _, err := NewSourceGrant(draft, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate folder error=%v", err)
	}
}
