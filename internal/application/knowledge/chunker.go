package knowledge

import (
	"bytes"
	"strings"
	"unicode/utf8"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const (
	DocumentChunkGeneration   = "spyglass/utf8-window-v1"
	MaximumDocumentChunkBytes = 4096
	DocumentChunkOverlapBytes = 512
)

// ChunkExtractedText creates deterministic, byte-addressed windows over the
// normalized extracted object. Boundaries prefer paragraphs, then lines and
// whitespace, while remaining valid UTF-8. The offsets always address the
// exact bytes stored in Content.
func ChunkExtractedText(text []byte) ([]DocumentChunkDraft, error) {
	if len(text) == 0 || int64(len(text)) > knowledgedomain.MaximumExtractedTextBytes || !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 {
		return nil, ErrInvalid
	}
	if strings.TrimSpace(string(text)) == "" {
		return nil, ErrInvalid
	}

	chunks := make([]DocumentChunkDraft, 0, len(text)/MaximumDocumentChunkBytes+1)
	for start := 0; start < len(text); {
		end := min(start+MaximumDocumentChunkBytes, len(text))
		if end < len(text) {
			end = preferredChunkEnd(text, start, end)
		}
		for end < len(text) && end > start && !utf8.RuneStart(text[end]) {
			end--
		}
		if end <= start {
			return nil, ErrInvalid
		}

		contentStart, contentEnd := trimChunkBounds(text, start, end)
		if contentStart < contentEnd {
			content := string(text[contentStart:contentEnd])
			tokens := len(strings.Fields(content))
			if tokens == 0 || len(chunks) >= int(knowledgedomain.MaximumDocumentChunks) {
				return nil, ErrInvalid
			}
			chunks = append(chunks, DocumentChunkDraft{
				StartByte:  int64(contentStart),
				EndByte:    int64(contentEnd),
				Content:    content,
				TokenCount: uint32(tokens),
			})
		}
		if end == len(text) {
			break
		}

		next := end - min(DocumentChunkOverlapBytes, end-start-1)
		for next < end && !utf8.RuneStart(text[next]) {
			next++
		}
		if next <= start || next >= end {
			return nil, ErrInvalid
		}
		start = next
	}
	if len(chunks) == 0 {
		return nil, ErrInvalid
	}
	return chunks, nil
}

func preferredChunkEnd(text []byte, start, maximum int) int {
	minimum := start + (maximum-start)/2
	window := text[minimum:maximum]
	if index := bytes.LastIndex(window, []byte("\n\n")); index >= 0 {
		return minimum + index + 2
	}
	if index := bytes.LastIndexByte(window, '\n'); index >= 0 {
		return minimum + index + 1
	}
	for index := len(window) - 1; index >= 0; index-- {
		switch window[index] {
		case ' ', '\t':
			return minimum + index + 1
		}
	}
	return maximum
}

func trimChunkBounds(text []byte, start, end int) (int, int) {
	content := string(text[start:end])
	left := strings.TrimLeftFunc(content, func(value rune) bool {
		return value == ' ' || value == '\t' || value == '\n' || value == '\r'
	})
	start += len(content) - len(left)
	content = strings.TrimRightFunc(left, func(value rune) bool {
		return value == ' ' || value == '\t' || value == '\n' || value == '\r'
	})
	return start, start + len(content)
}
