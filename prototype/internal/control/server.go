package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
)

type Server struct {
	logger         *slog.Logger
	pool           *pgxpool.Pool
	store          *Store
	adminTokenHash [sha256.Size]byte
	adminToken     string
}

func NewServer(logger *slog.Logger, pool *pgxpool.Pool, adminToken string) *Server {
	return &Server{logger: logger, pool: pool, store: NewStore(pool), adminToken: adminToken, adminTokenHash: sha256.Sum256([]byte(adminToken))}
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", s.health)
	router.Get("/", s.home)
	router.Route("/api/tenants", func(router chi.Router) {
		router.Use(s.requireAdminToken)
		router.Get("/", s.listTenants)
		router.Post("/", s.createTenant)
		router.Put("/{tenantID}/runtime", s.setRuntime)
		router.Post("/{tenantID}/owner", s.transferOwner)
	})
	router.Route("/api/announcements", func(router chi.Router) { router.Use(s.requireAdminToken); router.Post("/", s.createAnnouncement) })

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.SecurityHeaders,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
	)
}

func (s *Server) transferOwner(w http.ResponseWriter, r *http.Request) {
	tenantID, err := domain.ParseTenantID(chi.URLParam(r, "tenantID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_tenant_id", "Tenant ID is invalid.")
		return
	}
	var input struct {
		UserID string `json:"user_id"`
	}
	if decodeJSON(w, r, &input) != nil {
		return
	}
	tenant, err := s.store.TenantByID(r.Context(), tenantID)
	if errors.Is(err, ErrTenantNotFound) {
		httpx.WriteProblem(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found.")
		return
	}
	if err != nil {
		httpx.WriteProblem(w, http.StatusInternalServerError, "tenant_load_failed", "Tenant could not be loaded.")
		return
	}
	if err := s.platformRequest(r, tenant, "/internal/platform/ownership", map[string]string{"user_id": input.UserID}); err != nil {
		httpx.WriteProblem(w, http.StatusBadGateway, "ownership_transfer_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Category string `json:"category"`
		Target   string `json:"target"`
		TenantID string `json:"tenant_id"`
	}
	if decodeJSON(w, r, &input) != nil {
		return
	}
	announcement, err := s.store.CreatePlatformAnnouncement(r.Context(), input.Title, input.Body, input.Category, input.Target, input.TenantID, "platform_administrator")
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_announcement", err.Error())
		return
	}
	tenants, err := s.store.AnnouncementTenants(r.Context(), announcement)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "announcement_targets_unavailable", err.Error())
		return
	}
	for _, tenant := range tenants {
		target := announcement.Target
		if target == "all_users" {
			target = "all_users"
		}
		if err := s.platformRequest(r, tenant, "/internal/platform/announcements", map[string]string{"id": announcement.ID, "title": announcement.Title, "body": announcement.Body, "category": announcement.Category, "target": target}); err != nil {
			httpx.WriteProblem(w, http.StatusBadGateway, "announcement_delivery_failed", "Announcement was recorded but could not be delivered to "+tenant.Slug+": "+err.Error())
			return
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, announcement)
}

func (s *Server) platformRequest(r *http.Request, tenant Tenant, path string, payload any) error {
	base, err := url.Parse(tenant.InternalURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return errors.New("tenant runtime is unavailable")
	}
	endpoint, err := base.Parse(path)
	if err != nil {
		return errors.New("tenant maintenance endpoint is invalid")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+s.adminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("tenant returned %s", response.Status)
	}
	return nil
}

func (s *Server) requireAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		actual := sha256.Sum256([]byte(value))
		if value == "" || subtle.ConstantTimeCompare(actual[:], s.adminTokenHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mainspring-control"`)
			httpx.WriteProblem(w, http.StatusUnauthorized, "authentication_required", "A control-plane administration token is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "database_unavailable", "The control database is unavailable.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "control"})
}

func (s *Server) home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Mainspring Engine</title></head><body><main><h1>Mainspring Engine</h1><p>The control plane is running.</p></main></body></html>`)
}

func (s *Server) listTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := s.store.ListTenants(r.Context())
	if err != nil {
		s.logger.Error("list tenants", "request_id", httpx.RequestIDFromContext(r.Context()), "error", err)
		httpx.WriteProblem(w, http.StatusInternalServerError, "list_failed", "Tenants could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Slug        string `json:"slug"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return
	}

	tenant, err := s.store.CreateTenant(r.Context(), input.DisplayName, input.Slug)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			httpx.WriteProblem(w, http.StatusConflict, "slug_taken", "That boardroom address is already in use.")
			return
		}
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_tenant", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, tenant)
}

func (s *Server) setRuntime(w http.ResponseWriter, r *http.Request) {
	tenantID, err := domain.ParseTenantID(chi.URLParam(r, "tenantID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_tenant_id", "Tenant ID is invalid.")
		return
	}
	var input struct {
		InternalURL string `json:"internal_url"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return
	}
	if !strings.HasPrefix(input.InternalURL, "http://") && !strings.HasPrefix(input.InternalURL, "https://") {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_internal_url", "Internal URL must use HTTP or HTTPS.")
		return
	}
	if err := s.store.SetRuntime(r.Context(), tenantID, input.InternalURL); err != nil {
		if errors.Is(err, ErrTenantNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found.")
			return
		}
		httpx.WriteProblem(w, http.StatusInternalServerError, "runtime_update_failed", "Tenant runtime could not be updated.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_json", "Request body must be valid JSON with known fields.")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object.")
		return errors.New("multiple JSON values")
	}
	return nil
}
