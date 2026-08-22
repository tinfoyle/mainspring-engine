package tika

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const (
	defaultTimeout      = 2 * time.Minute
	maximumVersionBytes = 512
)

var (
	ErrConfiguration = errors.New("Tika configuration is invalid")
	ErrUnavailable   = errors.New("Tika is unavailable")
	ErrExtraction    = errors.New("Tika could not extract the document")
	ErrOutput        = errors.New("Tika extraction output is invalid")
)

type Config struct {
	Endpoint  string
	Timeout   time.Duration
	Transport http.RoundTripper
}

type Client struct {
	endpoint string
	http     *http.Client

	extractorMu sync.RWMutex
	extractor   string
}

func New(config Config) (*Client, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || config.Timeout < 0 {
		return nil, ErrConfiguration
	}
	parsed.Path = ""
	client := &http.Client{Timeout: config.Timeout, Transport: config.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{endpoint: strings.TrimSuffix(parsed.String(), "/"), http: client}, nil
}

func (client *Client) Verify(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/version", nil)
	if err != nil {
		return ErrConfiguration
	}
	request.Header.Set("Accept", "text/plain")
	response, err := client.http.Do(request)
	if err != nil {
		return unavailable("read version", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: version status %d", ErrUnavailable, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumVersionBytes+1))
	if err != nil {
		return unavailable("read version response", err)
	}
	extractor := strings.TrimSpace(string(body))
	if len(body) > maximumVersionBytes || !utf8.Valid(body) || !strings.HasPrefix(extractor, "Apache Tika ") || len(extractor) > knowledgedomain.MaximumProcessorIdentity || strings.ContainsAny(extractor, "\r\n\x00") {
		return ErrUnavailable
	}
	client.extractorMu.Lock()
	client.extractor = extractor
	client.extractorMu.Unlock()
	return nil
}

func (client *Client) Extract(ctx context.Context, request knowledgeapp.TextExtractionRequest) (knowledgeapp.TextExtractionResult, error) {
	mediaType := normalizeMediaType(request.MediaType)
	if request.Body == nil || request.Size <= 0 || request.Size > knowledgedomain.MaximumDocumentBytes || !supportedMediaTypes[mediaType] {
		return knowledgeapp.TextExtractionResult{}, knowledgeapp.ErrInvalid
	}
	extractor, err := client.extractorIdentity(ctx)
	if err != nil {
		return knowledgeapp.TextExtractionResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, client.endpoint+"/tika", io.LimitReader(request.Body, request.Size))
	if err != nil {
		return knowledgeapp.TextExtractionResult{}, ErrConfiguration
	}
	httpRequest.ContentLength = request.Size
	httpRequest.Header.Set("Content-Type", mediaType)
	httpRequest.Header.Set("Accept", "text/plain")
	httpRequest.Header.Set("X-Tika-Skip-Embedded", "true")
	httpRequest.Header.Set("maxEmbeddedResources", "0")
	httpRequest.Header.Set("writeLimit", strconv.FormatInt(knowledgedomain.MaximumExtractedTextBytes, 10))
	response, err := client.http.Do(httpRequest)
	if err != nil {
		return knowledgeapp.TextExtractionResult{}, unavailable("extract", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return knowledgeapp.TextExtractionResult{}, fmt.Errorf("%w: status %d", ErrExtraction, response.StatusCode)
	}
	text, err := io.ReadAll(io.LimitReader(response.Body, knowledgedomain.MaximumExtractedTextBytes+1))
	if err != nil {
		return knowledgeapp.TextExtractionResult{}, unavailable("read extraction", err)
	}
	if int64(len(text)) > knowledgedomain.MaximumExtractedTextBytes {
		return knowledgeapp.TextExtractionResult{}, ErrOutput
	}
	text = normalizeText(text)
	if len(text) == 0 || int64(len(text)) > knowledgedomain.MaximumExtractedTextBytes || !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 {
		return knowledgeapp.TextExtractionResult{}, ErrOutput
	}
	return knowledgeapp.TextExtractionResult{Text: text, TextSHA256: sha256.Sum256(text), Extractor: extractor}, nil
}

func (client *Client) extractorIdentity(ctx context.Context) (string, error) {
	client.extractorMu.RLock()
	extractor := client.extractor
	client.extractorMu.RUnlock()
	if extractor != "" {
		return extractor, nil
	}
	if err := client.Verify(ctx); err != nil {
		return "", err
	}
	client.extractorMu.RLock()
	defer client.extractorMu.RUnlock()
	return client.extractor, nil
}

func normalizeText(value []byte) []byte {
	value = bytes.ReplaceAll(value, []byte("\r\n"), []byte("\n"))
	value = bytes.ReplaceAll(value, []byte("\r"), []byte("\n"))
	return bytes.TrimSpace(value)
}

func normalizeMediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

func unavailable(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
}

var supportedMediaTypes = map[string]bool{
	"text/plain": true, "text/markdown": true, "text/csv": true, "text/tab-separated-values": true,
	"application/json": true, "application/xml": true, "text/html": true, "application/yaml": true,
	"application/pdf": true, "application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
}

var _ knowledgeapp.TextExtractor = (*Client)(nil)
