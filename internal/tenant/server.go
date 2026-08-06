package tenant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tinfoyle/mainspring-engine/internal/auth"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
	"github.com/tinfoyle/mainspring-engine/internal/scheduling"
	"github.com/tinfoyle/mainspring-engine/web/assets"
	"github.com/tinfoyle/mainspring-engine/web/components"
)

const (
	sessionCookieName = "mainspring_session"
	sessionLifetime   = 7 * 24 * time.Hour
)

type contextKey string

const authContextKey contextKey = "tenant_auth"

type authenticatedSession struct {
	User     User
	RawToken string
}

type RunDispatcher interface {
	Dispatch(context.Context, domain.RunID) error
}

type ServerConfig struct {
	TenantID      domain.TenantID
	TenantSlug    string
	TenantName    string
	BaseDomain    string
	SessionSecret []byte
	SetupToken    string
	CookieSecure  bool
}

type Server struct {
	logger     *slog.Logger
	config     ServerConfig
	store      *Store
	boardrooms *boardroom.Store
	dispatcher RunDispatcher
	schedules  *scheduling.Service
}

func NewServer(logger *slog.Logger, config ServerConfig, store *Store, boardrooms *boardroom.Store, dispatcher RunDispatcher, schedules *scheduling.Service) (*Server, error) {
	if len(config.SessionSecret) < 32 {
		return nil, errors.New("MAINSPRING_SESSION_SECRET must contain at least 32 bytes")
	}
	if strings.TrimSpace(config.SetupToken) == "" {
		return nil, errors.New("MAINSPRING_SETUP_TOKEN is required")
	}
	return &Server{
		logger:     logger,
		config:     config,
		store:      store,
		boardrooms: boardrooms,
		dispatcher: dispatcher,
		schedules:  schedules,
	}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", s.health)
	router.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServerFS(assets.Files)))

	router.Group(func(router chi.Router) {
		router.Use(s.tenantBoundary)
		router.Use(s.rejectCrossSitePosts)
		router.Get("/setup", s.setupPage)
		router.Post("/setup", s.setup)
		router.Get("/login", s.loginPage)
		router.Post("/login", s.login)

		router.Group(func(router chi.Router) {
			router.Use(s.requireAuthentication)
			router.Get("/", s.dashboard)
			router.Get("/boardrooms/{boardroomID}", s.boardroomPage)
			router.Post("/boardrooms/{boardroomID}/runs", s.requireCSRF(s.createRun))
			router.Get("/runs/{runID}", s.runPage)
			router.Get("/runs/{runID}/events", s.runEvents)
			router.Get("/schedules", s.schedulesPage)
			router.Post("/schedules", s.requireCSRF(s.createSchedule))
			router.Post("/schedules/{scheduleID}/pause", s.requireCSRF(s.pauseSchedule))
			router.Post("/schedules/{scheduleID}/trigger", s.requireCSRF(s.triggerSchedule))
			router.Post("/schedules/{scheduleID}/delete", s.requireCSRF(s.deleteSchedule))
			router.Post("/logout", s.requireCSRF(s.logout))
		})
	})

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.SecurityHeaders,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
	)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": "tenant", "tenant_id": s.config.TenantID.String(),
	})
}

