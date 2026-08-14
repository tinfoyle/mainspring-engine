package documentextract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const (
	MaxUploadBytes = 15 << 20
	MaxTextBytes   = 2 << 20
)

func Extract(filename string, content []byte) (string, string, error) {
	if len(content) == 0 || len(content) > MaxUploadBytes {
		return "", "", fmt.Errorf("document must contain data and be no larger than %d MB", MaxUploadBytes>>20)
	}
	extension := strings.ToLower(filepath.Ext(filename))
	switch extension {
	case ".pdf":
		text, err := extractPDF(content)
		return validateText(text, "application/pdf", err)
	case ".docx":
		text, err := extractDOCX(content)
		return validateText(text, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", err)
	default:
		mediaType, ok := textMediaType(extension)
		if !ok {
			return "", "", errors.New("supported formats are PDF, DOCX, TXT, Markdown, CSV, TSV, JSON, XML, HTML, YAML, and LOG")
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return "", "", errors.New("the text document is not valid UTF-8")
		}
		return validateText(string(content), mediaType, nil)
	}
}

func textMediaType(extension string) (string, bool) {
	switch extension {
	case ".txt", ".log":
		return "text/plain", true
	case ".md", ".markdown":
		return "text/markdown", true
	case ".csv":
		return "text/csv", true
	case ".tsv":
		return "text/tab-separated-values", true
	case ".json":
		return "application/json", true
	case ".xml":
		return "application/xml", true
	case ".html", ".htm":
		return "text/html", true
	case ".yaml", ".yml":
		return "text/yaml", true
	default:
		return "", false
	}
}

func validateText(text, mediaType string, extractionErr error) (string, string, error) {
	if extractionErr != nil {
		return "", "", extractionErr
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
	if text == "" {
		return "", "", errors.New("the document does not contain extractable text")
	}
	if len(text) > MaxTextBytes {
		return "", "", fmt.Errorf("extracted document text exceeds %d MB", MaxTextBytes>>20)
	}
	return text, mediaType, nil
}

func extractPDF(content []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}
	text, err := io.ReadAll(io.LimitReader(plain, MaxTextBytes+1))
	if err != nil {
		return "", fmt.Errorf("read PDF text: %w", err)
	}
	if len(text) > MaxTextBytes {
		return "", fmt.Errorf("extracted PDF text exceeds %d MB", MaxTextBytes>>20)
	}
	return string(text), nil
}

func extractDOCX(content []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", fmt.Errorf("open DOCX: %w", err)
	}
	if len(archive.File) > 10000 {
		return "", errors.New("DOCX contains too many archive entries")
	}
	var document *zip.File
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", errors.New("DOCX is missing word/document.xml")
	}
	if document.UncompressedSize64 > 16<<20 {
		return "", errors.New("DOCX document XML is too large")
	}
	stream, err := document.Open()
	if err != nil {
		return "", fmt.Errorf("open DOCX document XML: %w", err)
	}
	defer stream.Close()
	decoder := xml.NewDecoder(io.LimitReader(stream, 16<<20))
	var builder strings.Builder
	var inText bool
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse DOCX document XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "t" {
				inText = true
			}
		case xml.CharData:
			if inText {
				builder.Write(value)
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				inText = false
				builder.WriteByte(' ')
			case "p", "tr":
				builder.WriteByte('\n')
			}
		}
		if builder.Len() > MaxTextBytes {
			return "", fmt.Errorf("extracted DOCX text exceeds %d MB", MaxTextBytes>>20)
		}
	}
	return normalizeWhitespace(builder.String()), nil
}

func normalizeWhitespace(value string) string {
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
