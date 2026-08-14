package webresearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

type SearchRequest struct {
	Query          string
	Limit          int
	IncludeDomains []string
	ExcludeDomains []string
	RecencyDays    int
}

type SearchResult struct {
	CitationID  string    `json:"citation_id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

type ReadRequest struct {
	URL           string
	MaxCharacters int
}

type ReadResult struct {
	CitationID  string    `json:"citation_id"`
	Title       string    `json:"title,omitempty"`
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	Content     string    `json:"content"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

type Provider interface {
	Search(context.Context, SearchRequest) ([]SearchResult, error)
	Read(context.Context, ReadRequest) (ReadResult, error)
}

func CitationID(rawURL string) string {
	normalized := strings.TrimSpace(rawURL)
	if parsed, err := url.Parse(normalized); err == nil {
		parsed.Fragment = ""
		normalized = parsed.String()
	}
	sum := sha256.Sum256([]byte(normalized))
	return "web:" + hex.EncodeToString(sum[:8])
}
