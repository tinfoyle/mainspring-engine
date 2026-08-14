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
	ErrInvitationNotFound   = errors.New("invitation not found")
	ErrMemberNotFound       = errors.New("member not found")
	ErrOwnerProtected       = errors.New("the organization owner is protected")
)

type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        string
	State       string
}

type Invitation struct {
	ID          string
	Email       string
	DisplayName string
	ExpiresAt   time.Time
}

type Announcement struct {
	ID          string
	Title       string
	Body        string
	Category    string
	PublishedAt time.Time
	ReadAt      *time.Time
}

// MessengerMessage is deliberately channel-scoped rather than permission-scoped
// for the MVP. Future direct messages or restricted support conversations can
// add a recipient model without changing the tenant boundary.
type MessengerMessage struct {
	ID         string
	Channel    string
	SenderName string
	Body       string
	CreatedAt  time.Time
}

type Store struct {
	pool     *pgxpool.Pool
	template BusinessTemplate
}

func NewStore(pool *pgxpool.Pool, templates ...BusinessTemplate) *Store {
	template := TemplateTrades
	if len(templates) > 0 {
		template = ParseBusinessTemplate(templates[0].String())
	}
	return &Store{pool: pool, template: template}
}

func (s *Store) Bootstrap(ctx context.Context, tenantID domain.TenantID, slug, displayName string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, slug, display_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		SET slug = EXCLUDED.slug,
		    display_name = COALESCE(
		        (SELECT NULLIF(o.business_profile->>'business_name', '')
		         FROM tenant_onboarding o
		         WHERE o.tenant_id = EXCLUDED.tenant_id AND o.status = 'completed'),
		        EXCLUDED.display_name
		    ),
		    updated_at = now()
	`, tenantID.String(), strings.ToLower(strings.TrimSpace(slug)), strings.TrimSpace(displayName))
	if err != nil {
		return fmt.Errorf("bootstrap tenant settings: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO tenant_onboarding (tenant_id) VALUES ($1)
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID.String()); err != nil {
		return fmt.Errorf("bootstrap tenant onboarding: %w", err)
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

func (s *Store) TenantDisplayName(ctx context.Context, tenantID domain.TenantID) (string, error) {
	var displayName string
	if err := s.pool.QueryRow(ctx, `SELECT display_name FROM tenant_settings WHERE tenant_id = $1`, tenantID.String()).Scan(&displayName); err != nil {
		return "", fmt.Errorf("load tenant display name: %w", err)
	}
	return displayName, nil
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

	if err := seedDefaultBoardroom(ctx, tx, s.template); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit owner setup: %w", err)
	}
	return user, nil
}

type personaSeed struct {
	name, role, instructions string
	position                 int
	grants                   []domain.Capability
	provider, reasoning      string
	maxToolCalls             int
}

func defaultPersonaSeeds() []personaSeed {
	return []personaSeed{
		{
			name: "Main Manager", role: "Main Manager", position: 1,
			instructions: "Act as the organization's evidence-first operating manager. Coordinate work across the boardroom, use available tools to inspect documents and operational context before reaching conclusions, create focused follow-up work when justified, and give the owner a concise prioritized action list. Never claim a tool was used unless its result appears in the conversation. Do not send messages or make external changes; propose them for approval.",
			grants:       []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityDocumentsWrite, domain.CapabilityWebSearch, domain.CapabilityWebRead, domain.CapabilityTicketRead, domain.CapabilityTicketCreate, domain.CapabilityScheduleRead, domain.CapabilitySchedulePropose, domain.CapabilityEmailDraft},
			provider:     "codex", reasoning: "high", maxToolCalls: 8,
		},
		{
			name: "Casey", role: "Bookkeeper", position: 2,
			instructions: "Review invoicing and accounts-payable information carefully. Prepare actions, surface missing records, and never execute a payment without approval.",
			grants:       []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityDocumentsWrite, domain.CapabilityInvoicePrepare, domain.CapabilityPaymentPropose},
		},
		{
			name: "Riley", role: "Dispatcher", position: 3,
			instructions: "Review jobs, crews, appointments, and conflicts. Recommend practical scheduling changes without applying them automatically.",
			grants:       []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityDocumentsWrite, domain.CapabilityScheduleRead, domain.CapabilityTicketRead, domain.CapabilitySchedulePropose},
		},
	}
}

func personaSeedsForTemplate(template BusinessTemplate) []personaSeed {
	if template.IsSoftware() {
		return softwareDefaultPersonaSeeds()
	}
	return defaultPersonaSeeds()
}

func seedDefaultBoardroom(ctx context.Context, tx pgx.Tx, template BusinessTemplate) error {
	name := "Back Office"
	description := "Your operational team for scheduling, invoicing, paperwork, and follow-up."
	if template.IsSoftware() {
		name = "Company Operating Room"
		description = "Your cross-functional team for product, customers, revenue, delivery, and risk."
	}
	var boardroomID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO boardrooms (name, description, max_turns)
		VALUES ($1, $2, 6)
		RETURNING id::text
	`, name, description).Scan(&boardroomID); err != nil {
		return fmt.Errorf("create default boardroom: %w", err)
	}

	return applyPersonaSeeds(ctx, tx, boardroomID, template)
}

