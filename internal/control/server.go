package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
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
}

func NewServer(logger *slog.Logger, pool *pgxpool.Pool, adminToken string) *Server {
	return &Server{logger: logger, pool: pool, store: NewStore(pool), adminTokenHash: sha256.Sum256([]byte(adminToken))}
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
	})

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.SecurityHeaders,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
	)
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