func (s *Server) tenantBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := normalizedHost(r.Host)
		expected := strings.ToLower(s.config.TenantSlug + "." + strings.Trim(s.config.BaseDomain, "."))
		if host != expected {
			httpx.WriteProblem(w, http.StatusMisdirectedRequest, "tenant_host_mismatch", "This request was sent to the wrong boardroom.")
			return
		}
		if forwardedTenant := r.Header.Get("X-Mainspring-Tenant-ID"); forwardedTenant != "" && forwardedTenant != s.config.TenantID.String() {
			httpx.WriteProblem(w, http.StatusMisdirectedRequest, "tenant_identity_mismatch", "The routed tenant identity does not match this boardroom.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rejectCrossSitePosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				httpx.WriteProblem(w, http.StatusForbidden, "cross_site_request", "Cross-site form submissions are not allowed.")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
					httpx.WriteProblem(w, http.StatusForbidden, "origin_mismatch", "Request origin does not match this boardroom.")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		user, err := s.store.UserBySession(r.Context(), cookie.Value)
		if errors.Is(err, ErrSessionNotFound) {
			s.clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			s.logger.Error("authenticate tenant session", "error", err)
			httpx.WriteProblem(w, http.StatusInternalServerError, "session_error", "The session could not be verified.")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey, authenticatedSession{User: user, RawToken: cookie.Value})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok {
			httpx.WriteProblem(w, http.StatusUnauthorized, "authentication_required", "Sign in is required.")
			return
		}
		if err := r.ParseForm(); err != nil || !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
			httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
			return
		}
		next(w, r)
	}
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.store.HasUsers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The boardroom setup state could not be loaded.")
		return
	}
	if hasUsers {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, components.SetupPage(s.config.TenantName, ""))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, components.SetupPage(s.config.TenantName, "The setup form could not be read."))
		return
	}
	expected := sha256.Sum256([]byte(s.config.SetupToken))
	actual := sha256.Sum256([]byte(r.FormValue("setup_token")))
	if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		s.render(w, http.StatusForbidden, components.SetupPage(s.config.TenantName, "The setup token is not valid."))
		return
	}
	user, err := s.store.CreateOwner(r.Context(), r.FormValue("email"), r.FormValue("display_name"), r.FormValue("password"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrSetupComplete) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		s.render(w, status, components.SetupPage(s.config.TenantName, err.Error()))
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.renderError(w, http.StatusInternalServerError, "The owner was created, but the session could not be started.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.store.HasUsers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The boardroom login state could not be loaded.")
		return
	}
	if !hasUsers {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, components.LoginPage(s.config.TenantName, ""))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, components.LoginPage(s.config.TenantName, "The login form could not be read."))
		return
	}
	user, err := s.store.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if errors.Is(err, ErrAuthenticationFailed) {
		s.render(w, http.StatusUnauthorized, components.LoginPage(s.config.TenantName, "The email or password is incorrect."))
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Sign in could not be completed.")
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Sign in could not be completed.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteSession(r.Context(), session.RawToken); err != nil {
		s.logger.Error("delete tenant session", "error", err)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardrooms could not be loaded.")
		return
	}
	views := make([]components.BoardroomCardView, 0, len(rooms))
	for _, room := range rooms {
		views = append(views, boardroomView(room))
	}
	s.render(w, http.StatusOK, components.DashboardPage(s.config.TenantName, userView(session.User), views, s.csrfToken(session)))
}

