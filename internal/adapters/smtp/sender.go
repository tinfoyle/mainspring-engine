// Package smtp delivers transactional Spyglass identity messages over an
// implicit-TLS SMTP connection. Tokens are placed only in message bodies and
// are never returned by production transports or written to logs here.
package smtp

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"mime"
	"net"
	"net/mail"
	stdsmtp "net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type Config struct {
	Address, ServerName, Username, Password string
	FromAddress, FromName, AppOrigin        string
	Timeout                                 time.Duration
}

type Sender struct {
	config Config
	from   mail.Address
	origin string
}

func New(config Config) (*Sender, error) {
	if config.Address == "" || config.ServerName == "" || config.FromAddress == "" || config.AppOrigin == "" {
		return nil, errors.New("SMTP address, server name, sender, and application origin are required")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("SMTP username and password must be configured together")
	}
	if strings.ContainsAny(config.FromName, "\r\n") {
		return nil, errors.New("SMTP sender name is invalid")
	}
	parsedAddress, err := mail.ParseAddress(config.FromAddress)
	if err != nil {
		return nil, errors.New("SMTP sender address is invalid")
	}
	if parsedAddress.Address != config.FromAddress {
		return nil, errors.New("SMTP sender address must not include a display name")
	}
	origin, err := url.Parse(config.AppOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("SMTP application origin must be an exact HTTPS origin")
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	return &Sender{config: config, from: mail.Address{Name: config.FromName, Address: parsedAddress.Address}, origin: strings.TrimSuffix(config.AppOrigin, "/")}, nil
}

func (s *Sender) SendVerification(ctx context.Context, message registration.VerificationMessage) error {
	link := s.origin + "/verify?token=" + url.QueryEscape(message.Token)
	subject := "Verify your Infinite Ocean identity"
	plain := fmt.Sprintf("Hello %s,\r\n\r\nVerify your identity and secure your Spyglass Account:\r\n%s\r\n\r\nThis link expires at %s.\r\n", message.DisplayName, link, message.ExpiresAt.UTC().Format(time.RFC1123))
	htmlBody := fmt.Sprintf("<p>Hello %s,</p><p>Verify your identity and secure your Spyglass Account.</p><p><a href=\"%s\">Verify identity</a></p><p>This link expires at %s.</p>", html.EscapeString(message.DisplayName), html.EscapeString(link), html.EscapeString(message.ExpiresAt.UTC().Format(time.RFC1123)))
	return s.send(ctx, message.Email, subject, plain, htmlBody)
}

func (s *Sender) SendInvitation(ctx context.Context, message invitations.Message) error {
	link := s.origin + "/invitations/accept?token=" + url.QueryEscape(message.Token)
	subject := "Join " + message.AccountName + " in Spyglass"
	plain := fmt.Sprintf("You have been invited to join %s in Infinite Ocean: Spyglass as %s.\r\n\r\nAccept the invitation:\r\n%s\r\n\r\nThis link expires at %s.\r\n", message.AccountName, message.Role, link, message.ExpiresAt.UTC().Format(time.RFC1123))
	htmlBody := fmt.Sprintf("<p>You have been invited to join <strong>%s</strong> in Infinite Ocean: Spyglass as %s.</p><p><a href=\"%s\">Accept invitation</a></p><p>This link expires at %s.</p>", html.EscapeString(message.AccountName), html.EscapeString(string(message.Role)), html.EscapeString(link), html.EscapeString(message.ExpiresAt.UTC().Format(time.RFC1123)))
	return s.send(ctx, message.Email, subject, plain, htmlBody)
}

func (s *Sender) SendRecovery(ctx context.Context, message recovery.Message) error {
	if message.Suppress {
		return nil
	}
	link := s.origin + "/reset-password?token=" + url.QueryEscape(message.Token)
	subject := "Reset your Infinite Ocean identity password"
	plain := fmt.Sprintf("Hello %s,\r\n\r\nA password reset was requested for your Infinite Ocean identity. Set a new password here:\r\n%s\r\n\r\nThis single-use link expires at %s. If you did not request it, no change has been made.\r\n", message.DisplayName, link, message.ExpiresAt.UTC().Format(time.RFC1123))
	htmlBody := fmt.Sprintf("<p>Hello %s,</p><p>A password reset was requested for your Infinite Ocean identity.</p><p><a href=\"%s\">Set a new password</a></p><p>This single-use link expires at %s. If you did not request it, no change has been made.</p>", html.EscapeString(message.DisplayName), html.EscapeString(link), html.EscapeString(message.ExpiresAt.UTC().Format(time.RFC1123)))
	return s.send(ctx, message.Email, subject, plain, htmlBody)
}

func (s *Sender) send(ctx context.Context, to, subject, plain, htmlBody string) error {
	parsedTo, err := mail.ParseAddress(to)
	if err != nil || parsedTo.Address != to {
		return errors.New("notification recipient is invalid")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return errors.New("notification subject is invalid")
	}
	body, err := messageBody(s.from, mail.Address{Address: to}, subject, plain, htmlBody)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: s.config.Timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("SMTP dial: %w", err)
	}
	deadline := time.Now().Add(s.config.Timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	tlsConnection := tls.Client(connection, &tls.Config{ServerName: s.config.ServerName, MinVersion: tls.VersionTLS12})
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return fmt.Errorf("SMTP TLS: %w", err)
	}
	client, err := stdsmtp.NewClient(tlsConnection, s.config.ServerName)
	if err != nil {
		_ = tlsConnection.Close()
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer client.Close()
	if s.config.Username != "" {
		if err := client.Auth(stdsmtp.PlainAuth("", s.config.Username, s.config.Password, s.config.ServerName)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("SMTP sender: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP recipient: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP data: %w", err)
	}
	if _, err = w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return fmt.Errorf("SMTP write: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("SMTP finish: %w", err)
	}
	if err = client.Quit(); err != nil {
		return fmt.Errorf("SMTP quit: %w", err)
	}
	return nil
}

func messageBody(from, to mail.Address, subject, plain, htmlBody string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("notification message identity unavailable")
	}
	boundary := "spyglass_" + hex.EncodeToString(random[:])
	headers := []string{"From: " + from.String(), "To: " + to.String(), "Subject: " + mime.QEncoding.Encode("utf-8", subject), "Date: " + time.Now().UTC().Format(time.RFC1123Z), "MIME-Version: 1.0", `Content-Type: multipart/alternative; boundary="` + boundary + `"`}
	body := strings.Join(headers, "\r\n") + "\r\n\r\n--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + plain + "\r\n--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + htmlBody + "\r\n--" + boundary + "--\r\n"
	return body, nil
}

var _ registration.VerificationSender = (*Sender)(nil)
var _ invitations.Sender = (*Sender)(nil)
var _ recovery.Sender = (*Sender)(nil)
