package mcpoauth

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
)

type recordingMetadataLoader struct {
	clientID string
	client   mcpauth.Client
	err      error
}

func (l *recordingMetadataLoader) Load(_ context.Context, clientID string) (mcpauth.Client, error) {
	l.clientID = clientID
	return l.client, l.err
}

func TestPinnedMetadataLoaderResolvesExactClientWithoutNetwork(t *testing.T) {
	fallback := &recordingMetadataLoader{err: errors.New("network should not be used")}
	client := mcpauth.Client{
		ID:           "https://agent-client.infiniteocean.localhost:8444/oauth/client-metadata.json",
		Name:         "Local agent journey",
		RedirectURIs: []string{"https://agent-client.infiniteocean.localhost:8444/callback"},
	}
	loader, err := NewPinnedMetadataLoader(fallback, client)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(context.Background(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != client.ID || loaded.Name != client.Name || len(loaded.RedirectURIs) != 1 || loaded.RedirectURIs[0] != client.RedirectURIs[0] {
		t.Fatalf("unexpected client: %#v", loaded)
	}
	if fallback.clientID != "" {
		t.Fatalf("fallback unexpectedly called with %q", fallback.clientID)
	}

	loaded.RedirectURIs[0] = "https://mutated.example/callback"
	again, err := loader.Load(context.Background(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.RedirectURIs[0] != client.RedirectURIs[0] {
		t.Fatal("loader returned mutable pinned metadata")
	}
}

func TestPinnedMetadataLoaderDelegatesUnknownClient(t *testing.T) {
	fallbackClient := mcpauth.Client{ID: "https://public.example/client.json", Name: "Public", RedirectURIs: []string{"https://public.example/callback"}}
	fallback := &recordingMetadataLoader{client: fallbackClient}
	loader, err := NewPinnedMetadataLoader(fallback)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(context.Background(), fallbackClient.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.clientID != fallbackClient.ID || loaded.ID != fallbackClient.ID {
		t.Fatalf("fallback was not used: id=%q client=%#v", fallback.clientID, loaded)
	}
}

func TestPinnedMetadataLoaderRejectsIncompleteAndDuplicateClients(t *testing.T) {
	fallback := &recordingMetadataLoader{}
	if _, err := NewPinnedMetadataLoader(nil); err == nil {
		t.Fatal("expected nil fallback rejection")
	}
	if _, err := NewPinnedMetadataLoader(fallback, mcpauth.Client{ID: "https://example.test/client.json"}); err == nil {
		t.Fatal("expected incomplete client rejection")
	}
	client := mcpauth.Client{ID: "https://example.test/client.json", Name: "Client", RedirectURIs: []string{"https://example.test/callback"}}
	if _, err := NewPinnedMetadataLoader(fallback, client, client); err == nil {
		t.Fatal("expected duplicate client rejection")
	}
}
