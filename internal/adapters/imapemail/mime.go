package imapemail

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"golang.org/x/net/html/charset"
)

const maximumMIMEDepth = 5

var unsafeAttachmentExtension = regexp.MustCompile(`(?i)\.(ade|adp|app|bat|chm|cmd|com|cpl|dll|dmg|exe|hta|ins|iso|jar|js|jse|lnk|mde|msc|msi|msp|mst|pif|ps1|reg|scr|sct|shb|sys|vb|vbe|vbs|vxd|wsc|wsf|wsh)$`)

type capturedPart struct {
	Title, Filename, MediaType string
	Content                    []byte
}

type parsedMIME struct {
	plain, html []byte
	attachments []capturedPart
	rejections  []string
	partCount   int
}

func parseMessage(raw []byte, fallback messageEnvelope, maximumPartBytes int64) ([]capturedPart, error) {
	message, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, ErrMessage
	}
	envelope := envelopeFromHeader(message.Header, fallback)
	parsed := &parsedMIME{}
	if err := parsed.walk(message.Header, message.Body, 0, maximumPartBytes, false); err != nil {
		return nil, err
	}
	parts := make([]capturedPart, 0, 1+len(parsed.attachments))
	manifest := buildManifest(envelope, parsed.plain, parsed.rejections)
	parts = append(parts, capturedPart{Title: boundedTitle(envelope.Subject), Filename: "message.txt", MediaType: "text/plain", Content: manifest})
	if len(parsed.plain) == 0 && len(parsed.html) > 0 && len(parts) < maximumPartsPerMessage {
		parts = append(parts, capturedPart{Title: boundedTitle(envelope.Subject + " — HTML body"), Filename: "message.html", MediaType: "text/html", Content: parsed.html})
	}
	for _, attachment := range parsed.attachments {
		if len(parts) >= maximumPartsPerMessage {
			break
		}
		parts = append(parts, attachment)
	}
	return parts, nil
}

func (parsed *parsedMIME) walk(header mail.Header, body io.Reader, depth int, maximumPartBytes int64, inheritedAttachment bool) error {
	if depth > maximumMIMEDepth || parsed.partCount >= maximumPartsPerMessage*2 {
		return ErrLimit
	}
	mediaType, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		if header.Get("Content-Type") == "" {
			mediaType = "text/plain"
		} else {
			return ErrMessage
		}
	}
	mediaType = strings.ToLower(mediaType)
	disposition, dispositionParameters, dispositionErr := mime.ParseMediaType(header.Get("Content-Disposition"))
	if dispositionErr != nil && header.Get("Content-Disposition") != "" {
		return ErrMessage
	}
	filename := dispositionParameters["filename"]
	if filename == "" {
		filename = parameters["name"]
	}
	isAttachment := inheritedAttachment || strings.EqualFold(disposition, "attachment") || filename != ""
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := parameters["boundary"]
		if boundary == "" || len(boundary) > 200 {
			return ErrMessage
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return ErrMessage
			}
			partHeader := make(mail.Header, len(part.Header))
			for key, values := range part.Header {
				partHeader[key] = append([]string(nil), values...)
			}
			if err := parsed.walk(partHeader, part, depth+1, maximumPartBytes, isAttachment); err != nil {
				_ = part.Close()
				return err
			}
			_ = part.Close()
		}
		return nil
	}
	parsed.partCount++
	decoded, err := decodeTransfer(body, header.Get("Content-Transfer-Encoding"), maximumPartBytes)
	if err != nil {
		parsed.rejections = append(parsed.rejections, "a MIME part exceeded its byte limit or used an invalid transfer encoding")
		return nil
	}
	defer wipe(decoded)
	if isAttachment {
		cleanFilename := safeFilename(filename)
		if cleanFilename == "" {
			cleanFilename = fmt.Sprintf("attachment-%d", parsed.partCount)
		}
		if !supportedAttachment(mediaType, cleanFilename) {
			parsed.rejections = append(parsed.rejections, cleanFilename+" was not admitted because its type is unsupported")
			return nil
		}
		parsed.attachments = append(parsed.attachments, capturedPart{Title: boundedTitle(cleanFilename), Filename: cleanFilename,
			MediaType: mediaType, Content: append([]byte(nil), decoded...)})
		return nil
	}
	switch mediaType {
	case "text/plain", "text/html":
		text, convertErr := decodeCharset(decoded, parameters["charset"], maximumPartBytes)
		if convertErr != nil {
			parsed.rejections = append(parsed.rejections, "a message body used an unsupported or invalid character set")
			return nil
		}
		if mediaType == "text/plain" && len(parsed.plain) == 0 {
			parsed.plain = text
		} else if mediaType == "text/html" && len(parsed.html) == 0 {
			parsed.html = text
		}
	default:
		parsed.rejections = append(parsed.rejections, "an inline MIME part was not admitted because its type is unsupported")
	}
	return nil
}

