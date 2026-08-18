package buildinfo

import "testing"

func TestCurrentCopiesLinkerIdentity(t *testing.T) {
	previous := Current()
	t.Cleanup(func() { Version, Revision, BuiltAt = previous.Version, previous.Revision, previous.BuiltAt })
	Version, Revision, BuiltAt = "2.0.0", "0123456789012345678901234567890123456789", "2026-08-18T18:00:00Z"

	got := Current()
	if got.Version != Version || got.Revision != Revision || got.BuiltAt != BuiltAt {
		t.Fatalf("unexpected build info: %+v", got)
	}
}
