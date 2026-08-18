package migrations

import "testing"

func TestEmbeddedMigrationSetsLoad(t *testing.T) {
	for _, target := range []Target{Global, Cell, Development} {
		loaded, err := load(target)
		if err != nil {
			t.Fatalf("load %s migrations: %v", target, err)
		}
		if len(loaded) == 0 {
			t.Fatalf("expected embedded %s migrations", target)
		}
	}
}

func TestAtomicBodyRequiresTransactionEnvelope(t *testing.T) {
	if _, err := atomicBody("SELECT 1;"); err == nil {
		t.Fatal("expected an error for an unwrapped migration")
	}
	body, err := atomicBody("BEGIN;\nSELECT 1;\nCOMMIT;")
	if err != nil {
		t.Fatal(err)
	}
	if body != "SELECT 1;" {
		t.Fatalf("unexpected body %q", body)
	}
}
