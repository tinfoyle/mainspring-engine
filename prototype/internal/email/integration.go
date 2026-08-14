package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/secretbox"
)

var (
	ErrNotConfigured   = errors.New("email inbox is not configured")
	ErrMessageNotFound = errors.New("email message was not found")
)

type Integration struct {
	ID             string
	Name           string
	EmailAddress   string
	DisplayName    string
	IMAPHost       string
	IMAPPort       int
	IMAPSecurity   string
	IMAPUsername   string
	IMAPPassword   string
	SMTPHost       string
	SMTPPort       int
	SMTPSecurity   string
	SMTPUsername   string
	SMTPPassword   string
	Status         string
	LastVerifiedAt *time.Time
	LastError      string
}

type SettingsInput struct {
	Name         string
	EmailAddress string
	DisplayName  string
	IMAPHost     string
	IMAPPort     int
	IMAPSecurity string
	IMAPUsername string
	IMAPPassword string
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	SMTPUsername string
	SMTPPassword string
}

func (input SettingsInput) integration() (Integration, error) {
	integration := Integration{
		Name: strings.TrimSpace(input.Name), EmailAddress: strings.TrimSpace(input.EmailAddress),
		DisplayName: strings.TrimSpace(input.DisplayName), IMAPHost: strings.TrimSpace(input.IMAPHost),
		IMAPPort: input.IMAPPort, IMAPSecurity: strings.ToLower(strings.TrimSpace(input.IMAPSecurity)),
		IMAPUsername: strings.TrimSpace(input.IMAPUsername), IMAPPassword: input.IMAPPassword,
		SMTPHost: strings.TrimSpace(input.SMTPHost), SMTPPort: input.SMTPPort,
		SMTPSecurity: strings.ToLower(strings.TrimSpace(input.SMTPSecurity)),
		SMTPUsername: strings.TrimSpace(input.SMTPUsername), SMTPPassword: input.SMTPPassword,
		Status: "active",
	}
	if integration.Name == "" || len(integration.Name) > 120 {
		return Integration{}, errors.New("mailbox name is required and must be 120 characters or fewer")
	}
	address, err := mail.ParseAddress(integration.EmailAddress)
	if err != nil || !strings.EqualFold(address.Address, integration.EmailAddress) {
		return Integration{}, errors.New("a valid mailbox email address is required")
	}
	if len(integration.DisplayName) > 120 {
		return Integration{}, errors.New("sender display name must be 120 characters or fewer")
	}
	for label, host := range map[string]string{"IMAP": integration.IMAPHost, "SMTP": integration.SMTPHost} {
		if host == "" || len(host) > 253 || strings.ContainsAny(host, " \t\r\n/\\") {
			return Integration{}, fmt.Errorf("%s host is invalid", label)
		}
	}
	if integration.IMAPPort < 1 || integration.IMAPPort > 65535 || integration.SMTPPort < 1 || integration.SMTPPort > 65535 {
		return Integration{}, errors.New("mail server ports must be between 1 and 65535")
	}
	if !validSecurity(integration.IMAPSecurity) || !validSecurity(integration.SMTPSecurity) {
		return Integration{}, errors.New("mail connections must use TLS or STARTTLS")
	}
	if integration.IMAPUsername == "" || integration.SMTPUsername == "" || integration.IMAPPassword == "" || integration.SMTPPassword == "" {
		return Integration{}, errors.New("IMAP and SMTP usernames and passwords are required")
	}
	if len(integration.IMAPUsername) > 320 || len(integration.SMTPUsername) > 320 || len(integration.IMAPPassword) > 4096 || len(integration.SMTPPassword) > 4096 {
		return Integration{}, errors.New("mail credentials are too long")
	}
	return integration, nil
}

func validSecurity(value string) bool { return value == SecurityTLS || value == SecurityStartTLS }

func ParseRecipientList(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return nil, errors.New("enter valid email recipients separated by commas")
	}
	if len(addresses) > 50 {
		return nil, errors.New("email cannot have more than 50 recipients")
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.String())
	}
	return result, nil
}

type Store struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

func NewStore(pool *pgxpool.Pool, box *secretbox.Box) *Store { return &Store{pool: pool, box: box} }

