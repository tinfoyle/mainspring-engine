package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/scheduleaction"
	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	"github.com/tinfoyle/spyglass-engine/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentApprovedScheduleWithDeployedBrokerRole(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("13000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("13000000-0000-4000-8000-000000000002")
	userID := ids.UserID("23000000-0000-4000-8000-000000000001")
	boardroomID := ids.BoardroomID("33000000-0000-4000-8000-000000000001")
	personaID := ids.PersonaID("43000000-0000-4000-8000-000000000001")
	scheduleID := ids.ScheduleID("53000000-0000-4000-8000-000000000001")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := postgresadapter.NewAgentRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agents.CreateBoardroom(ctx, mustBoardroom(t, boardroomID, accountID, now)); err != nil {
		t.Fatal(err)
	}
	persona := mustPersonaVersion(t, "63000000-0000-4000-8000-000000000001", personaID, accountID, 1, "Daily Reviewer", userID, now)
	if _, _, err := agents.PublishPersona(ctx, boardroomID, persona, 0); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("../deploy/docker/spyglass/roles/cell.local.sql")
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err = owner.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "\\") || strings.Contains(line, "\\gexec") {
			continue
		}
		lines = append(lines, line)
	}
	sql := strings.ReplaceAll(strings.Join(lines, "\n"), "ON DATABASE spyglass", "ON DATABASE "+pgx.Identifier{name}.Sanitize())
	if _, err = owner.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	broker := openPool(t, ctx, databaseURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE spyglass_runner_broker")
		return err
	})
	defer broker.Close()
	scoped, err := database.NewCellPool(broker)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewScheduleRepository(scoped)
	if err != nil {
		t.Fatal(err)
	}
	input := scheduleaction.Input{Name: "Agent daily report", Timezone: "America/New_York", LocalHour: 8, BoardroomID: boardroomID, PersonaIDs: []ids.PersonaID{personaID}, Subject: "Daily report", Prompt: "Summarize today's public source captures.", SourceURLs: []string{"https://example.com/prices"}, EmailSelf: true, RunNow: true}
	value, err := input.Schedule(accountID, string(scheduleID), userID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.CreateApprovedSchedule(ctx, value, true, string(scheduleID)); err != nil {
		t.Fatal(err)
	}
	if err = repository.CreateApprovedSchedule(ctx, value, true, string(scheduleID)); err != nil {
		t.Fatal(err)
	}
	if found, err := repository.ApprovedScheduleExists(ctx, accountID, scheduleID, input, userID); err != nil || !found {
		t.Fatalf("reconcile found=%v err=%v", found, err)
	}
	var schedules, triggers int
	if err = owner.QueryRow(ctx, "SELECT (SELECT count(*) FROM spyglass.schedules),(SELECT count(*) FROM spyglass.schedule_triggers)").Scan(&schedules, &triggers); err != nil || schedules != 1 || triggers != 1 {
		t.Fatalf("duplicates %d/%d %v", schedules, triggers, err)
	}
	if _, err = repository.Get(ctx, otherAccountID, scheduleID); !errors.Is(err, scheduleapp.ErrNotFound) {
		t.Fatalf("cross-account leak: %v", err)
	}
	input.BoardroomID = ids.BoardroomID("33000000-0000-4000-8000-000000000099")
	bad, _ := input.Schedule(accountID, "53000000-0000-4000-8000-000000000099", userID, now)
	if err = repository.CreateApprovedSchedule(ctx, bad, true, string(bad.ID)); err == nil {
		t.Fatal("accepted another team's missing ID")
	}
	if _, err = broker.Exec(ctx, "UPDATE spyglass.schedules SET name='unauthorized'"); err == nil {
		t.Fatal("broker can update schedules")
	}
}

func TestToolRouterReceiptWithDeployedGlobalRole(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Global); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	accountID := ids.AccountID("13000000-0000-4000-8000-000000000001")
	seedTwoCellGlobalControl(t, ctx, owner, now, ids.UserID("23000000-0000-4000-8000-000000000001"), accountID,
		ids.AccountID("13000000-0000-4000-8000-000000000002"), ids.CellID("tool-cell-a"), ids.CellID("tool-cell-b"))
	raw, err := os.ReadFile("../deploy/docker/spyglass/roles/global.local.sql")
	if err != nil {
		t.Fatal(err)
	}
	var name string
	_ = owner.QueryRow(ctx, "SELECT current_database()").Scan(&name)
	lines := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "\\") || strings.Contains(line, "\\gexec") {
			continue
		}
		lines = append(lines, line)
	}
	sql := strings.ReplaceAll(strings.Join(lines, "\n"), "ON DATABASE spyglass", "ON DATABASE "+pgx.Identifier{name}.Sanitize())
	if _, err = owner.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	router := openPool(t, ctx, databaseURL, func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE spyglass_app_router")
		return err
	})
	defer router.Close()
	repository, err := postgresadapter.NewToolContextReceiptRepository(router)
	if err != nil {
		t.Fatal(err)
	}
	id := "33000000-0000-4000-8000-000000000003"
	hash := sha256.Sum256([]byte("{}"))
	claims := toolcontext.Claims{IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
		Authority: toolcontext.Authority{RequestID: id, AccountID: accountID, InvocationID: id, PodUID: id, OperationID: id, Capability: "work.summary.read"},
		Binding:   routecontext.Binding{Method: "POST", Target: "/internal/v1/tools:invoke", BodySHA256: hex.EncodeToString(hash[:])}}
	if err = repository.Consume(ctx, claims, now); err != nil {
		t.Fatal(err)
	}
	if err = repository.Consume(ctx, claims, now); !errors.Is(err, toolcontext.ErrReplay) {
		t.Fatalf("replay not rejected: %v", err)
	}
	if _, err = router.Exec(ctx, "SELECT body_sha256 FROM tool_context_receipts"); err == nil {
		t.Fatal("router can read receipt contents")
	}
	if _, err = router.Exec(ctx, "DELETE FROM tool_context_receipts"); err == nil {
		t.Fatal("router can erase replay protection")
	}
}
