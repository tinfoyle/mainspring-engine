package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestManagerLedRunFreezesConfiguredManagerAsFinalTurn(t *testing.T) {
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

	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("14000000-0000-4000-8000-000000000001")
	userID := ids.UserID("24000000-0000-4000-8000-000000000002")
	boardroomID := ids.BoardroomID("34000000-0000-4000-8000-000000000003")
	specialistID := ids.PersonaID("44000000-0000-4000-8000-000000000004")
	managerID := ids.PersonaID("45000000-0000-4000-8000-000000000005")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, accountID, now); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewAgentRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	boardroom, _, err := repository.CreateBoardroom(ctx, mustBoardroom(t, boardroomID, accountID, now))
	if err != nil {
		t.Fatal(err)
	}
	specialistV1 := mustPersonaVersion(t, "54000000-0000-4000-8000-000000000004", specialistID, accountID, 1, "Operations Specialist", userID, now)
	managerV1 := mustPersonaVersion(t, "55000000-0000-4000-8000-000000000005", managerID, accountID, 1, "Synthesis Manager", userID, now)
	if _, _, err := repository.PublishPersona(ctx, boardroomID, specialistV1, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishPersona(ctx, boardroomID, managerV1, 0); err != nil {
		t.Fatal(err)
	}
	configured, changed, err := repository.ConfigureBoardroomManager(ctx, accountID, boardroomID, managerID, boardroom.Version, now.Add(time.Minute))
	if err != nil || !changed || configured.Version != 2 || configured.ManagerPersonaID != managerID {
		t.Fatalf("configured=%+v changed=%v err=%v", configured, changed, err)
	}
	replayed, changed, err := repository.ConfigureBoardroomManager(ctx, accountID, boardroomID, managerID, boardroom.Version, now.Add(2*time.Minute))
	if err != nil || changed || replayed.Version != configured.Version || replayed.UpdatedAt != configured.UpdatedAt {
		t.Fatalf("replayed=%+v changed=%v err=%v", replayed, changed, err)
	}

	runID := ids.RunID("64000000-0000-4000-8000-000000000006")
	run, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID, RunID: runID,
		ConversationID: "74000000-0000-4000-8000-000000000007", CreateConversation: true,
		UserMessageID: "84000000-0000-4000-8000-000000000008", Subject: "Manager-led operating review",
		Prompt: "Synthesize a decision from the specialist evidence.", Mode: agentapp.RunModeManagerLed,
		PersonaIDs: []ids.PersonaID{specialistID}, EntitlementVersion: 7, MaximumConcurrentRun: 4,
		CreatedAt: now.Add(3 * time.Minute), RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || run.Mode != agentapp.RunModeManagerLed || run.Plan.PolicyVersion != configured.Version || len(run.Plan.Turns) != 2 ||
		run.Plan.Turns[0].PersonaID != specialistID || run.Plan.Turns[0].PersonaVersionID != specialistV1.ID || run.Plan.Turns[1].PersonaID != managerID || run.Plan.Turns[1].PersonaVersionID != managerV1.ID {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}

	managerV2 := mustPersonaVersion(t, "56000000-0000-4000-8000-000000000006", managerID, accountID, 2, "Synthesis Manager", userID, now.Add(4*time.Minute))
	if _, _, err := repository.PublishPersona(ctx, boardroomID, managerV2, 1); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.GetRun(ctx, accountID, runID)
	if err != nil || loaded.Mode != agentapp.RunModeManagerLed || loaded.Plan.Turns[1].PersonaVersionID != managerV1.ID || loaded.Plan.Turns[1].PersonaDigest != managerV1.ContentDigest {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	future, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID, RunID: "66000000-0000-4000-8000-000000000006",
		ConversationID: "76000000-0000-4000-8000-000000000007", CreateConversation: true,
		UserMessageID: "86000000-0000-4000-8000-000000000008", Subject: "Future manager version",
		Prompt: "Use the newly published manager version.", Mode: agentapp.RunModeManagerLed,
		PersonaIDs: []ids.PersonaID{specialistID}, EntitlementVersion: 7, MaximumConcurrentRun: 4,
		CreatedAt: now.Add(5 * time.Minute), RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || future.Plan.Turns[1].PersonaVersionID != managerV2.ID || future.Plan.Turns[1].PersonaDigest != managerV2.ContentDigest {
		t.Fatalf("future=%+v created=%v err=%v", future, created, err)
	}

	_, _, err = repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: access.Actor{UserID: userID}, AccountID: accountID, BoardroomID: boardroomID, RunID: "65000000-0000-4000-8000-000000000006",
		ConversationID: "75000000-0000-4000-8000-000000000007", CreateConversation: true,
		UserMessageID: "85000000-0000-4000-8000-000000000008", Subject: "Invalid manager selection", Prompt: "Do not duplicate the manager.",
		Mode: agentapp.RunModeManagerLed, PersonaIDs: []ids.PersonaID{managerID}, EntitlementVersion: 7, MaximumConcurrentRun: 4,
		CreatedAt: now.Add(6 * time.Minute), RequestExpiresAt: now.Add(time.Hour),
	})
	if !errors.Is(err, agentapp.ErrConstraint) {
		t.Fatalf("manager duplicated as specialist err=%v", err)
	}
}

func mustBoardroom(t *testing.T, boardroomID ids.BoardroomID, accountID ids.AccountID, now time.Time) agentdomain.Boardroom {
	t.Helper()
	item, err := agentdomain.NewBoardroom(boardroomID, accountID, "Operating Boardroom", "Coordinate specialist evidence and manager synthesis.", now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func mustPersonaVersion(t *testing.T, versionID string, personaID ids.PersonaID, accountID ids.AccountID, version uint64, name string, userID ids.UserID, now time.Time) agentdomain.PersonaVersion {
	t.Helper()
	item, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: ids.PersonaVersionID(versionID), PersonaID: personaID, AccountID: accountID, Version: version,
		Name: name, Role: "Decision support", Description: "Provides accountable decision support.",
		SystemInstructions: "Review the supplied evidence and produce a concise, accountable contribution.",
		Policy:             agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-test", MaximumInputTokens: 128000, MaximumOutputTokens: 4096, MaximumCostMicros: 100000, MaximumToolSteps: 0, CitationPolicy: "none", ActionPolicy: "none", Tools: []agentdomain.ToolGrant{}, OutputSchema: agentdomain.ResultSchema()},
		CreatedBy:          userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}
