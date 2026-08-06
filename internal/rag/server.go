package rag

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
)

type Server struct {
	logger    *slog.Logger
	store     *Store
	tenantID  domain.TenantID
	tokenHash [sha256.Size]byte
}

func NewServer(logger *slog.Logger, store *Store, tenantID domain.TenantID, token string) (*Server, error) {
	if len(token) < 32 {
		return nil, errors.New("MAINSPRING_RAG_TOKEN must contain at least 32 bytes")
	}
	return &Server{logger: logger, store: store, tenantID: tenantID, tokenHash: sha256.Sum256([]byte(token))}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "rag", "tenant_id": s.tenantID.String()})
	})
	router.Group(func(router chi.Router) {
		router.Use(s.authorize)
		router.Post("/documents/text", s.ingestText)
		router.Get("/search", s.search)
	})
	return httpx.Chain(router, httpx.RequestID, httpx.Recover(s.logger), httpx.AccessLog(s.logger))
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		actual := sha256.Sum256([]byte(value))
		if value == "" || subtle.ConstantTimeCompare(actual[:], s.tokenHash[:]) != 1 || r.Header.Get("X-Mainspring-Tenant-ID") != s.tenantID.String() {
			httpx.WriteProblem(w, http.StatusUnauthorized, "authentication_required", "Valid tenant RAG credentials are required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ingestText(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		MediaType string `json:"media_type"`
		Content   string `json:"content"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		return
	}
	document, err := s.store.IngestText(r.Context(), input.Name, input.MediaType, input.Content)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "ingest_failed", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, document)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := s.store.Search(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "search_failed", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
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
