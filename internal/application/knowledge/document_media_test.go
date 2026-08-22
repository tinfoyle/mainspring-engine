package knowledge

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestVerifyDocumentSourceDerivesTrustedIdentity(t *testing.T) {
	body := []byte("# Operating plan\n\nVerified source.\n")
	verified, err := VerifyDocumentSource("operating-plan.md", "text/markdown; charset=utf-8", bytes.NewReader(body))
	if err != nil || verified.MediaType != "text/markdown" || verified.Size != int64(len(body)) || verified.ContentSHA256 != sha256.Sum256(body) {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
}

func TestVerifyDocumentSourceRejectsDeclarationAndSignatureMismatch(t *testing.T) {
	for name, fixture := range map[string]struct {
		declared string
		body     []byte
	}{
		"plan.pdf":  {declared: "application/pdf", body: []byte("not a pdf")},
		"plan.json": {declared: "text/plain", body: []byte(`{"valid":true}`)},
		"plan.html": {declared: "text/html", body: []byte("plain text")},
		"plan.txt":  {declared: "text/plain", body: []byte{'v', 0, 'x'}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyDocumentSource(name, fixture.declared, bytes.NewReader(fixture.body)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestVerifyDocumentSourceRecognizesDOCXContainer(t *testing.T) {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for _, name := range []string{"[Content_Types].xml", "word/document.xml"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("<root/>")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyDocumentSource("plan.docx", "application/octet-stream", bytes.NewReader(body.Bytes()))
	if err != nil || verified.MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
}
