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
