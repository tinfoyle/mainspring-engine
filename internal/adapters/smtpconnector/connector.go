// Package smtpconnector performs governed Marketing email delivery over
// implicit-TLS SMTP. Credential bytes are parsed only inside the dedicated
// connector runtime and are never retained after the operation lease closes.
package smtpconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	stdsmtp "net/smtp"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

const (
	ProviderCode      = "smtp"
	maximumRecipients = 500
)

type credentialDocument struct {
	Version           uint64   `json:"version"`
	Address           string   `json:"address"`
	ServerName        string   `json:"server_name"`
	Username          string   `json:"username,omitempty"`
	Password          string   `json:"password,omitempty"`
	FromAddress       string   `json:"from_address"`
	FromName          string   `json:"from_name,omitempty"`
	AudienceReference string   `json:"audience_reference"`
	Recipients        []string `json:"recipients"`
	RootCAPEM         string   `json:"root_ca_pem,omitempty"`
}

type credential struct {
	credentialDocument
	roots *x509.CertPool
}

type session interface {
	Auth(stdsmtp.Auth) error
	Mail(string) error
	Rcpt(string) error
	Data() (io.WriteCloser, error)
	Noop() error
	Quit() error
	Close() error
}

type dialSession func(context.Context, credential) (session, error)

type Connector struct{ dial dialSession }

func New() *Connector { return &Connector{dial: dialImplicitTLS} }

func (connector *Connector) Probe(ctx context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if connector == nil || connector.dial == nil || call.Claim.ConnectorKind != domain.ConnectorEmail || call.Claim.CredentialProvider != ProviderCode {
		return healthUnavailable("smtp_health_connector_mismatch")
	}
	config, err := parseCredential(call.Credential)
	if err != nil || call.Claim.Scope.EmailAddress != config.FromAddress ||
		call.Claim.Scope.AudienceReference != config.AudienceReference || call.Claim.Scope.HTTPSOrigin != "" || call.Claim.Scope.PathPrefix != "" {
		return healthUnavailable("smtp_health_configuration_invalid")
	}
	client, err := connector.dial(ctx, config)
	if err != nil {
		return healthUnavailable("smtp_health_unavailable")
	}
	defer client.Close()
	if config.Username != "" {
		if err := client.Auth(stdsmtp.PlainAuth("", config.Username, config.Password, config.ServerName)); err != nil {
			return healthUnavailable("smtp_health_authentication_failed")
		}
	}
	if err := client.Noop(); err != nil {
		return healthUnavailable("smtp_health_unavailable")
	}
	_ = client.Quit()
	return integrationhealth.ProbeResult{State: domain.HealthHealthy}
}

func (connector *Connector) Execute(ctx context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	if connector == nil || connector.dial == nil || call.Claim.Capability != domain.CapabilityEmailSend || call.Claim.CredentialProvider != ProviderCode {
		return failed("smtp_connector_mismatch")
	}
	config, err := parseCredential(call.Credential)
	if err != nil {
		return failed("smtp_configuration_invalid")
	}
	envelope, err := integrationexecution.DecodeProviderEnvelope(call.Payload.ProviderPayload, call.Claim)
	if err != nil {
		return failed("smtp_payload_invalid")
	}
	if envelope.Scope.EmailAddress != config.FromAddress || envelope.Scope.AudienceReference != config.AudienceReference ||
		envelope.Scope.HTTPSOrigin != "" || envelope.Scope.PathPrefix != "" {
		return failed("smtp_scope_mismatch")
	}
	message, err := buildMessage(config, envelope, call.At.UTC())
	if err != nil {
		return failed("smtp_payload_invalid")
	}
	client, err := connector.dial(ctx, config)
	if err != nil {
		return failed("smtp_unavailable")
	}
	defer client.Close()
	if config.Username != "" {
		if err := client.Auth(stdsmtp.PlainAuth("", config.Username, config.Password, config.ServerName)); err != nil {
			return failed("smtp_authentication_failed")
		}
	}
	if err := client.Mail(config.FromAddress); err != nil {
		return failed("smtp_sender_rejected")
	}
	for _, recipient := range config.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return failed("smtp_recipient_rejected")
		}
	}
	writer, err := client.Data()
	if err != nil {
		return failed("smtp_data_rejected")
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return unknown("smtp_delivery_uncertain")
	}
	if err := writer.Close(); err != nil {
		return unknown("smtp_delivery_uncertain")
	}
	// DATA acceptance is the durable provider boundary. A failed QUIT cannot
	// safely turn the result into a retry because the message may be queued.
	_ = client.Quit()
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}
}

func (connector *Connector) Reconcile(_ context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	if connector == nil || call.Claim.Capability != domain.CapabilityEmailSend || call.Claim.CredentialProvider != ProviderCode {
		return failed("smtp_connector_mismatch")
	}
	// SMTP has no portable provider query that can prove non-application. The
	// immutable Message-ID supports operator/provider investigation, but an
	// uncertain send must never be retried automatically.
	return unknown("smtp_reconciliation_unavailable")
}