func (s *Server) boardroomPage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	roomID, err := domain.ParseBoardroomID(chi.URLParam(r, "boardroomID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	room, err := s.boardrooms.Get(r.Context(), roomID)
	if errors.Is(err, boardroom.ErrBoardroomNotFound) {
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom could not be loaded.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), roomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom personas could not be loaded.")
		return
	}
	s.render(w, http.StatusOK, components.BoardroomPage(
		s.config.TenantName, userView(session.User), boardroomView(room), personaViews(personas), nil, nil, s.csrfToken(session),
	))
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	roomID, err := domain.ParseBoardroomID(chi.URLParam(r, "boardroomID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" || len(prompt) > 12000 {
		s.renderError(w, http.StatusBadRequest, "The boardroom request must contain between 1 and 12,000 characters.")
		return
	}
	run, err := s.boardrooms.CreateRun(r.Context(), roomID, session.User.ID, prompt)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.dispatcher.Dispatch(r.Context(), run.ID); err != nil {
		_ = s.boardrooms.SetRunStatus(r.Context(), run.ID, domain.RunFailed, "The durable workflow could not be started.")
		s.logger.Error("dispatch boardroom run", "run_id", run.ID.String(), "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "The boardroom could not be started. Please try again.")
		return
	}
	http.Redirect(w, r, "/runs/"+run.ID.String(), http.StatusSeeOther)
}

func (s *Server) schedulesPage(w http.ResponseWriter, r *http.Request) {
	s.renderSchedules(w, r, http.StatusOK, "")
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can create schedules.")
		return
	}
	if s.schedules == nil {
		s.renderSchedules(w, r, http.StatusServiceUnavailable, "Durable scheduling requires Temporal orchestration.")
		return
	}
	boardroomID, err := domain.ParseBoardroomID(r.FormValue("boardroom_id"))
	if err != nil {
		s.renderSchedules(w, r, http.StatusBadRequest, "Choose a valid boardroom.")
		return
	}
	var every time.Duration
	cronExpression := strings.TrimSpace(r.FormValue("cron_expression"))
	if cronExpression == "" {
		count, err := strconv.Atoi(r.FormValue("every_number"))
		if err != nil || count <= 0 {
			s.renderSchedules(w, r, http.StatusBadRequest, "Enter a valid schedule interval.")
			return
		}
		unit := time.Hour
		switch r.FormValue("every_unit") {
		case "hours":
		case "days":
			unit = 24 * time.Hour
		case "weeks":
			unit = 7 * 24 * time.Hour
		default:
			s.renderSchedules(w, r, http.StatusBadRequest, "Choose hours, days, or weeks for the interval.")
			return
		}
		every = time.Duration(count) * unit
	}
	_, err = s.schedules.Create(r.Context(), scheduling.CreateInput{
		BoardroomID: boardroomID, Name: r.FormValue("name"), Prompt: r.FormValue("prompt"),
		Every: every, CronExpression: cronExpression, TimeZone: r.FormValue("time_zone"),
	})
	if err != nil {
		if errors.Is(err, scheduling.ErrInvalidInput) {
			s.renderSchedules(w, r, http.StatusBadRequest, strings.TrimPrefix(err.Error(), scheduling.ErrInvalidInput.Error()+": "))
			return
		}
		s.logger.Error("create boardroom schedule", "error", err)
		s.renderSchedules(w, r, http.StatusServiceUnavailable, "The durable schedule could not be created. Please try again.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) pauseSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can change schedules.")
		return
	}
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	paused := r.FormValue("paused") == "true"
	if err := s.schedules.SetPaused(r.Context(), id, paused); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("pause boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_update_failed", "Schedule could not be updated.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) triggerSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can run schedules.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	if err := s.schedules.Trigger(r.Context(), id); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("trigger boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_trigger_failed", "Schedule could not be started.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can delete schedules.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	if err := s.schedules.Delete(r.Context(), id); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("delete boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_delete_failed", "Schedule could not be deleted.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) renderSchedules(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardrooms could not be loaded.")
		return
	}
	var items []scheduling.Schedule
	if s.schedules != nil {
		items, err = s.schedules.List(r.Context())
		if err != nil {
			s.renderError(w, http.StatusInternalServerError, "Schedules could not be loaded.")
			return
		}
	} else if formError == "" {
		formError = "Durable scheduling requires Temporal orchestration."
	}
	s.render(w, status, components.SchedulesPage(
		s.config.TenantName, userView(session.User), boardroomViews(rooms), scheduleViews(items), s.csrfToken(session), formError,
	))
}

func scheduleAdmin(ctx context.Context) bool {
	session, ok := sessionFromContext(ctx)
	return ok && (session.User.Role == "owner" || session.User.Role == "admin")
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	runID, err := domain.ParseRunID(chi.URLParam(r, "runID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Boardroom run was not found.")
		return
	}
	run, err := s.boardrooms.GetRun(r.Context(), runID)
	if errors.Is(err, boardroom.ErrRunNotFound) {
		s.renderError(w, http.StatusNotFound, "Boardroom run was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom run could not be loaded.")
		return
	}
	room, err := s.boardrooms.Get(r.Context(), run.BoardroomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom could not be loaded.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), run.BoardroomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom personas could not be loaded.")
		return
	}
	messages, cursor, err := s.boardrooms.MessagesSnapshot(r.Context(), run.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom messages could not be loaded.")
		return
	}
	runView := componentRun(run, cursor)
	s.render(w, http.StatusOK, components.BoardroomPage(
		s.config.TenantName, userView(session.User), boardroomView(room), personaViews(personas), &runView, messageViews(messages), s.csrfToken(session),
	))
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	runID, err := domain.ParseRunID(chi.URLParam(r, "runID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "run_not_found", "Boardroom run was not found.")
		return
	}
	if _, err := s.boardrooms.GetRun(r.Context(), runID); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "run_not_found", "Boardroom run was not found.")
		return
	}
	after := parseEventCursor(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	poll := time.NewTicker(700 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		terminal, err := s.writePendingEvents(r.Context(), w, runID, &after)
		if err != nil {
			s.logger.Error("stream boardroom events", "run_id", runID.String(), "error", err)
			return
		}
		if terminal {
			writeSSE(w, after, "finished", "done")
			_ = controller.Flush()
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

func (s *Server) writePendingEvents(ctx context.Context, w http.ResponseWriter, runID domain.RunID, after *int64) (bool, error) {
	events, err := s.boardrooms.EventsAfter(ctx, runID, *after, 100)
	if err != nil {
		return false, err
	}
	terminal := false
	for _, event := range events {
		*after = event.ID
		switch event.Type {
		case "message.completed":
			var payload struct {
				MessageID   string    `json:"message_id"`
				Sequence    int64     `json:"sequence"`
				PersonaName string    `json:"persona_name"`
				PersonaRole string    `json:"persona_role"`
				Role        string    `json:"role"`
				Body        string    `json:"body"`
				CreatedAt   time.Time `json:"created_at"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return false, err
			}
			fragment, err := renderString(components.MessageBubble(components.MessageView{
				ID: payload.MessageID, Sequence: payload.Sequence, PersonaName: payload.PersonaName,
				PersonaRole: payload.PersonaRole, Role: payload.Role, Body: payload.Body, CreatedAt: payload.CreatedAt,
			}))
			if err != nil {
				return false, err
			}
			writeSSE(w, event.ID, "message", fragment)
		case "run.status":
			run, err := s.boardrooms.GetRun(ctx, runID)
			if err != nil {
				return false, err
			}
			fragment, err := renderString(components.StatusFragment(componentRun(run, event.ID)))
			if err != nil {
				return false, err
			}
			writeSSE(w, event.ID, "status", fragment)
			terminal = run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCanceled
		}
	}
	return terminal, nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user User) error {
	raw, expiresAt, err := s.store.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r), sessionLifetime)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: raw, Path: "/", Expires: expiresAt,
		MaxAge: int(time.Until(expiresAt).Seconds()), HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) csrfToken(session authenticatedSession) string {
	return auth.CSRFToken(session.RawToken, s.config.SessionSecret)
}

func (s *Server) render(w http.ResponseWriter, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(context.Background(), w); err != nil {
		s.logger.Error("render tenant page", "error", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, message string) {
	httpx.WriteProblem(w, status, "request_failed", message)
}

func sessionFromContext(ctx context.Context) (authenticatedSession, bool) {
	session, ok := ctx.Value(authContextKey).(authenticatedSession)
	return session, ok
}

func normalizedHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
}

func parseEventCursor(r *http.Request) int64 {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("after")
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	if parsed < 0 {
		return 0
	}
	return parsed
}

func writeSSE(w http.ResponseWriter, id int64, eventName, data string) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\n", id, eventName)
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
}

func renderString(component templ.Component) (string, error) {
	var buffer bytes.Buffer
	if err := component.Render(context.Background(), &buffer); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func userView(user User) components.UserView {
	return components.UserView{DisplayName: user.DisplayName, Email: user.Email, Role: user.Role}
}

func boardroomView(room boardroom.Summary) components.BoardroomCardView {
	return components.BoardroomCardView{
		ID: room.ID.String(), Name: room.Name, Description: room.Description,
		PersonaCount: room.PersonaCount, Status: room.Status,
	}
}

func boardroomViews(rooms []boardroom.Summary) []components.BoardroomCardView {
	result := make([]components.BoardroomCardView, 0, len(rooms))
	for _, room := range rooms {
		result = append(result, boardroomView(room))
	}
	return result
}

func scheduleViews(items []scheduling.Schedule) []components.ScheduleView {
	result := make([]components.ScheduleView, 0, len(items))
	for _, item := range items {
		timing := "Cron: " + item.Spec.CronExpression
		if item.Spec.Kind == "interval" {
			duration := time.Duration(item.Spec.EverySeconds) * time.Second
			switch {
			case duration%(7*24*time.Hour) == 0:
				timing = fmt.Sprintf("Every %d weeks", duration/(7*24*time.Hour))
			case duration%(24*time.Hour) == 0:
				timing = fmt.Sprintf("Every %d days", duration/(24*time.Hour))
			default:
				timing = fmt.Sprintf("Every %d hours", duration/time.Hour)
			}
		}
		result = append(result, components.ScheduleView{
			ID: item.ID, Name: item.Name, BoardroomName: item.BoardroomName, Prompt: item.Prompt,
			Timing: timing, TimeZone: item.Spec.TimeZone, Paused: item.Paused, State: item.State, LastError: item.LastError,
		})
	}
	return result
}

func personaViews(personas []boardroom.Persona) []components.PersonaView {
	result := make([]components.PersonaView, 0, len(personas))
	for _, persona := range personas {
		view := components.PersonaView{Name: persona.Name, Role: persona.Role}
		for _, grant := range persona.Grants {
			view.Tools = append(view.Tools, string(grant.Capability))
		}
		result = append(result, view)
	}
	return result
}

func componentRun(run boardroom.Run, eventCursor int64) components.RunView {
	return components.RunView{
		ID: run.ID.String(), Status: string(run.Status), Prompt: run.Prompt, TurnCount: run.TurnCount,
		EventCursor: eventCursor, CreatedAt: run.CreatedAt, Error: run.Error,
	}
}

func messageViews(messages []boardroom.Message) []components.MessageView {
	result := make([]components.MessageView, 0, len(messages))
	for _, message := range messages {
		result = append(result, components.MessageView{
			ID: message.ID, PersonaName: message.PersonaName, PersonaRole: message.PersonaRole,
			Role: string(message.Role), Body: message.Body, Sequence: message.Sequence, CreatedAt: message.CreatedAt,
		})
	}
	return result
}