func resetDefaultBoardroom(ctx context.Context, tx pgx.Tx, template BusinessTemplate) error {
	var boardroomID string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM boardrooms WHERE status <> 'archived' ORDER BY created_at LIMIT 1 FOR UPDATE
	`).Scan(&boardroomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return seedDefaultBoardroom(ctx, tx, template)
	}
	if err != nil {
		return fmt.Errorf("load default boardroom for reset: %w", err)
	}
	name := "Back Office"
	description := "Your operational team for scheduling, invoicing, paperwork, and follow-up."
	if template.IsSoftware() {
		name = "Company Operating Room"
		description = "Your cross-functional team for product, customers, revenue, delivery, and risk."
	}
	if _, err := tx.Exec(ctx, `
		UPDATE boardrooms
		SET name = $2, description = $3,
		    max_turns = 6, status = 'active', updated_at = now()
		WHERE id = $1
	`, boardroomID, name, description); err != nil {
		return fmt.Errorf("reset default boardroom: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE personas SET enabled = false, updated_at = now() WHERE boardroom_id = $1`, boardroomID); err != nil {
		return fmt.Errorf("disable personas for reset: %w", err)
	}
	return applyPersonaSeeds(ctx, tx, boardroomID, template)
}

func applyPersonaSeeds(ctx context.Context, tx pgx.Tx, boardroomID string, template BusinessTemplate) error {
	for _, persona := range personaSeedsForTemplate(template) {
		provider, reasoning, maxToolCalls := persona.provider, persona.reasoning, persona.maxToolCalls
		if provider == "" {
			provider = "inherit"
		}
		if reasoning == "" {
			reasoning = "inherit"
		}
		if maxToolCalls == 0 {
			maxToolCalls = 5
		}
		var personaID string
		if err := tx.QueryRow(ctx, `
		INSERT INTO personas (boardroom_id, name, role, system_instructions, position, enabled, provider, reasoning_effort, max_tool_calls)
		VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8)
		ON CONFLICT (boardroom_id, position) DO UPDATE
		SET name = EXCLUDED.name, role = EXCLUDED.role, system_instructions = EXCLUDED.system_instructions,
		    enabled = true, provider = EXCLUDED.provider, reasoning_effort = EXCLUDED.reasoning_effort,
		    max_tool_calls = EXCLUDED.max_tool_calls, updated_at = now()
		RETURNING id::text
		`, boardroomID, persona.name, persona.role, persona.instructions, persona.position, provider, reasoning, maxToolCalls).Scan(&personaID); err != nil {
			return fmt.Errorf("create default persona: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM persona_tool_grants WHERE persona_id = $1`, personaID); err != nil {
			return fmt.Errorf("reset default persona grants: %w", err)
		}
		for _, capability := range persona.grants {
			if _, err := tx.Exec(ctx, `
				INSERT INTO persona_tool_grants (persona_id, capability)
				VALUES ($1, $2)
			`, personaID, string(capability)); err != nil {
				return fmt.Errorf("grant default capability: %w", err)
			}
		}
		for _, capability := range []domain.Capability{domain.CapabilityWebSearch, domain.CapabilityWebRead} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO persona_tool_grants (persona_id, capability)
				VALUES ($1, $2)
				ON CONFLICT (persona_id, capability) DO NOTHING
			`, personaID, string(capability)); err != nil {
				return fmt.Errorf("grant baseline web capability: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	var user User
	var passwordHash string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email::text, display_name, role, password_hash, state
		FROM users
		WHERE email = $1 AND state = 'active' AND disabled_at IS NULL
	`, strings.ToLower(strings.TrimSpace(email))).Scan(
		&user.ID, &user.Email, &user.DisplayName, &user.Role, &passwordHash, &user.State,
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
		SELECT u.id::text, u.email::text, u.display_name, u.role, u.state
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL AND u.state = 'active'
	`, auth.HashToken(rawToken)).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrSessionNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load session: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1`, auth.HashToken(rawToken))
	return user, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, email::text, display_name, role, state
		FROM users
		WHERE email = $1 AND state = 'active' AND disabled_at IS NULL
	`, strings.ToLower(strings.TrimSpace(email))).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrAuthenticationFailed
	}
	if err != nil {
		return User{}, fmt.Errorf("load user by email: %w", err)
	}
	return user, nil
}

func (s *Store) ListMembers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, email::text, display_name, role, state FROM users ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, created_at`)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.State); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}
	return users, nil
}

