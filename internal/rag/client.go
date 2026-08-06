package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type Client struct {
	baseURL  string
	token    string
	tenantID domain.TenantID
	http     *http.Client
}

func NewClient(baseURL, token string, tenantID domain.TenantID) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("MAINSPRING_RAG_INTERNAL_URL must be an HTTP or HTTPS URL")
	}
	if len(token) < 32 {
		return nil, errors.New("MAINSPRING_RAG_TOKEN must contain at least 32 bytes")
	}
	return &Client{
		baseURL: baseURL, token: token, tenantID: tenantID,
		http: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (c *Client) ListDocuments(ctx context.Context) ([]Document, error) {
	var response struct {
		Documents []Document `json:"documents"`
	}
	if err := c.request(ctx, http.MethodGet, "/documents", nil, &response); err != nil {
		return nil, err
	}
	return response.Documents, nil
}

func (c *Client) GetDocument(ctx context.Context, documentID string) (DocumentDetail, error) {
	var document DocumentDetail
	if err := c.request(ctx, http.MethodGet, "/documents/"+url.PathEscape(documentID), nil, &document); err != nil {
		return DocumentDetail{}, err
	}
	return document, nil
}

func (c *Client) IngestText(ctx context.Context, name, mediaType, content, createdBy string) (Document, error) {
	input := struct {
		Name      string `json:"name"`
		MediaType string `json:"media_type"`
		Content   string `json:"content"`
		CreatedBy string `json:"created_by"`
	}{Name: name, MediaType: mediaType, Content: content, CreatedBy: createdBy}
	var document Document
	if err := c.request(ctx, http.MethodPost, "/documents/text", input, &document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode RAG request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create RAG request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("X-Mainspring-Tenant-ID", c.tenantID.String())
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call RAG service: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrDocumentNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("RAG service returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode RAG response: %w", err)
	}
	return nil
}
