package documentextract

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestExtractText(t *testing.T) {
	text, mediaType, err := Extract("notes.md", []byte("# Closeout\nVerify photos."))
	if err != nil || mediaType != "text/markdown" || text != "# Closeout\nVerify photos." {
		t.Fatalf("Extract() = %q, %q, %v", text, mediaType, err)
	}
}

func TestExtractDOCX(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>License</w:t></w:r><w:r><w:t>renewal</w:t></w:r></w:p><w:p><w:r><w:t>Due annually</w:t></w:r></w:p></w:body></w:document>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	text, mediaType, err := Extract("license.docx", archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || text != "License renewal\nDue annually" {
		t.Fatalf("Extract() = %q, %q", text, mediaType)
	}
}

func TestExtractRejectsUnsupportedAndBinaryText(t *testing.T) {
	if _, _, err := Extract("image.png", []byte("png")); err == nil {
		t.Fatal("expected unsupported format error")
	}
	if _, _, err := Extract("notes.txt", []byte{'a', 0, 'b'}); err == nil {
		t.Fatal("expected binary text error")
	}
}
