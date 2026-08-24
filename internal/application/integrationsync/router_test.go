package integrationsync

import (
	"context"
	"errors"
	"testing"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

type routedProvider struct{ called int }

func (provider *routedProvider) Sync(context.Context, ProviderRequest) (ProviderPage, error) {
	provider.called++
	return ProviderPage{NextCursor: []byte("routed")}, nil
}

func TestProviderRouterUsesExactKindAndCredentialProvider(t *testing.T) {
	drive := &routedProvider{}
	email := &routedProvider{}
	router, err := NewProviderRouter([]ProviderRoute{
		{Kind: domain.ConnectorGoogleDrive, CredentialProvider: "google_oauth", Provider: drive},
		{Kind: domain.ConnectorEmail, CredentialProvider: "imap", Provider: email},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := router.Sync(context.Background(), ProviderRequest{SourceKind: domain.ConnectorEmail, CredentialProvider: "imap"})
	if err != nil || string(page.NextCursor) != "routed" || email.called != 1 || drive.called != 0 {
		t.Fatalf("page=%+v err=%v email=%d drive=%d", page, err, email.called, drive.called)
	}
	if _, err := router.Sync(context.Background(), ProviderRequest{SourceKind: domain.ConnectorEmail, CredentialProvider: "google_oauth"}); !errors.Is(err, ErrUnavailable) || email.called != 1 || drive.called != 0 {
		t.Fatalf("wrong-provider err=%v email=%d drive=%d", err, email.called, drive.called)
	}
}

func TestProviderRouterRejectsAmbiguousOrInvalidRoutes(t *testing.T) {
	provider := &routedProvider{}
	if _, err := NewProviderRouter(nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty routes err=%v", err)
	}
	if _, err := NewProviderRouter([]ProviderRoute{{Kind: domain.ConnectorEmail, CredentialProvider: "IMAP", Provider: provider}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid provider err=%v", err)
	}
	if _, err := NewProviderRouter([]ProviderRoute{
		{Kind: domain.ConnectorEmail, CredentialProvider: "imap", Provider: provider},
		{Kind: domain.ConnectorEmail, CredentialProvider: "imap", Provider: provider},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate route err=%v", err)
	}
}
