package knowledge

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const maximumDocumentArchiveEntries = 4096
const maximumDocumentArchiveExpandedBytes = uint64(256 << 20)

type DocumentSource interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

type VerifiedDocumentSource struct {
	MediaType     string
	Size          int64
	ContentSHA256 [sha256.Size]byte
}

func VerifyDocumentSource(filename, declaredType string, source DocumentSource) (VerifiedDocumentSource, error) {
	mediaType, ok := knowledgedomain.ExpectedDocumentMediaType(filename)
	declaredType = normalizeDocumentMediaType(declaredType)
	if !ok || source == nil || (declaredType != "" && declaredType != "application/octet-stream" && declaredType != mediaType) {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	size, err := source.Seek(0, io.SeekEnd)
	if err != nil || size <= 0 || size > knowledgedomain.MaximumDocumentBytes {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(source, knowledgedomain.MaximumDocumentBytes+1))
	if err != nil || written != size {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	if err := verifyDocumentMedia(mediaType, source, size); err != nil {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return VerifiedDocumentSource{}, ErrInvalid
	}
	return VerifiedDocumentSource{MediaType: mediaType, Size: size, ContentSHA256: digest}, nil
}

func verifyDocumentMedia(mediaType string, source DocumentSource, size int64) error {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch mediaType {
	case "application/pdf":
		var signature [5]byte
		if _, err := io.ReadFull(source, signature[:]); err != nil || string(signature[:]) != "%PDF-" {
			return ErrInvalid
		}
		return nil
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return verifyDOCX(source, size)
	case "application/json":
		return verifyJSON(source)
	case "application/xml":
		return verifyXML(source)
	case "text/html":
		if err := verifyUTF8Text(source); err != nil {
			return err
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return err
		}
		prefix, err := io.ReadAll(io.LimitReader(source, 4096))
		if err != nil {
			return err
		}
		trimmed := strings.ToLower(strings.TrimSpace(string(prefix)))
		if !strings.HasPrefix(trimmed, "<!doctype html") && !strings.HasPrefix(trimmed, "<html") && !strings.HasPrefix(trimmed, "<head") && !strings.HasPrefix(trimmed, "<body") {
			return ErrInvalid
		}
		return nil
	case "text/csv":
		return verifyDelimited(source, ',')
	case "text/tab-separated-values":
		return verifyDelimited(source, '\t')
	default:
		return verifyUTF8Text(source)
	}
}

func verifyDOCX(source DocumentSource, size int64) error {
	reader, err := zip.NewReader(source, size)
	if err != nil || len(reader.File) == 0 || len(reader.File) > maximumDocumentArchiveEntries {
		return ErrInvalid
	}
	var expanded uint64
	contentTypes, document := false, false
	for _, entry := range reader.File {
		expanded += entry.UncompressedSize64
		if expanded > maximumDocumentArchiveExpandedBytes || strings.Contains(entry.Name, "\\") || strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "../") {
			return ErrInvalid
		}
		switch entry.Name {
		case "[Content_Types].xml":
			contentTypes = true
		case "word/document.xml":
			document = true
		}
	}
	if !contentTypes || !document {
		return ErrInvalid
	}
	return nil
}

func verifyJSON(source io.Reader) error {
	decoder := json.NewDecoder(source)
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func verifyXML(source io.Reader) error {
	decoder := xml.NewDecoder(source)
	foundElement := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !foundElement {
				return ErrInvalid
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			foundElement = true
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(value)), "DOCTYPE") {
				return ErrInvalid
			}
		}
	}
}

func verifyDelimited(source io.Reader, comma rune) error {
	reader := csv.NewReader(source)
	reader.Comma = comma
	reader.FieldsPerRecord = -1
	rows := 0
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			if rows == 0 {
				return ErrInvalid
			}
			return nil
		}
		if err != nil {
			return err
		}
		rows++
	}
}

func verifyUTF8Text(source io.Reader) error {
	reader := bufio.NewReader(source)
	count := 0
	for {
		value, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			if count == 0 {
				return ErrInvalid
			}
			return nil
		}
		if err != nil || value == utf8.RuneError && size == 1 || value == 0 {
			return ErrInvalid
		}
		count += size
	}
}

func normalizeDocumentMediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}
