package knowledge

import (
	"context"
	"crypto/sha256"
	"io"
)

type TextExtractionRequest struct {
	Body      io.Reader
	Size      int64
	MediaType string
}

type TextExtractionResult struct {
	Text       []byte
	TextSHA256 [sha256.Size]byte
	Extractor  string
}

type TextExtractor interface {
	Verify(context.Context) error
	Extract(context.Context, TextExtractionRequest) (TextExtractionResult, error)
}