func (s *Store) CreateMemberInvitation(ctx context.Context, email, displayName string) (Invitation, string, error) {
	email, displayName = strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") {
		return Invitation{}, "", errors.New("a valid email is required")
	}
	if displayName == "" || len(displayName) > 120 {
		return Invitation{}, "", errors.New("a display name of 1-120 characters is required")
	}
	raw, hash, err := auth.NewToken()
	if err != nil {
		return Invitation{}, "", err
	}
	invitation := Invitation{Email: email, DisplayName: displayName, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invitation{}, "", fmt.Errorf("begin member invitation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, role string
	err = tx.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role, state) VALUES ($1,$2,NULL,'member','pending') ON CONFLICT (email) DO UPDATE SET display_name = EXCLUDED.display_name, role = CASE WHEN users.role = 'owner' THEN users.role ELSE 'member' END, state = CASE WHEN users.role = 'owner' THEN users.state ELSE 'pending' END, disabled_at = CASE WHEN users.role = 'owner' THEN users.disabled_at ELSE NULL END, updated_at = now() RETURNING id::text, role`, email, displayName).Scan(&userID, &role)
	if err != nil {
		return Invitation{}, "", fmt.Errorf("create pending member: %w", err)
	}
	if role == "owner" {
		return Invitation{}, "", ErrOwnerProtected
	}
	if _, err := tx.Exec(ctx, `DELETE FROM invitations WHERE email = $1 AND accepted_at IS NULL`, email); err != nil {
		return Invitation{}, "", fmt.Errorf("replace invitation: %w", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO invitations (email, role, token_hash, expires_at, invited_user_id) VALUES ($1,'member',$2,$3,$4) RETURNING id::text`, email, hash, invitation.ExpiresAt, userID).Scan(&invitation.ID); err != nil {
		return Invitation{}, "", fmt.Errorf("create invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Invitation{}, "", fmt.Errorf("commit member invitation: %w", err)
	}
	return invitation, raw, nil
}

