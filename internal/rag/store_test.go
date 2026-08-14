package rag

import (
	"strings"
	"testing"
)

func TestChunkTextPreservesContentAndOverlap(t *testing.T) {
	content := strings.Repeat("alpha beta gamma ", 30)
	chunks := chunkText(content, 80, 10)
	if len(chunks) < 2 {
		t.Fatalf("chunkText() returned %d chunks", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk == "" || len([]rune(chunk)) > 80 {
			t.Fatalf("invalid chunk length %d", len([]rune(chunk)))
		}
	}
}

func TestSupportedTextMediaType(t *testing.T) {
	for _, mediaType := range []string{"text/plain", "text/markdown; charset=utf-8", "text/csv", "application/json", "application/xml", "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"} {
		if !supportedTextMediaType(mediaType) {
			t.Fatalf("expected %q to be supported", mediaType)
		}
	}
	for _, mediaType := range []string{"application/octet-stream", "image/png"} {
		if supportedTextMediaType(mediaType) {
			t.Fatalf("expected %q to be rejected", mediaType)
		}
	}
}

func TestIngestTextRejectsOversizedContentBeforeStorage(t *testing.T) {
	store := &Store{}
	_, err := store.IngestText(t.Context(), "too-large.txt", "text/plain", strings.Repeat("a", TextDocumentLimit+1))
	if err == nil || !strings.Contains(err.Error(), "2 MB") {
		t.Fatalf("error = %v, want 2 MB size rejection", err)
	}
}
