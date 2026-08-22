package knowledge

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkExtractedTextIsDeterministicAndByteAddressed(t *testing.T) {
	text := []byte(strings.Repeat("alpha beta gamma delta. ", 260) + "\n\n" + strings.Repeat("naïve café 航海. ", 180))
	first, err := ChunkExtractedText(text)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChunkExtractedText(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first) < 3 {
		t.Fatalf("non-deterministic or insufficient chunks: first=%d second=%d", len(first), len(second))
	}
	for index, chunk := range first {
		if chunk.EndByte <= chunk.StartByte || chunk.EndByte > int64(len(text)) || int(chunk.EndByte-chunk.StartByte) > MaximumDocumentChunkBytes || chunk.TokenCount == 0 {
			t.Fatalf("chunk %d has invalid bounds or token count: %+v", index, chunk)
		}
		if chunk.Content != string(text[chunk.StartByte:chunk.EndByte]) || !utf8.ValidString(chunk.Content) {
			t.Fatalf("chunk %d does not address exact UTF-8 source bytes", index)
		}
		if index > 0 && chunk.StartByte >= first[index-1].EndByte {
			t.Fatalf("chunk %d does not overlap its predecessor", index)
		}
	}
}

func TestChunkExtractedTextPrefersSemanticBoundary(t *testing.T) {
	firstParagraph := strings.Repeat("paragraph-one ", 190)
	text := []byte(firstParagraph + "\n\n" + strings.Repeat("paragraph-two ", 220))
	chunks, err := ChunkExtractedText(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 || !strings.HasSuffix(chunks[0].Content, "paragraph-one") {
		t.Fatalf("first boundary was not placed at the paragraph: %+v", chunks[0])
	}
}

func TestChunkExtractedTextRejectsInvalidInput(t *testing.T) {
	for name, value := range map[string][]byte{
		"empty":      nil,
		"whitespace": []byte(" \n\t"),
		"nul":        []byte("before\x00after"),
		"utf8":       {0xff},
	} {
		t.Run(name, func(t *testing.T) {
			if chunks, err := ChunkExtractedText(value); err == nil || chunks != nil {
				t.Fatalf("chunks=%v err=%v", chunks, err)
			}
		})
	}
	over := bytes.Repeat([]byte("x"), 8<<20+1)
	if _, err := ChunkExtractedText(over); err == nil {
		t.Fatal("oversized extracted text was accepted")
	}
}
