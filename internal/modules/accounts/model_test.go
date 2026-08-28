package accounts

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestNewAccountSlugIsReadableAndAccountUnique(t *testing.T) {
	now := time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC)
	first, err := NewAccount(ids.AccountID("10000000-0000-4000-8000-000000000001"), ids.UserID("20000000-0000-4000-8000-000000000001"), "cell-a", "Smith Auto Repair", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAccount(ids.AccountID("10000000-0000-4000-8000-000000000002"), ids.UserID("20000000-0000-4000-8000-000000000002"), "cell-a", "Smith Auto Repair", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Slug == second.Slug {
		t.Fatalf("duplicate slugs: %q", first.Slug)
	}
	if first.Slug != "smith-auto-repair-10000000000040008000000000000001" {
		t.Fatalf("unexpected slug %q", first.Slug)
	}
}
