package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestClientAddsTenantCredentialsAndDecodesDocuments(t *testing.T) {
	tenantID := domain.NewTenantID()
	token := "document-client-test-token-at-least-thirty-two-bytes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-Mainspring-Tenant-ID") != tenantID.String() {
			t.Fatal("client did not send tenant RAG credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[{"id":"doc-1","name":"Field notes","media_type":"text/plain","status":"ready"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := client.ListDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].Name != "Field notes" {
		t.Fatalf("documents = %#v", documents)
	}
}

func TestClientMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, err := NewClient(server.URL, "document-client-test-token-at-least-thirty-two-bytes", domain.NewTenantID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetDocument(context.Background(), "missing"); err != ErrDocumentNotFound {
		t.Fatalf("error = %v, want ErrDocumentNotFound", err)
	}
}
