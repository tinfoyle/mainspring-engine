package cellapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type WorkQueries interface {
	Get(context.Context, access.Actor, ids.AccountID, ids.WorkItemID) (workdomain.Item, error)
	List(context.Context, access.Actor, ids.AccountID, workapp.ListQuery) (workapp.Page, error)
	Children(context.Context, access.Actor, ids.AccountID, ids.WorkItemID, int) ([]workdomain.Item, error)
	Summary(context.Context, access.Actor, ids.AccountID) (workapp.Summary, error)
}

func (s *Server) workList(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.workRequest(w, r)
	if !ok {
		return
	}
	query, err := parseWorkListQuery(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_work_query", "Work filters or cursor are invalid")
		return
	}
	page, err := s.work.List(routecontext.WithClaims(r.Context(), claims), actor, accountID, query)
	if err != nil {
		s.writeWorkError(w, err)
		return
	}
	items := make([]workItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, workItemView(item))
	}
	response := workPageResponse{Items: items}
	if page.NextCursor != nil {
		response.NextCursor, err = encodeWorkCursor(*page.NextCursor)
		if err != nil {
			s.logger.Error("encode Work cursor", "error", err)
			writeProblem(w, http.StatusInternalServerError, "internal_error", "the Work page could not be encoded")
			return
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) workSummary(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.workRequest(w, r)
	if !ok {
		return
	}
	summary, err := s.work.Summary(routecontext.WithClaims(r.Context(), claims), actor, accountID)
	if err != nil {
		s.writeWorkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workSummaryResponse{Active: summary.Active, InProgress: summary.InProgress, Waiting: summary.Waiting, Urgent: summary.Urgent, Done: summary.Done})
}

func (s *Server) workItem(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.workRequest(w, r)
	if !ok {
		return
	}
	itemID := ids.WorkItemID(r.PathValue("itemID"))
	if len(r.URL.Query()) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_work_query", "Work item query is invalid")
		return
	}
	if ids.Validate(string(itemID)) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_work_item", "Work item ID is invalid")
		return
	}
	item, err := s.work.Get(routecontext.WithClaims(r.Context(), claims), actor, accountID, itemID)
	if err != nil {
		s.writeWorkError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, item.Version))
	writeJSON(w, http.StatusOK, workItemView(item))
}