func (s *Store) Primary(ctx context.Context) (Integration, error) {
	var result Integration
	var imapPassword, smtpPassword string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, name, email_address, display_name, imap_host, imap_port, imap_security,
		       imap_username, imap_password_ciphertext, smtp_host, smtp_port, smtp_security,
		       smtp_username, smtp_password_ciphertext, status, last_verified_at, COALESCE(last_error, '')
		FROM email_integrations WHERE status <> 'disabled' ORDER BY created_at LIMIT 1
	`).Scan(&result.ID, &result.Name, &result.EmailAddress, &result.DisplayName, &result.IMAPHost,
		&result.IMAPPort, &result.IMAPSecurity, &result.IMAPUsername, &imapPassword, &result.SMTPHost,
		&result.SMTPPort, &result.SMTPSecurity, &result.SMTPUsername, &smtpPassword, &result.Status,
		&result.LastVerifiedAt, &result.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotConfigured
	}
	if err != nil {
		return Integration{}, fmt.Errorf("load email integration: %w", err)
	}
	if result.IMAPPassword, err = s.box.Decrypt(imapPassword); err != nil {
		return Integration{}, err
	}
	if result.SMTPPassword, err = s.box.Decrypt(smtpPassword); err != nil {
		return Integration{}, err
	}
	return result, nil
}

func (s *Store) SavePrimary(ctx context.Context, integration Integration, userID string) (Integration, error) {
	imapPassword, err := s.box.Encrypt(integration.IMAPPassword)
	if err != nil {
		return Integration{}, err
	}
	smtpPassword, err := s.box.Encrypt(integration.SMTPPassword)
	if err != nil {
		return Integration{}, err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO email_integrations (name, email_address, display_name, imap_host, imap_port,
			imap_security, imap_username, imap_password_ciphertext, smtp_host, smtp_port,
			smtp_security, smtp_username, smtp_password_ciphertext, status, last_verified_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'active',now(),$14)
		ON CONFLICT ((true)) WHERE status <> 'disabled' DO UPDATE SET
			name=EXCLUDED.name, email_address=EXCLUDED.email_address, display_name=EXCLUDED.display_name,
			imap_host=EXCLUDED.imap_host, imap_port=EXCLUDED.imap_port, imap_security=EXCLUDED.imap_security,
			imap_username=EXCLUDED.imap_username, imap_password_ciphertext=EXCLUDED.imap_password_ciphertext,
			smtp_host=EXCLUDED.smtp_host, smtp_port=EXCLUDED.smtp_port, smtp_security=EXCLUDED.smtp_security,
			smtp_username=EXCLUDED.smtp_username, smtp_password_ciphertext=EXCLUDED.smtp_password_ciphertext,
			status='active', last_verified_at=now(), last_error=NULL, updated_at=now()
		RETURNING id::text
	`, integration.Name, integration.EmailAddress, integration.DisplayName, integration.IMAPHost,
		integration.IMAPPort, integration.IMAPSecurity, integration.IMAPUsername, imapPassword,
		integration.SMTPHost, integration.SMTPPort, integration.SMTPSecurity, integration.SMTPUsername,
		smtpPassword, userID).Scan(&id)
	if err != nil {
		return Integration{}, fmt.Errorf("save email integration: %w", err)
	}
	return s.Primary(ctx)
}

func (s *Store) MarkError(ctx context.Context, id string, cause error) {
	if cause == nil {
		return
	}
	_, _ = s.pool.Exec(ctx, `UPDATE email_integrations SET status='error', last_error=$2, updated_at=now() WHERE id=$1`, id, cause.Error())
}

func (s *Store) PrepareOutbox(ctx context.Context, integrationID, key string, message OutgoingMessage, actorType, actorID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO email_outbox (integration_id, idempotency_key, recipients, subject, body, requested_by_type, requested_by_id)
		VALUES ($1,$2,jsonb_build_object('to',$3::text[],'cc',$4::text[]),$5,$6,$7,NULLIF($8,''))
		ON CONFLICT (idempotency_key) DO NOTHING
	`, integrationID, key, message.To, message.CC, message.Subject, message.Body, actorType, actorID)
	return err
}

func (s *Store) MarkOutbox(ctx context.Context, key, status, messageID, lastError string, sentAt *time.Time) {
	_, _ = s.pool.Exec(ctx, `UPDATE email_outbox SET status=$2, message_id=NULLIF($3,''), last_error=NULLIF($4,''), sent_at=$5, updated_at=now() WHERE idempotency_key=$1`, key, status, messageID, lastError, sentAt)
}