func parseCredential(raw []byte) (credential, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return credential{}, errors.New("credential size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document credentialDocument
	if err := decoder.Decode(&document); err != nil {
		return credential{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return credential{}, errors.New("credential has trailing JSON")
	}
	if document.Version != 1 || document.Address == "" || document.ServerName == "" || document.FromAddress == "" ||
		document.AudienceReference == "" || len(document.Recipients) == 0 || len(document.Recipients) > maximumRecipients ||
		(document.Username == "") != (document.Password == "") || strings.ContainsAny(document.FromName, "\r\n") ||
		strings.ContainsAny(document.AudienceReference, "\r\n") {
		return credential{}, errors.New("credential fields are invalid")
	}
	host, port, err := net.SplitHostPort(document.Address)
	if err != nil || host == "" || port == "" || strings.ContainsAny(document.ServerName, ":/\\\r\n") {
		return credential{}, errors.New("SMTP endpoint is invalid")
	}
	from, err := mail.ParseAddress(document.FromAddress)
	if err != nil || from.Address != document.FromAddress || strings.ContainsAny(document.FromAddress, "\r\n") {
		return credential{}, errors.New("SMTP sender is invalid")
	}
	seen := make(map[string]struct{}, len(document.Recipients))
	for _, value := range document.Recipients {
		parsed, parseErr := mail.ParseAddress(value)
		if parseErr != nil || parsed.Address != value || strings.ContainsAny(value, "\r\n") {
			return credential{}, errors.New("SMTP recipient is invalid")
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return credential{}, errors.New("SMTP recipient is duplicated")
		}
		seen[key] = struct{}{}
	}
	sort.Strings(document.Recipients)
	var roots *x509.CertPool
	if document.RootCAPEM != "" {
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(document.RootCAPEM)) {
			return credential{}, errors.New("SMTP root CA is invalid")
		}
	}
	return credential{credentialDocument: document, roots: roots}, nil
}

func dialImplicitTLS(ctx context.Context, config credential) (session, error) {
	dialer := net.Dialer{Timeout: 15 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", config.Address)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	tlsConnection := tls.Client(connection, &tls.Config{ServerName: config.ServerName, MinVersion: tls.VersionTLS12, RootCAs: config.roots})
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	client, err := stdsmtp.NewClient(tlsConnection, config.ServerName)
	if err != nil {
		_ = tlsConnection.Close()
		return nil, err
	}
	return client, nil
}

func buildMessage(config credential, envelope integrationexecution.ProviderEnvelope, at time.Time) ([]byte, error) {
	var body *integrationexecution.ProviderAsset
	attachments := make([]integrationexecution.ProviderAsset, 0, len(envelope.Assets))
	for index := range envelope.Assets {
		asset := &envelope.Assets[index]
		if asset.Kind == marketingdomain.AssetCopy {
			if body != nil || (asset.MediaType != "text/plain" && asset.MediaType != "text/html") {
				return nil, errors.New("email requires one supported copy asset")
			}
			body = asset
			continue
		}
		attachments = append(attachments, *asset)
	}
	if body == nil || strings.ContainsAny(body.Title, "\r\n") {
		return nil, errors.New("email copy is invalid")
	}
	from := mail.Address{Name: config.FromName, Address: config.FromAddress}
	messageIDDomain := config.FromAddress[strings.LastIndexByte(config.FromAddress, '@')+1:]
	headers := []string{
		"From: " + from.String(),
		"To: " + strings.Join(config.Recipients, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", body.Title),
		"Date: " + at.Format(time.RFC1123Z),
		"Message-ID: <spyglass-" + string(envelope.IdempotencyKey) + "@" + messageIDDomain + ">",
		"MIME-Version: 1.0",
	}
	if len(attachments) == 0 {
		headers = append(headers, "Content-Type: "+body.MediaType+"; charset=utf-8", "Content-Transfer-Encoding: 8bit")
		return append([]byte(strings.Join(headers, "\r\n")+"\r\n\r\n"), append(body.Content, []byte("\r\n")...)...), nil
	}
	boundaryDigest := sha256.Sum256([]byte(envelope.IdempotencyKey))
	boundary := "spyglass_" + hex.EncodeToString(boundaryDigest[:12])
	headers = append(headers, `Content-Type: multipart/mixed; boundary="`+boundary+`"`)
	var output bytes.Buffer
	output.WriteString(strings.Join(headers, "\r\n") + "\r\n\r\n")
	writer := multipart.NewWriter(&output)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, err
	}
	bodyHeader := make(textproto.MIMEHeader)
	bodyHeader.Set("Content-Type", body.MediaType+"; charset=utf-8")
	bodyHeader.Set("Content-Transfer-Encoding", "8bit")
	part, err := writer.CreatePart(bodyHeader)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(body.Content); err != nil {
		return nil, err
	}
	for _, asset := range attachments {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", asset.MediaType)
		header.Set("Content-Disposition", `attachment; filename="`+string(asset.ID)+`"`)
		header.Set("Content-Transfer-Encoding", "base64")
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if err := writeBase64Lines(part, asset.Content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeBase64Lines(writer io.Writer, value []byte) error {
	encoded := base64.StdEncoding.EncodeToString(value)
	for len(encoded) > 76 {
		if _, err := io.WriteString(writer, encoded[:76]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err := io.WriteString(writer, encoded+"\r\n")
	return err
}

func failed(code string) integrationexecution.ConnectorResult {
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptFailed, ErrorCode: code}
}

func unknown(code string) integrationexecution.ConnectorResult {
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptUnknown, ErrorCode: code}
}

var _ integrationexecution.Connector = (*Connector)(nil)
var _ integrationhealth.Probe = (*Connector)(nil)

func healthUnavailable(code string) integrationhealth.ProbeResult {
	return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: code}
}
