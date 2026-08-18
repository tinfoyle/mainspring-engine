// Package admissionapi exposes a private, route-proof-bound capacity broker.
// It owns no cell data and accepts no browser session or bearer credential.
package admissionapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const DefaultMaxBody = int64(64 << 10)

type Usage interface {
	Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error)
	Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error)
}

type Verifier interface {
	Verify(string, routecontext.Binding) (routecontext.Claims, error)
}

type Server struct {
	usage     Usage
	verifiers map[ids.CellID]Verifier
	logger    *slog.Logger
	maxBody   int64
}

func New(usage Usage, verifiers map[ids.CellID]Verifier, logger *slog.Logger, maxBody int64) (*Server, error) {
	if usage == nil || len(verifiers) == 0 || logger == nil || maxBody <= 0 || maxBody > 1<<20 {
		return nil, errors.New("admission API dependencies and bounded body size are required")
	}
	copyVerifiers := make(map[ids.CellID]Verifier, len(verifiers))
	for cellID, verifier := range verifiers {
		if !routecontext.ValidCellID(cellID) || verifier == nil {
			return nil, errors.New("admission API cell verifier is invalid")
		}
		copyVerifiers[cellID] = verifier
	}
	return &Server{usage: usage, verifiers: copyVerifiers, logger: logger, maxBody: maxBody}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/work/capacity/reserve", s.reserve)
	mux.HandleFunc("POST /internal/v1/work/capacity/release", s.release)
	return s.recover(s.securityHeaders(mux))
}

type capacityRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	RequestID    string               `json:"request_id"`
	RouteContext string               `json:"route_context"`
	Binding      routecontext.Binding `json:"binding"`
}

type capacityResponse struct {
	RequestID    string                          `json:"request_id"`
	State        usageadmission.ReservationState `json:"state"`
	Current      int64                           `json:"current"`
	Maximum      int64                           `json:"maximum"`
	ExpiresAt    *time.Time                      `json:"expires_at,omitempty"`
	NewlyCreated bool                            `json:"newly_created"`
}

func (s *Server) reserve(w http.ResponseWriter, r *http.Request) {
	request, claims, actor, ok := s.accept(w, r)
	if !ok {
		return
	}
	reservation, err := s.usage.Reserve(r.Context(), usageadmission.ReserveCommand{Actor: actor, AccountID: claims.Authority.AccountID, PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: request.RequestID})
	if err != nil {
		s.writeUsageError(w, "reserve", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityView(reservation))
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	request, claims, actor, ok := s.accept(w, r)
	if !ok {
		return
	}
	reservation, err := s.usage.Release(r.Context(), usageadmission.ReleaseCommand{Actor: actor, AccountID: claims.Authority.AccountID, RequestID: request.RequestID})
	if err != nil {
		s.writeUsageError(w, "release", err)
		return
	}
	writeJSON(w, http.StatusOK, capacityView(reservation))
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (capacityRequest, routecontext.Claims, access.Actor, bool) {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "json_required", "capacity admission requires application/json")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody))
	decoder.DisallowUnknownFields()
	var request capacityRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_admission_request", "the capacity request is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_admission_request", "the capacity request is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	verifier, exists := s.verifiers[request.CellID]
	if !exists || ids.Validate(request.RequestID) != nil || request.RouteContext == "" {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	claims, err := verifier.Verify(request.RouteContext, request.Binding)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "invalid_route_proof", "the routed operation proof is invalid")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	if claims.Authority.CellID != request.CellID || claims.Authority.OperationID != request.RequestID || claims.Authority.ActorKind != "user" || !workMutationBinding(claims) || claims.Authority.PackageAccess == nil || claims.Authority.PackageAccess.Code != string(catalog.PackageWork) || claims.Authority.PackageAccess.Mode != string(catalog.ModeEnabled) {
		writeProblem(w, http.StatusForbidden, "admission_scope_denied", "the routed operation cannot manage Work capacity")
		return capacityRequest{}, routecontext.Claims{}, access.Actor{}, false
	}
	return request, claims, access.Actor{UserID: ids.UserID(claims.Authority.ActorID)}, true
}

func workMutationBinding(claims routecontext.Claims) bool {
	binding, accountID := claims.Binding, claims.Authority.AccountID
	base := "/api/v1/accounts/" + string(accountID) + "/work-items"
	if binding.Method != http.MethodPost {
		return false
	}
	if binding.Target == base {
		return true
	}
	remaining, found := strings.CutPrefix(binding.Target, base+"/")
	if !found {
		return false
	}
	itemID, action, found := strings.Cut(remaining, "/")
	return found && action == "transitions" && ids.Validate(itemID) == nil
}

func capacityView(reservation usageadmission.Reservation) capacityResponse {
	return capacityResponse{RequestID: reservation.RequestID, State: reservation.State, Current: reservation.Current, Maximum: reservation.Maximum, ExpiresAt: reservation.ExpiresAt, NewlyCreated: reservation.NewlyCreated}
}

func (s *Server) writeUsageError(w http.ResponseWriter, operation string, err error) {
	var denied *access.DeniedError
	switch {
	case errors.As(err, &denied):
		writeProblemFields(w, http.StatusForbidden, string(denied.Code), "current Account access does not admit this Work operation", denied.Current, denied.Maximum)
	case errors.Is(err, usageadmission.ErrInvalidRequest):
		writeProblem(w, http.StatusBadRequest, "invalid_usage_request", "the capacity request is invalid")
	case errors.Is(err, usageadmission.ErrReservationConflict):
		writeProblem(w, http.StatusConflict, "reservation_conflict", "the operation key belongs to different capacity work")
	case errors.Is(err, usageadmission.ErrReservationClosed):
		writeProblem(w, http.StatusConflict, "reservation_closed", "the operation key was already finalized")
	case errors.Is(err, usageadmission.ErrEntitlementChanged):
		writeProblem(w, http.StatusConflict, "entitlement_changed", "Account access changed; retry with fresh routing")
	default:
		s.logger.Error("Work capacity admission failed", "operation", operation, "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "admission_unavailable", "Work capacity admission is temporarily unavailable")
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("admission API panic", "value", value)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "capacity admission could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	writeProblemFields(w, status, code, detail, 0, 0)
}

func writeProblemFields(w http.ResponseWriter, status int, code, detail string, current, maximum int64) {
	value := map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail}
	if current != 0 {
		value["current"] = current
	}
	if maximum != 0 {
		value["maximum"] = maximum
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
