package tika

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

func TestClientExtractsBoundedNormalizedText(t *testing.T) {
	source := []byte("source document")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/version":
			_, _ = writer.Write([]byte("Apache Tika 3.3.0"))
		case "/tika":
			if request.Method != http.MethodPut || request.ContentLength != int64(len(source)) || request.Header.Get("Content-Type") != "application/pdf" || request.Header.Get("Accept") != "text/plain" || request.Header.Get("X-Tika-Skip-Embedded") != "true" || request.Header.Get("maxEmbeddedResources") != "0" || request.Header.Get("writeLimit") != "8388608" {
				t.Errorf("request method=%s length=%d headers=%v", request.Method, request.ContentLength, request.Header)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || !bytes.Equal(body, source) {
				t.Errorf("body=%q err=%v", body, err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = writer.Write([]byte("  First line\r\nSecond line \r\n"))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	result, err := client.Extract(context.Background(), knowledgeapp.TextExtractionRequest{Body: bytes.NewReader(source), Size: int64(len(source)), MediaType: "application/pdf; version=1.7"})
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte("First line\nSecond line")
	if !bytes.Equal(result.Text, expected) || result.TextSHA256 != sha256.Sum256(expected) || result.Extractor != "Apache Tika 3.3.0" {
		t.Fatalf("result=%+v", result)
	}
}

func TestClientFailsClosedOnServiceAndOutputErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		wantErr error
	}{
		{name: "unsupported version", handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("unexpected")) }), wantErr: ErrUnavailable},
		{name: "extraction status", handler: versionAndExtractHandler(func(writer http.ResponseWriter) { writer.WriteHeader(http.StatusUnprocessableEntity) }), wantErr: ErrExtraction},
		{name: "empty output", handler: versionAndExtractHandler(func(writer http.ResponseWriter) { _, _ = writer.Write([]byte(" \r\n")) }), wantErr: ErrOutput},
		{name: "invalid utf8", handler: versionAndExtractHandler(func(writer http.ResponseWriter) { _, _ = writer.Write([]byte{0xff, 0xfe}) }), wantErr: ErrOutput},
		{name: "oversize output", handler: versionAndExtractHandler(func(writer http.ResponseWriter) {
			_, _ = io.CopyN(writer, bytes.NewReader(make([]byte, knowledgedomain.MaximumExtractedTextBytes+1)), knowledgedomain.MaximumExtractedTextBytes+1)
		}), wantErr: ErrOutput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client := newTestClient(t, server.URL)
			_, err := client.Extract(context.Background(), knowledgeapp.TextExtractionRequest{Body: bytes.NewReader([]byte("x")), Size: 1, MediaType: "text/plain"})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v want=%v", err, test.wantErr)
			}
		})
	}
}

func TestClientRejectsInvalidConfigurationAndInput(t *testing.T) {
	for _, endpoint := range []string{"", "https://tika:9998", "http://user@tika:9998", "http://tika:9998/path", "http://tika:9998?query=1"} {
		if _, err := New(Config{Endpoint: endpoint}); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("endpoint=%q err=%v", endpoint, err)
		}
	}
	client := newTestClient(t, "http://127.0.0.1:1")
	if _, err := client.Extract(context.Background(), knowledgeapp.TextExtractionRequest{Body: bytes.NewReader([]byte("x")), Size: 1, MediaType: "application/zip"}); !errors.Is(err, knowledgeapp.ErrInvalid) {
		t.Fatalf("unsupported input err=%v", err)
	}
}

func versionAndExtractHandler(extract func(http.ResponseWriter)) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/version" {
			_, _ = writer.Write([]byte("Apache Tika 3.3.0"))
			return
		}
		extract(writer)
	})
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := New(Config{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