func (s *Store) AcceptMemberInvitation(ctx context.Context, rawToken, password string) (User, error) {
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var user User
	err = tx.QueryRow(ctx, `SELECT u.id::text, u.email::text, u.display_name, u.role FROM invitations i JOIN users u ON u.id = i.invited_user_id WHERE i.token_hash = $1 AND i.accepted_at IS NULL AND i.expires_at > now() FOR UPDATE`, auth.HashToken(rawToken)).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvitationNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load invitation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, state='active', activated_at=now(), disabled_at=NULL, updated_at=now() WHERE id=$1`, user.ID, passwordHash); err != nil {
		return User{}, fmt.Errorf("activate invited member: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at=now() WHERE token_hash=$1`, auth.HashToken(rawToken)); err != nil {
		return User{}, fmt.Errorf("accept invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	user.State = "active"
	return user, nil
}

func (s *Store) DisableMember(ctx context.Context, id string) error {
	result, err := s.pool.Exec(ctx, `UPDATE users SET state='disabled', disabled_at=now(), updated_at=now() WHERE id=$1 AND role='member'`, id)
	if err != nil {
		return fmt.Errorf("disable member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrMemberNotFound
	}
	return nil
}

func (s *Store) TransferOwnership(ctx context.Context, nextOwnerID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin ownership transfer: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM users WHERE id=$1 AND role='member' FOR UPDATE`, nextOwnerID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("load new owner: %w", err)
	}
	if state != "active" {
		return errors.New("the new owner must be active")
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET role='member', updated_at=now() WHERE role='owner'`); err != nil {
		return fmt.Errorf("demote current owner: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET role='owner', updated_at=now() WHERE id=$1`, nextOwnerID); err != nil {
		return fmt.Errorf("promote new owner: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ownership transfer: %w", err)
	}
	return nil
}

func (s *Store) DeliverAnnouncement(ctx context.Context, id, title, body, category, target string) error {
	if id == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "" || (category != "planned_downtime" && category != "service_notice") {
		return errors.New("invalid platform announcement")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin announcement delivery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO platform_announcements (id,title,body,category,published_at) VALUES ($1,$2,$3,$4,now()) ON CONFLICT (id) DO NOTHING`, id, strings.TrimSpace(title), strings.TrimSpace(body), category); err != nil {
		return fmt.Errorf("store announcement: %w", err)
	}
	query := `INSERT INTO platform_announcement_recipients (announcement_id,user_id) SELECT $1,id FROM users WHERE state='active'`
	if target == "owners" {
		query += ` AND role='owner'`
	} else if target == "tenant_members" {
		query += ` AND role='member'`
	} else if target != "all_users" {
		return errors.New("invalid announcement target")
	}
	query += ` ON CONFLICT DO NOTHING`
	if _, err = tx.Exec(ctx, query, id); err != nil {
		return fmt.Errorf("create announcement recipients: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit announcement delivery: %w", err)
	}
	return nil
}

func (s *Store) ListAnnouncements(ctx context.Context, userID string) ([]Announcement, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.title,a.body,a.category,a.published_at,r.read_at FROM platform_announcement_recipients r JOIN platform_announcements a ON a.id=r.announcement_id WHERE r.user_id=$1 ORDER BY a.published_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list announcements: %w", err)
	}
	defer rows.Close()
	var result []Announcement
	for rows.Next() {
		var item Announcement
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Category, &item.PublishedAt, &item.ReadAt); err != nil {
			return nil, fmt.Errorf("scan announcement: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MarkAnnouncementRead(ctx context.Context, userID, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE platform_announcement_recipients SET read_at=COALESCE(read_at,now()) WHERE announcement_id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return fmt.Errorf("mark announcement read: %w", err)
	}
	return nil
}

func (s *Store) ListMessengerMessages(ctx context.Context, channel string) ([]MessengerMessage, error) {
	if channel != "team" && channel != "support" {
		return nil, errors.New("invalid messenger channel")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, channel, sender_name, body, created_at
		FROM messenger_messages WHERE channel = $1
		ORDER BY created_at DESC LIMIT 100
	`, channel)
	if err != nil {
		return nil, fmt.Errorf("list messenger messages: %w", err)
	}
	defer rows.Close()
	items := make([]MessengerMessage, 0)
	for rows.Next() {
		var item MessengerMessage
		if err := rows.Scan(&item.ID, &item.Channel, &item.SenderName, &item.Body, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan messenger message: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messenger messages: %w", err)
	}
	// The query is newest-first for its index; the client renders chronologically.
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

func (s *Store) CreateMessengerMessage(ctx context.Context, channel string, user User, body string) (MessengerMessage, error) {
	if channel != "team" && channel != "support" {
		return MessengerMessage{}, errors.New("invalid messenger channel")
	}
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 4000 {
		return MessengerMessage{}, errors.New("a message of 1-4000 characters is required")
	}
	var item MessengerMessage
	err := s.pool.QueryRow(ctx, `
		INSERT INTO messenger_messages (channel, sender_user_id, sender_name, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, channel, sender_name, body, created_at
	`, channel, user.ID, user.DisplayName, body).Scan(&item.ID, &item.Channel, &item.SenderName, &item.Body, &item.CreatedAt)
	if err != nil {
		return MessengerMessage{}, fmt.Errorf("create messenger message: %w", err)
	}
	return item, nil
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
