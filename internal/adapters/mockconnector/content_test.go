package mockconnector

import (
	"context"
	"io"
	"testing"

	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

func TestContentSourceCopiesAndOpensOnlyConfiguredImmutableReference(t *testing.T) {
	body := []byte("approved content")
	source, err := NewContentSource(map[string][]byte{"objects/approved": body})
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'X'
	reader, err := source.OpenContent(context.Background(), marketingdomain.AssetRevision{ContentReference: "objects/approved"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(loaded) != "approved content" {
		t.Fatalf("loaded=%q read=%v close=%v", loaded, readErr, closeErr)
	}
	loaded[0] = 'Y'
	reopened, err := source.OpenContent(context.Background(), marketingdomain.AssetRevision{ContentReference: "objects/approved"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := io.ReadAll(reopened)
	_ = reopened.Close()
	if string(again) != "approved content" {
		t.Fatalf("content mutated through reader: %q", again)
	}
	if _, err := source.OpenContent(context.Background(), marketingdomain.AssetRevision{ContentReference: "objects/missing"}); err == nil {
		t.Fatal("missing content reference opened")
	}
}
