package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
	"github.com/google/uuid"
)

const (
	SecurityTLS      = "tls"
	SecurityStartTLS = "starttls"
)

type InboxMessage struct {
	UID       uint32    `json:"uid"`
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
	Unread    bool      `json:"unread"`
	MessageID string    `json:"message_id"`
}

type Message struct {
	InboxMessage
	Body string `json:"body"`
}

type OutgoingMessage struct {
	To      []string `json:"to"`
	CC      []string `json:"cc"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
}

type SendResult struct {
	MessageID string    `json:"message_id"`
	SentAt    time.Time `json:"sent_at"`
}

type Connector interface {
	Test(context.Context, Integration) error
	Inbox(context.Context, Integration, int) ([]InboxMessage, error)
	Message(context.Context, Integration, uint32) (Message, error)
	Send(context.Context, Integration, OutgoingMessage) (SendResult, error)
}

type NetworkConnector struct {
	timeout time.Duration
}

func NewNetworkConnector(timeout time.Duration) *NetworkConnector {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &NetworkConnector{timeout: timeout}
}

func (c *NetworkConnector) Test(ctx context.Context, integration Integration) error {
	imapConnection, err := c.connectIMAP(ctx, integration)
	if err != nil {
		return fmt.Errorf("IMAP connection failed: %w", err)
	}
	if _, err := imapConnection.Select("INBOX", true); err != nil {
		_ = imapConnection.Logout()
		return fmt.Errorf("IMAP inbox could not be selected: %w", err)
	}
	_ = imapConnection.Logout()

	smtpConnection, _, err := c.connectSMTP(ctx, integration)
	if err != nil {
		return fmt.Errorf("SMTP connection failed: %w", err)
	}
	defer smtpConnection.Close()
	if err := smtpConnection.Noop(); err != nil {
		return fmt.Errorf("SMTP server did not accept the session: %w", err)
	}
	return smtpConnection.Quit()
}

func (c *NetworkConnector) Inbox(ctx context.Context, integration Integration, limit int) ([]InboxMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	connection, err := c.connectIMAP(ctx, integration)
	if err != nil {
		return nil, err
	}
	defer connection.Logout()
	mailbox, err := connection.Select("INBOX", true)
	if err != nil {
		return nil, fmt.Errorf("select inbox: %w", err)
	}
	if mailbox.Messages == 0 {
		return []InboxMessage{}, nil
	}
	from := uint32(1)
	if mailbox.Messages > uint32(limit) {
		from = mailbox.Messages - uint32(limit) + 1
	}
	set := new(imap.SeqSet)
	set.AddRange(from, mailbox.Messages)
	messages := make(chan *imap.Message, limit)
	done := make(chan error, 1)
	go func() {
		done <- connection.Fetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags, imap.FetchInternalDate}, messages)
	}()
	result := make([]InboxMessage, 0, limit)
	for item := range messages {
		result = append(result, inboxMessage(item))
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch inbox: %w", err)
	}
	slices.Reverse(result)
	return result, nil
}

func (c *NetworkConnector) Message(ctx context.Context, integration Integration, uid uint32) (Message, error) {
	if uid == 0 {
		return Message{}, errors.New("email UID is required")
	}
	connection, err := c.connectIMAP(ctx, integration)
	if err != nil {
		return Message{}, err
	}
	defer connection.Logout()
	if _, err := connection.Select("INBOX", true); err != nil {
		return Message{}, fmt.Errorf("select inbox: %w", err)
	}
	set := new(imap.SeqSet)
	set.AddNum(uid)
	section := &imap.BodySectionName{Peek: true}
	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- connection.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags, imap.FetchInternalDate, section.FetchItem()}, messages)
	}()
	item, ok := <-messages
	if err := <-done; err != nil {
		return Message{}, fmt.Errorf("fetch email: %w", err)
	}
	if !ok || item == nil {
		return Message{}, ErrMessageNotFound
	}
	literal := item.GetBody(section)
	if literal == nil {
		return Message{}, errors.New("email body was not returned by the IMAP server")
	}
	parsed, err := mail.ReadMessage(literal)
	if err != nil {
		return Message{}, fmt.Errorf("parse email: %w", err)
	}
	body, err := extractReadableBody(parsed.Header, parsed.Body, 0)
	if err != nil {
		return Message{}, fmt.Errorf("read email body: %w", err)
	}
	return Message{InboxMessage: inboxMessage(item), Body: body}, nil
}

type DeliveryError struct {
	Err     error
	Unknown bool
}

func (e *DeliveryError) Error() string { return e.Err.Error() }
func (e *DeliveryError) Unwrap() error { return e.Err }

func (c *NetworkConnector) Send(ctx context.Context, integration Integration, message OutgoingMessage) (SendResult, error) {
	from, err := mail.ParseAddress(integration.EmailAddress)
	if err != nil {
		return SendResult{}, fmt.Errorf("invalid sender address: %w", err)
	}
	recipients, err := parseRecipients(append(append([]string{}, message.To...), message.CC...))
	if err != nil {
		return SendResult{}, err
	}
	if len(recipients) == 0 {
		return SendResult{}, errors.New("at least one recipient is required")
	}
	if len(message.Subject) > 500 || hasHeaderBreak(message.Subject) {
		return SendResult{}, errors.New("email subject is invalid")
	}
	if strings.TrimSpace(message.Body) == "" || len(message.Body) > 1<<20 {
		return SendResult{}, errors.New("email body must contain between 1 byte and 1 MB")
	}
	messageID := "<" + uuid.NewString() + "@" + senderDomain(from.Address) + ">"
	raw := buildRFC5322(integration, message, messageID)
	connection, _, err := c.connectSMTP(ctx, integration)
	if err != nil {
		return SendResult{}, err
	}
	defer connection.Close()
	if err := connection.Mail(from.Address); err != nil {
		return SendResult{}, fmt.Errorf("set SMTP sender: %w", err)
	}
	for _, recipient := range recipients {
		if err := connection.Rcpt(recipient); err != nil {
			return SendResult{}, fmt.Errorf("set SMTP recipient: %w", err)
		}
	}
	writer, err := connection.Data()
	if err != nil {
		return SendResult{}, fmt.Errorf("start SMTP message: %w", err)
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return SendResult{}, &DeliveryError{Err: fmt.Errorf("write SMTP message: %w", err), Unknown: true}
	}
	if err := writer.Close(); err != nil {
		return SendResult{}, &DeliveryError{Err: fmt.Errorf("finish SMTP message: %w", err), Unknown: true}
	}
	_ = connection.Quit()
	return SendResult{MessageID: messageID, SentAt: time.Now().UTC()}, nil
}

func (c *NetworkConnector) connectIMAP(ctx context.Context, integration Integration) (*imapclient.Client, error) {
	address := net.JoinHostPort(integration.IMAPHost, fmt.Sprintf("%d", integration.IMAPPort))
	dialer := &net.Dialer{Timeout: c.timeout}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: integration.IMAPHost}
	var connection *imapclient.Client
	var err error
	if integration.IMAPSecurity == SecurityTLS {
		connection, err = imapclient.DialWithDialerTLS(dialer, address, tlsConfig)
	} else {
		connection, err = imapclient.DialWithDialer(dialer, address)
		if err == nil {
			err = connection.StartTLS(tlsConfig)
			if err != nil {
				_ = connection.Terminate()
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err := connection.Login(integration.IMAPUsername, integration.IMAPPassword); err != nil {
		_ = connection.Logout()
		return nil, fmt.Errorf("IMAP login: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = connection.Logout()
		return nil, err
	}
	return connection, nil
}

func (c *NetworkConnector) connectSMTP(ctx context.Context, integration Integration) (*smtp.Client, net.Conn, error) {
	address := net.JoinHostPort(integration.SMTPHost, fmt.Sprintf("%d", integration.SMTPPort))
	dialer := &net.Dialer{Timeout: c.timeout}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: integration.SMTPHost}
	var connection net.Conn
	var err error
	if integration.SMTPSecurity == SecurityTLS {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	client, err := smtp.NewClient(connection, integration.SMTPHost)
	if err != nil {
		connection.Close()
		return nil, nil, err
	}
	if integration.SMTPSecurity == SecurityStartTLS {
		if err := client.StartTLS(tlsConfig); err != nil {
			client.Close()
			return nil, nil, err
		}
	}
	if integration.SMTPUsername != "" {
		if err := client.Auth(smtp.PlainAuth("", integration.SMTPUsername, integration.SMTPPassword, integration.SMTPHost)); err != nil {
			client.Close()
			return nil, nil, fmt.Errorf("SMTP login: %w", err)
		}
	}
	return client, connection, nil
}

func inboxMessage(message *imap.Message) InboxMessage {
	result := InboxMessage{UID: message.Uid, Date: message.InternalDate, Unread: !slices.Contains(message.Flags, imap.SeenFlag)}
	if message.Envelope == nil {
		return result
	}
	result.Subject = message.Envelope.Subject
	result.MessageID = message.Envelope.MessageId
	if !message.Envelope.Date.IsZero() {
		result.Date = message.Envelope.Date
	}
	result.From = formatIMAPAddresses(message.Envelope.From)
	result.To = splitIMAPAddresses(message.Envelope.To)
	return result
}

func formatIMAPAddresses(addresses []*imap.Address) string {
	values := splitIMAPAddresses(addresses)
	return strings.Join(values, ", ")
}

func splitIMAPAddresses(addresses []*imap.Address) []string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address == nil {
			continue
		}
		value := &mail.Address{Name: address.PersonalName, Address: address.MailboxName + "@" + address.HostName}
		result = append(result, value.String())
	}
	return result
}

func parseRecipients(values []string) ([]string, error) {
	if len(values) > 50 {
		return nil, errors.New("email cannot have more than 50 recipients")
	}
	var result []string
	for _, value := range values {
		address, err := mail.ParseAddress(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid recipient %q", value)
		}
		result = append(result, address.Address)
	}
	return result, nil
}

func buildRFC5322(integration Integration, message OutgoingMessage, messageID string) []byte {
	from := (&mail.Address{Name: integration.DisplayName, Address: integration.EmailAddress}).String()
	to := canonicalAddresses(message.To)
	cc := canonicalAddresses(message.CC)
	headers := []string{
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: " + messageID,
		"From: " + from,
		"To: " + strings.Join(to, ", "),
	}
	if len(cc) > 0 {
		headers = append(headers, "Cc: "+strings.Join(cc, ", "))
	}
	headers = append(headers,
		"Subject: "+mime.QEncoding.Encode("UTF-8", message.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
	)
	var builder strings.Builder
	builder.WriteString(strings.Join(headers, "\r\n"))
	builder.WriteString("\r\n\r\n")
	encoded := quotedprintable.NewWriter(&builder)
	_, _ = encoded.Write([]byte(message.Body))
	_ = encoded.Close()
	return []byte(builder.String())
}

func canonicalAddresses(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if address, err := mail.ParseAddress(strings.TrimSpace(value)); err == nil {
			result = append(result, address.String())
		}
	}
	return result
}

func senderDomain(address string) string {
	_, domain, found := strings.Cut(address, "@")
	if !found || domain == "" {
		return "mainspring.local"
	}
	return domain
}

func hasHeaderBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func extractReadableBody(header mail.Header, body io.Reader, depth int) (string, error) {
	if depth > 8 {
		return "", errors.New("email MIME nesting is too deep")
	}
	mediaType, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	decodedBody := decodeTransferEncoding(body, header.Get("Content-Transfer-Encoding"))
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := parameters["boundary"]
		if boundary == "" {
			return "", errors.New("multipart email is missing a boundary")
		}
		reader := multipart.NewReader(decodedBody, boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(strings.ToLower(part.Header.Get("Content-Disposition")), "attachment") {
				part.Close()
				continue
			}
			partHeader := mail.Header(part.Header)
			text, err := extractReadableBody(partHeader, part, depth+1)
			part.Close()
			if err == nil && strings.TrimSpace(text) != "" {
				return text, nil
			}
		}
		return "", nil
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return "", nil
	}
	content, err := io.ReadAll(io.LimitReader(decodedBody, 256<<10))
	if err != nil {
		return "", err
	}
	text := string(content)
	if mediaType == "text/html" {
		text = html.UnescapeString(htmlTagPattern.ReplaceAllString(text, " "))
		text = strings.Join(strings.Fields(text), " ")
	}
	return strings.TrimSpace(text), nil
}

func decodeTransferEncoding(reader io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, bufio.NewReader(reader))
	case "quoted-printable":
		return quotedprintable.NewReader(reader)
	default:
		return reader
	}
}