func (s *Server) workChildren(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.workRequest(w, r)
	if !ok {
		return
	}
	itemID := ids.WorkItemID(r.PathValue("itemID"))
	limit, err := parseLimit(r, 50)
	if ids.Validate(string(itemID)) != nil || err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_work_query", "Work child query is invalid")
		return
	}
	items, err := s.work.Children(routecontext.WithClaims(r.Context(), claims), actor, accountID, itemID, limit)
	if err != nil {
		s.writeWorkError(w, err)
		return
	}
	views := make([]workItemResponse, 0, len(items))
	for _, item := range items {
		views = append(views, workItemView(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) workRequest(w http.ResponseWriter, r *http.Request) (routecontext.Claims, access.Actor, ids.AccountID, bool) {
	claims, ok := s.accept(w, r)
	if !ok {
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	if s.work == nil {
		writeProblem(w, http.StatusServiceUnavailable, "work_unavailable", "Work queries are not available in this cell")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	accountID := ids.AccountID(r.PathValue("accountID"))
	if accountID != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return routecontext.Claims{}, access.Actor{}, "", false
	}
	actor := access.Actor{}
	if claims.Authority.ActorKind == "user" {
		actor.UserID = ids.UserID(claims.Authority.ActorID)
	} else {
		actor.WorkloadID = claims.Authority.ActorID
	}
	return claims, actor, accountID, true
}

func parseWorkListQuery(r *http.Request) (workapp.ListQuery, error) {
	values := r.URL.Query()
	for key := range values {
		if key != "state" && key != "kind" && key != "q" && key != "cursor" && key != "limit" {
			return workapp.ListQuery{}, errors.New("unknown query field")
		}
	}
	if len(values["q"]) > 1 || len(values["cursor"]) > 1 || len(values["limit"]) > 1 {
		return workapp.ListQuery{}, errors.New("duplicate scalar query field")
	}
	query := workapp.ListQuery{Search: values.Get("q")}
	for _, value := range values["state"] {
		query.States = append(query.States, workdomain.State(value))
	}
	for _, value := range values["kind"] {
		query.Kinds = append(query.Kinds, workdomain.Kind(value))
	}
	limit, err := parseLimit(r, 50)
	if err != nil {
		return workapp.ListQuery{}, err
	}
	query.Limit = limit
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeWorkCursor(raw)
		if err != nil {
			return workapp.ListQuery{}, err
		}
		query.AfterUpdatedAt, query.AfterID = &cursor.UpdatedAt, cursor.ID
	}
	return query, nil
}

func parseLimit(r *http.Request, fallback int) (int, error) {
	values := r.URL.Query()["limit"]
	if len(values) > 1 {
		return 0, workapp.ErrInvalidCommand
	}
	raw := ""
	if len(values) == 1 {
		raw = values[0]
	}
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > workapp.MaxPageSize {
		return 0, workapp.ErrInvalidCommand
	}
	return value, nil
}

type cursorEnvelope struct {
	Version   int            `json:"v"`
	UpdatedAt time.Time      `json:"updated_at"`
	ID        ids.WorkItemID `json:"id"`
}

func encodeWorkCursor(cursor workapp.Cursor) (string, error) {
	if cursor.UpdatedAt.IsZero() || ids.Validate(string(cursor.ID)) != nil {
		return "", workapp.ErrInvalidCommand
	}
	raw, err := json.Marshal(cursorEnvelope{Version: 1, UpdatedAt: cursor.UpdatedAt.UTC(), ID: cursor.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeWorkCursor(raw string) (workapp.Cursor, error) {
	if len(raw) > 1024 {
		return workapp.Cursor{}, workapp.ErrInvalidCommand
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return workapp.Cursor{}, workapp.ErrInvalidCommand
	}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	var value cursorEnvelope
	if err := decoder.Decode(&value); err != nil || value.Version != 1 || value.UpdatedAt.IsZero() || ids.Validate(string(value.ID)) != nil {
		return workapp.Cursor{}, workapp.ErrInvalidCommand
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return workapp.Cursor{}, workapp.ErrInvalidCommand
	}
	return workapp.Cursor{UpdatedAt: value.UpdatedAt.UTC(), ID: value.ID}, nil
}

func (s *Server) writeWorkError(w http.ResponseWriter, err error) {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, workapp.ErrInvalidCommand):
		writeProblem(w, http.StatusBadRequest, "invalid_work_query", "the Work query is invalid")
	case errors.Is(err, workapp.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "work_item_not_found", "the Work item was not found")
	case errors.As(err, &denied):
		writeProblem(w, http.StatusForbidden, string(denied.Code), "the Work package does not allow this query")
	default:
		s.logger.Error("query Work", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "work_unavailable", "Work could not be loaded")
	}
}

type workPageResponse struct {
	Items      []workItemResponse `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
type workSummaryResponse struct {
	Active     uint64 `json:"active"`
	InProgress uint64 `json:"in_progress"`
	Waiting    uint64 `json:"waiting"`
	Urgent     uint64 `json:"urgent"`
	Done       uint64 `json:"done"`
}
type workAssignmentResponse struct {
	Responsibility workdomain.Responsibility `json:"responsibility"`
	UserID         ids.UserID                `json:"user_id,omitempty"`
	PersonaID      string                    `json:"persona_id,omitempty"`
	ExternalRef    string                    `json:"external_ref,omitempty"`
}
type workProvenanceResponse struct {
	Source                workdomain.Source `json:"source"`
	CreatedBy             workActorResponse `json:"created_by"`
	BaselineRequirementID string            `json:"baseline_requirement_id,omitempty"`
	ScheduleID            string            `json:"schedule_id,omitempty"`
	ConversationID        string            `json:"conversation_id,omitempty"`
	RunID                 string            `json:"run_id,omitempty"`
}
type workActorResponse struct {
	Kind workdomain.ActorKind `json:"kind"`
	ID   string               `json:"id"`
}
type workItemResponse struct {
	ID          ids.WorkItemID         `json:"id"`
	Number      uint64                 `json:"number"`
	ParentID    ids.WorkItemID         `json:"parent_id,omitempty"`
	Depth       uint8                  `json:"depth"`
	Kind        workdomain.Kind        `json:"kind"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	State       workdomain.State       `json:"state"`
	Priority    workdomain.Priority    `json:"priority"`
	Assignment  workAssignmentResponse `json:"assignment"`
	Provenance  workProvenanceResponse `json:"provenance"`
	DueAt       *time.Time             `json:"due_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Version     uint64                 `json:"version"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

func workItemView(item workdomain.Item) workItemResponse {
	return workItemResponse{ID: item.ID, Number: item.Number, ParentID: item.ParentID, Depth: item.Depth, Kind: item.Kind, Title: item.Title, Description: item.Description, State: item.State, Priority: item.Priority, Assignment: workAssignmentResponse{Responsibility: item.Assignment.Responsibility, UserID: item.Assignment.UserID, PersonaID: item.Assignment.PersonaID, ExternalRef: item.Assignment.ExternalRef}, Provenance: workProvenanceResponse{Source: item.Provenance.Source, CreatedBy: workActorResponse{Kind: item.Provenance.CreatedBy.Kind, ID: item.Provenance.CreatedBy.ID}, BaselineRequirementID: item.Provenance.BaselineRequirementID, ScheduleID: item.Provenance.ScheduleID, ConversationID: item.Provenance.ConversationID, RunID: item.Provenance.RunID}, DueAt: item.DueAt, CompletedAt: item.CompletedAt, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
