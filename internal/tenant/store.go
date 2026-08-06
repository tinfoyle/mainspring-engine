package tenant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/auth"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrAuthenticationFailed = errors.New("authentication failed")
	ErrSetupComplete        = errors.New("tenant setup is already complete")
	ErrSessionNotFound      = errors.New("session not found")
)

type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Bootstrap(ctx context.Context, tenantID domain.TenantID, slug, displayName string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, slug, display_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		SET slug = EXCLUDED.slug, display_name = EXCLUDED.display_name, updated_at = now()
	`, tenantID.String(), strings.ToLower(strings.TrimSpace(slug)), strings.TrimSpace(displayName))
	if err != nil {
		return fmt.Errorf("bootstrap tenant settings: %w", err)
	}
	return nil
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tenant users: %w", err)
	}
	return exists, nil
}

func (s *Store) CreateOwner(ctx context.Context, email, displayName, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") {
		return User{}, errors.New("a valid email is required")
	}
	if displayName == "" {
		return User{}, errors.New("display name is required")
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return User{}, fmt.Errorf("begin owner setup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var hasUsers bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&hasUsers); err != nil {
		return User{}, fmt.Errorf("check owner setup: %w", err)
	}
	if hasUsers {
		return User{}, ErrSetupComplete
	}

	user := User{Email: email, DisplayName: displayName, Role: "owner"}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ($1, $2, $3, 'owner')
		RETURNING id::text
	`, email, displayName, passwordHash).Scan(&user.ID); err != nil {
		return User{}, fmt.Errorf("create owner: %w", err)
	}

	if err := seedDefaultBoardroom(ctx, tx); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit owner setup: %w", err)
	}
	return user, nil
}

func seedDefaultBoardroom(ctx context.Context, tx pgx.Tx) error {
	var boardroomID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO boardrooms (name, description, max_turns)
		VALUES ('Back Office', 'Your operational team for scheduling, invoicing, paperwork, and follow-up.', 6)
		RETURNING id::text
	`).Scan(&boardroomID); err != nil {
		return fmt.Errorf("create default boardroom: %w", err)
	}

	type personaSeed struct {
		name, role, instructions string
		position                 int
		grants                   []domain.Capability
	}
	personas := []personaSeed{
		{
			name: "Morgan", role: "Office Manager", position: 1,
			instructions: "Coordinate the boardroom, identify exceptions, and turn discussion into a concise action list for the owner.",
			grants:       []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityTicketRead, domain.CapabilityTicketCreate, domain.CapabilityScheduleRead},
		},
		{
			name: "Casey", role: "Bookkeeper", position: 2,
			instructions: "Review invoicing and accounts-payable information carefully. Prepare actions, surface missing records, and never execute a payment without approval.",
			grants:       []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityInvoicePrepare, domain.CapabilityPaymentPropose},
		},
		{
			name: "Riley", role: "Dispatcher", position: 3,
			instructions: "Review jobs, crews, appointments, and conflicts. Recommend practical scheduling changes without applying them automatically.",
			grants:       []domain.Capability{domain.CapabilityScheduleRead, domain.CapabilityTicketRead, domain.CapabilitySchedulePropose},
		},
	}

	for _, persona := range personas {
		var personaID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO personas (boardroom_id, name, role, system_instructions, position)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id::text
		`, boardroomID, persona.name, persona.role, persona.instructions, persona.position).Scan(&personaID); err != nil {
			return fmt.Errorf("create default persona: %w", err)
		}
		for _, capability := range persona.grants {
			if _, err := tx.Exec(ctx, `
				INSERT INTO persona_tool_grants (persona_id, capability)
				VALUES ($1, $2)
			`, personaID, string(capability)); err != nil {
				return fmt.Errorf("grant default capability: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	var user User
	var passwordHash string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email::text, display_name, role, password_hash
		FROM users
		WHERE email = $1 AND disabled_at IS NULL
	`, strings.ToLower(strings.TrimSpace(email))).Scan(
		&user.ID, &user.Email, &user.DisplayName, &user.Role, &passwordHash,
	)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !auth.CheckPassword(passwordHash, password)) {
		return User{}, ErrAuthenticationFailed
	}
	if err != nil {
		return User{}, fmt.Errorf("load user for authentication: %w", err)
	}
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID, userAgent, ipAddress string, lifetime time.Duration) (string, time.Time, error) {
	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(lifetime)
	var ip any
	if strings.TrimSpace(ipAddress) != "" {
		ip = strings.TrimSpace(ipAddress)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, tokenHash, truncate(userAgent, 512), ip, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return raw, expiresAt, nil
}

func (s *Store) UserBySession(ctx context.Context, rawToken string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.email::text, u.display_name, u.role
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL
	`, auth.HashToken(rawToken)).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrSessionNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load session: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1`, auth.HashToken(rawToken))
	return user, nil
}

func (s *Store) DeleteSession(ctx context.Context, rawToken string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, auth.HashToken(rawToken))
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