func decodeTransfer(body io.Reader, encoding string, maximum int64) ([]byte, error) {
	var reader io.Reader = body
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		reader = quotedprintable.NewReader(body)
	default:
		return nil, ErrMessage
	}
	value, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(value)) > maximum {
		wipe(value)
		return nil, ErrLimit
	}
	return value, nil
}

func decodeCharset(value []byte, label string, maximum int64) ([]byte, error) {
	if label == "" || strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "us-ascii") {
		if !utf8.Valid(value) {
			return nil, ErrMessage
		}
		return append([]byte(nil), value...), nil
	}
	reader, err := charset.NewReaderLabel(label, bytes.NewReader(value))
	if err != nil {
		return nil, err
	}
	converted, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(converted)) > maximum || !utf8.Valid(converted) {
		wipe(converted)
		return nil, ErrMessage
	}
	return converted, nil
}

func supportedAttachment(mediaType, filename string) bool {
	if unsafeAttachmentExtension.MatchString(filename) {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") || strings.HasPrefix(mediaType, "image/") {
		return mediaType != "text/javascript"
	}
	switch mediaType {
	case "application/pdf", "application/json", "application/xml", "application/rtf",
		"application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.spreadsheet",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	default:
		return false
	}
}

func envelopeFromHeader(header mail.Header, fallback messageEnvelope) messageEnvelope {
	value := messageEnvelope{Subject: decodeHeader(header.Get("Subject")), From: header.Get("From"), To: header.Get("To"),
		Date: header.Get("Date"), MessageID: header.Get("Message-ID"), InReplyTo: header.Get("In-Reply-To"), References: header.Get("References")}
	if value.Subject == "" {
		value.Subject = fallback.Subject
	}
	for target, source := range map[*string]string{&value.From: fallback.From, &value.To: fallback.To, &value.Date: fallback.Date,
		&value.MessageID: fallback.MessageID, &value.InReplyTo: fallback.InReplyTo, &value.References: fallback.References} {
		if *target == "" {
			*target = source
		}
	}
	return value
}

func buildManifest(envelope messageEnvelope, plain []byte, rejections []string) []byte {
	var output strings.Builder
	for _, field := range []struct{ name, value string }{{"Subject", envelope.Subject}, {"From", envelope.From}, {"To", envelope.To},
		{"Date", envelope.Date}, {"Message-ID", envelope.MessageID}, {"In-Reply-To", envelope.InReplyTo}, {"References", envelope.References}} {
		if clean := cleanHeaderValue(field.value); clean != "" {
			output.WriteString(field.name + ": " + clean + "\n")
		}
	}
	output.WriteString("\n")
	if len(plain) > 0 {
		output.Write(plain)
		if plain[len(plain)-1] != '\n' {
			output.WriteByte('\n')
		}
	}
	if len(rejections) > 0 {
		output.WriteString("\nAttachment and MIME admission notes:\n")
		for _, reason := range rejections {
			output.WriteString("- " + cleanHeaderValue(reason) + "\n")
		}
	}
	if len(plain) == 0 && len(rejections) == 0 {
		output.WriteString("No supported plain-text body was present.\n")
	}
	return []byte(output.String())
}

func rejectionManifest(envelope messageEnvelope, reason string) capturedPart {
	return capturedPart{Title: boundedTitle(envelope.Subject), Filename: "message.txt", MediaType: "text/plain",
		Content: buildManifest(envelope, nil, []string{reason})}
}

func safeFilename(value string) string {
	value = decodeHeader(value)
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\\", "_"), "/", "_"))
	value = filepath.Base(value)
	if value == "." || value == ".." || value == "" || !utf8.ValidString(value) {
		return ""
	}
	return truncateUTF8(value, integrationsync.MaximumFilenameBytes)
}

func boundedTitle(value string) string {
	value = cleanHeaderValue(value)
	if value == "" {
		value = "Email message"
	}
	return truncateUTF8(value, integrationsync.MaximumTitleBytes)
}

func cleanHeaderValue(value string) string {
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == '\x00' {
			return ' '
		}
		return character
	}, value))
}

func decodeHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return cleanHeaderValue(value)
	}
	return cleanHeaderValue(decoded)
}

func truncateUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
