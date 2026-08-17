package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

type Server struct {
	registrations  *registration.Service
	catalog        func() catalog.PublishedCatalog
	verification   *memory.VerificationSink
	exposeDevToken bool
	logger         *slog.Logger
}

func NewServer(registrations *registration.Service, catalogSource func() catalog.PublishedCatalog, verification *memory.VerificationSink, exposeDevToken bool, logger *slog.Logger) *Server {
	return &Server{registrations: registrations, catalog: catalogSource, verification: verification, exposeDevToken: exposeDevToken, logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /api/v1/catalog/public", s.publicCatalog)
	mux.HandleFunc("POST /api/v1/registrations", s.beginRegistration)
	mux.HandleFunc("POST /api/v1/registrations/verify", s.completeRegistration)
	return s.securityHeaders(s.recoverPanics(s.requestLog(mux)))
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) publicCatalog(w http.ResponseWriter, _ *http.Request) {
	catalog := s.catalog()
	offers := make([]catalogOffer, 0, len(catalog.Offers))
	for _, offer := range catalog.Offers {
		if !offer.Published {
			continue
		}
		offers = append(offers, catalogOffer{Code: offer.Code, PlanCode: offer.PlanCode, PlanVersion: offer.PlanVersion, Currency: offer.Currency, AmountMinor: offer.AmountMinor, BillingInterval: offer.BillingInterval, EffectiveFrom: offer.EffectiveFrom})
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": catalog.Version, "published_at": catalog.PublishedAt, "packages": catalog.Packages, "plans": catalog.Plans, "offers": offers})
}

type catalogOffer struct {
	Code            string    `json:"code"`
	PlanCode        string    `json:"plan_code"`
	PlanVersion     uint64    `json:"plan_version"`
	Currency        string    `json:"currency"`
	AmountMinor     int64     `json:"amount_minor"`
	BillingInterval string    `json:"billing_interval"`
	EffectiveFrom   time.Time `json:"effective_from"`
}

func (s *Server) beginRegistration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		AccountName string `json:"account_name"`
		Region      string `json:"region"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.registrations.Begin(r.Context(), registration.BeginCommand{Email: input.Email, DisplayName: input.DisplayName, AccountName: input.AccountName, Region: input.Region})
	if err != nil {
		s.writeRegistrationError(w, err)
		return
	}
	response := map[string]any{"registration_id": result.RegistrationID, "expires_at": result.ExpiresAt, "status": "verification_required"}
	if s.exposeDevToken {
		if message, ok := s.verification.Latest(); ok && message.RegistrationID == result.RegistrationID {
			response["development_verification_token"] = message.Token
		}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) completeRegistration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Token) == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	result, err := s.registrations.Complete(r.Context(), registration.CompleteCommand{Token: input.Token})
	if err != nil {
		s.writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": map[string]any{"id": result.User.ID, "email": result.User.PrimaryEmail, "display_name": result.User.DisplayName, "state": result.User.State}, "account": map[string]any{"id": result.Account.ID, "slug": result.Account.Slug, "display_name": result.Account.DisplayName, "type": result.Account.Type, "state": result.Account.State, "cell_id": result.Account.CellID, "placement_generation": result.Account.PlacementGeneration}, "membership": map[string]any{"id": result.Membership.ID, "role": result.Membership.Role, "state": result.Membership.State}, "entitlements": result.Snapshot})
}

func (s *Server) writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registration.ErrEmailExists):
		writeProblem(w, http.StatusConflict, "registration_conflict", "a registration already exists for this email")
	case errors.Is(err, registration.ErrRegistrationExpired):
		writeProblem(w, http.StatusGone, "registration_expired", "the verification link has expired")
	case errors.Is(err, registration.ErrRegistrationConsumed):
		writeProblem(w, http.StatusConflict, "registration_consumed", "the verification link has already been used")
	case errors.Is(err, registration.ErrRegistrationNotFound):
		writeProblem(w, http.StatusNotFound, "registration_not_found", "the verification link is invalid")
	default:
		writeProblem(w, http.StatusBadRequest, "registration_invalid", err.Error())
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body must be valid JSON with known fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("http panic", "recovered", recovered)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
