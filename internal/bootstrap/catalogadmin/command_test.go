package catalogadmin

import "testing"

func TestDecodeCatalogRejectsUnknownAndMultipleDocuments(t *testing.T) {
	if _, err := decodeCatalog([]byte(`{"version":1,"packages":[],"plans":[],"offers":[],"unexpected":true}`)); err == nil {
		t.Fatal("expected unknown catalog field rejection")
	}
	if _, err := decodeCatalog([]byte(`{"version":1,"packages":[],"plans":[],"offers":[]} {}`)); err == nil {
		t.Fatal("expected multiple catalog document rejection")
	}
}
