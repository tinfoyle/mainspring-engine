package control

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])$`)

var ErrTenantNotFound = errors.New("tenant not found")

type TenantStatus string

const (
	TenantPending      TenantStatus = "pending"
	TenantProvisioning TenantStatus = "provisioning"
	TenantReady        TenantStatus = "ready"
	TenantSuspended    TenantStatus = "suspended"
	TenantFailed       TenantStatus = "failed"
)

type Tenant struct {
	ID            domain.TenantID `json:"id"`
	Slug          string          `json:"slug"`
	DisplayName   string          `json:"display_name"`
	Status        TenantStatus    `json:"status"`
	InternalURL   string          `json:"internal_url,omitempty"`
	DatabaseName  string          `json:"database_name"`
	RuntimeHostID *string         `json:"runtime_host_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	ReadyAt       *time.Time      `json:"ready_at,omitempty"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func NormalizeSlug(value string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(value))
	if !slugPattern.MatchString(slug) {
		return "", errors.New("slug must be 3-63 lowercase letters, numbers, or hyphens and cannot start or end with a hyphen")
	}
	switch slug {
	case "www", "account", "admin", "api", "status", "support":
		return "", errors.New("slug is reserved")
	}
	return slug, nil
}

func (s *Store) CreateTenant(ctx context.Context, displayName, requestedSlug string) (Tenant, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Tenant{}, errors.New("display name is required")
	}
	slug, err := NormalizeSlug(requestedSlug)
	if err != nil {
		return Tenant{}, err
	}

	id := domain.NewTenantID()
	databaseName := "tenant_" + strings.ReplaceAll(id.String(), "-", "")
	tenant := Tenant{
		ID:           id,
		Slug:         slug,
		DisplayName:  displayName,
		Status:       TenantPending,
		DatabaseName: databaseName,
	}

	err = s.pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slug, display_name, database_name)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at
	`, id.String(), slug, displayName, databaseName).Scan(&tenant.CreatedAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}

	return tenant, nil
}

// RegisterTenant is used by the trusted provisioner after it has allocated an
// immutable ID and runtime. It is deliberately not exposed as a public API.
func (s *Store) RegisterTenant(ctx context.Context, id domain.TenantID, displayName, requestedSlug, internalURL string) (Tenant, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Tenant{}, errors.New("display name is required")
	}
	slug, err := NormalizeSlug(requestedSlug)
	if err != nil {
		return Tenant{}, err
	}
	if !strings.HasPrefix(internalURL, "http://") && !strings.HasPrefix(internalURL, "https://") {
		return Tenant{}, errors.New("internal URL must use HTTP or HTTPS")
	}
	databaseName := "tenant_" + strings.ReplaceAll(id.String(), "-", "")
	tenant := Tenant{
		ID: id, Slug: slug, DisplayName: displayName, Status: TenantReady,
		InternalURL: strings.TrimSpace(internalURL), DatabaseName: databaseName,
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slug, display_name, status, internal_url, database_name, ready_at)
		VALUES ($1, $2, $3, 'ready', $4, $5, now())
		ON CONFLICT (id) DO UPDATE
		SET slug = EXCLUDED.slug, display_name = EXCLUDED.display_name, status = 'ready',
		    internal_url = EXCLUDED.internal_url, database_name = EXCLUDED.database_name,
		    ready_at = COALESCE(tenants.ready_at, now()), updated_at = now()
		RETURNING created_at, ready_at
	`, id.String(), slug, displayName, internalURL, databaseName).Scan(&tenant.CreatedAt, &tenant.ReadyAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("register tenant: %w", err)
	}
	return tenant, nil
}

func (s *Store) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, slug::text, display_name, status, COALESCE(internal_url, ''),
		       database_name, runtime_host_id::text, created_at, ready_at
		FROM tenants
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []Tenant
	for rows.Next() {
		var tenant Tenant
		var id string
		if err := rows.Scan(
			&id, &tenant.Slug, &tenant.DisplayName, &tenant.Status, &tenant.InternalURL,
			&tenant.DatabaseName, &tenant.RuntimeHostID, &tenant.CreatedAt, &tenant.ReadyAt,
		); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenant.ID, err = domain.ParseTenantID(id)
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenants: %w", err)
	}
	return tenants, nil
}

func (s *Store) ResolveRoute(ctx context.Context, slug string) (Tenant, error) {
	slug, err := NormalizeSlug(slug)
	if err != nil {
		return Tenant{}, ErrTenantNotFound
	}

	var tenant Tenant
	var id string
	err = s.pool.QueryRow(ctx, `
		SELECT id::text, slug::text, display_name, status, COALESCE(internal_url, ''),
		       database_name, runtime_host_id::text, created_at, ready_at
		FROM tenants
		WHERE slug = $1
	`, slug).Scan(
		&id, &tenant.Slug, &tenant.DisplayName, &tenant.Status, &tenant.InternalURL,
		&tenant.DatabaseName, &tenant.RuntimeHostID, &tenant.CreatedAt, &tenant.ReadyAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrTenantNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("resolve tenant route: %w", err)
	}
	tenant.ID, err = domain.ParseTenantID(id)
	if err != nil {
		return Tenant{}, err
	}
	return tenant, nil
}

func (s *Store) SetRuntime(ctx context.Context, tenantID domain.TenantID, internalURL string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE tenants
		SET status = 'ready', internal_url = $2, ready_at = COALESCE(ready_at, now()), updated_at = now()
		WHERE id = $1
	`, tenantID.String(), strings.TrimSpace(internalURL))
	if err != nil {
		return fmt.Errorf("set tenant runtime: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrTenantNotFound
	}
	return nil
}
