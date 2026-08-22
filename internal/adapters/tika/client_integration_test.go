package tika

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
)

func TestTikaExtractsAllowedDocumentFormats(t *testing.T) {
	endpoint := os.Getenv("SPYGLASS_TIKA_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("SPYGLASS_TIKA_TEST_ENDPOINT is not configured")
	}
	client, err := New(Config{Endpoint: endpoint, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mediaType, expected string
		body                      []byte
	}{
		{name: "text", mediaType: "text/plain", body: []byte("Hello text\r\n"), expected: "Hello text"},
		{name: "docx", mediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", body: testDOCX(t, "Hello DOCX"), expected: "Hello DOCX"},
		{name: "pdf", mediaType: "application/pdf", body: testPDF("Hello PDF"), expected: "Hello PDF"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, extractErr := client.Extract(ctx, knowledgeapp.TextExtractionRequest{Body: bytes.NewReader(test.body), Size: int64(len(test.body)), MediaType: test.mediaType})
			if extractErr != nil {
				t.Fatal(extractErr)
			}
			if !strings.Contains(string(result.Text), test.expected) {
				t.Fatalf("text=%q does not contain %q", result.Text, test.expected)
			}
		})
	}
}

func testDOCX(t *testing.T, text string) []byte {
	t.Helper()
	var result bytes.Buffer
	archive := zip.NewWriter(&result)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func testPDF(text string) []byte {
	var result bytes.Buffer
	result.WriteString("%PDF-1.4\n")
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>`,
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 12 Tf 72 720 Td (%s) Tj ET\nendstream", len("BT /F1 12 Tf 72 720 Td ("+text+") Tj ET\n"), text),
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
	}
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = result.Len()
		fmt.Fprintf(&result, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := result.Len()
	fmt.Fprintf(&result, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for index := 1; index < len(offsets); index++ {
		fmt.Fprintf(&result, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&result, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return result.Bytes()
}
