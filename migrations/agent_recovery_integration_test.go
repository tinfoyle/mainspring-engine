package migrations_test

import (
	"context"
	"encoding/json"
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

func TestAgentRunRecoveryPreservesHistoryAndExactRetryInputs(t *testing.T) {
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

	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("11000000-0000-4000-8000-000000000001")
	userID := ids.UserID("21000000-0000-4000-8000-000000000001")
	boardroomID := ids.BoardroomID("31000000-0000-4000-8000-000000000001")
	personaID := ids.PersonaID("41000000-0000-4000-8000-000000000001")
	personaID2 := ids.PersonaID("42000000-0000-4000-8000-000000000002")
	versionID := ids.PersonaVersionID("51000000-0000-4000-8000-000000000001")
	versionID2 := ids.PersonaVersionID("52000000-0000-4000-8000-000000000002")
	sourceRunID := ids.RunID("61000000-0000-4000-8000-000000000001")
	conversationID := ids.ConversationID("71000000-0000-4000-8000-000000000001")
	userMessageID := ids.MessageID("81000000-0000-4000-8000-000000000001")
	retryRunID := ids.RunID("62000000-0000-4000-8000-000000000002")
	resolutionID := ids.RunResolutionID("91000000-0000-4000-8000-000000000001")
	actor := access.Actor{UserID: userID}

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
	boardroom, err := agentdomain.NewBoardroom(boardroomID, accountID, "Operations", "Coordinate operational decisions", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repository.CreateBoardroom(ctx, boardroom); err != nil || !created {
		t.Fatalf("create Boardroom created=%v err=%v", created, err)
	}
	version, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: versionID, PersonaID: personaID, AccountID: accountID, Version: 1, Name: "Operations Lead", Role: "Operations",
		Description: "Coordinates work", SystemInstructions: "Review the evidence and clearly recommend the next operational step.",
		Policy: agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-test", MaximumInputTokens: 100000, MaximumOutputTokens: 4000,
			MaximumCostMicros: 100000, MaximumToolSteps: 0, CitationPolicy: "best_effort", ActionPolicy: "propose",
			Tools: []agentdomain.ToolGrant{}, OutputSchema: json.RawMessage(agentdomain.ResultSchema())},
		CreatedBy: userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repository.PublishPersona(ctx, boardroomID, version, 0); err != nil || !created {
		t.Fatalf("publish Persona created=%v err=%v", created, err)
	}
	version2, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: versionID2, PersonaID: personaID2, AccountID: accountID, Version: 1, Name: "Synthesis Lead", Role: "Synthesis",
		Description: "Synthesizes specialist work", SystemInstructions: "Synthesize the preceding contribution into a clear operational decision.",
		Policy: agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-test", MaximumInputTokens: 100000, MaximumOutputTokens: 4000,
			MaximumCostMicros: 100000, MaximumToolSteps: 0, CitationPolicy: "best_effort", ActionPolicy: "propose",
			Tools: []agentdomain.ToolGrant{}, OutputSchema: json.RawMessage(agentdomain.ResultSchema())},
		CreatedBy: userID, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := repository.PublishPersona(ctx, boardroomID, version2, 0); err != nil || !created {
		t.Fatalf("publish synthesis Persona created=%v err=%v", created, err)
	}
	source, created, err := repository.StartRun(ctx, agentapp.StartRunDraft{
		Actor: actor, AccountID: accountID, BoardroomID: boardroomID, RunID: sourceRunID, ConversationID: conversationID,
		CreateConversation: true, UserMessageID: userMessageID, Subject: "Weekly review", Prompt: "What should we prioritize?",
		PersonaIDs: []ids.PersonaID{personaID, personaID2}, EntitlementVersion: 3, MaximumConcurrentRun: 2, CreatedAt: now, RequestExpiresAt: now.Add(time.Hour),
	})
	if err != nil || !created || len(source.InvocationIDs) != 2 {
		t.Fatalf("start source Run created=%v run=%+v err=%v", created, source, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=decode(repeat('ab',32),'hex'),failure_code='provider_unavailable',completed_at=$3
		WHERE account_id=$1 AND id=$2`, accountID, source.InvocationIDs[0], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET status='canceled',failure_code='prior_turn_failed',completed_at=$3
		WHERE account_id=$1 AND id=$2`, accountID, source.InvocationIDs[1], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_runs SET state='failed',started_at=$3,completed_at=$3 WHERE account_id=$1 AND id=$2`,
		accountID, sourceRunID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	draft := agentapp.ResolveRunDraft{Actor: actor, AccountID: accountID, RunID: sourceRunID, ResolutionID: resolutionID,
		RetryRunID: retryRunID, Action: agentapp.RunResolutionRetryFailed, Note: "Retry after the provider recovered.",
		EntitlementVersion: 9, MaximumConcurrentRun: 2, CreatedAt: now.Add(2 * time.Minute), RequestExpiresAt: now.Add(2 * time.Hour)}
	resolution, created, err := repository.ResolveRun(ctx, draft)
	if err != nil || !created || resolution.RetryRunID != retryRunID {
		t.Fatalf("retry resolution created=%v resolution=%+v err=%v", created, resolution, err)
	}
	retry, err := repository.GetRun(ctx, accountID, retryRunID)
	if err != nil || retry.State != "planned" || retry.Plan.EntitlementVersion != 9 || retry.Plan.PolicyVersion != source.Plan.PolicyVersion ||
		len(retry.Plan.Turns) != 2 || retry.Plan.Turns[0].PersonaVersionID != versionID || retry.Plan.Turns[1].PersonaVersionID != versionID2 ||
		len(retry.Invocations) != 2 || retry.Invocations[0].Status != "queued" || retry.Invocations[1].Status != "queued" {
		t.Fatalf("retry Run=%+v err=%v", retry, err)
	}
	var sourceContext, retryContext, userMessages, retryDispatch int64
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT context_sequence FROM spyglass.agent_invocation_execution_plans WHERE account_id=$1 AND invocation_id=$2),
		(SELECT context_sequence FROM spyglass.agent_invocation_execution_plans WHERE account_id=$1 AND invocation_id=$3),
		(SELECT count(*) FROM spyglass.agent_user_messages WHERE account_id=$1),
		(SELECT count(*) FROM spyglass.agent_dispatch_queue q JOIN spyglass.agent_invocations i ON i.account_id=q.account_id AND i.id=q.invocation_id WHERE i.account_id=$1 AND i.run_id=$4)`,
		accountID, source.InvocationIDs[0], retry.Invocations[0].ID, retryRunID).Scan(&sourceContext, &retryContext, &userMessages, &retryDispatch); err != nil {
		t.Fatal(err)
	}
	if sourceContext != retryContext || userMessages != 1 || retryDispatch != 1 {
		t.Fatalf("retry context source=%d retry=%d user messages=%d dispatch=%d", sourceContext, retryContext, userMessages, retryDispatch)
	}
	source, err = repository.GetRun(ctx, accountID, sourceRunID)
	if err != nil || source.State != "failed" || len(source.Resolutions) != 1 || source.Resolutions[0].RetryRunID != retryRunID ||
		len(source.Invocations) != 2 || source.Invocations[0].Status != "failed" || source.Invocations[1].Status != "canceled" {
		t.Fatalf("immutable source Run=%+v err=%v", source, err)
	}
	if replay, replayed, err := repository.ResolveRun(ctx, draft); err != nil || replayed || replay.ID != resolution.ID || replay.RunID != resolution.RunID ||
		replay.Action != resolution.Action || replay.Note != resolution.Note || replay.ActorID != resolution.ActorID || replay.RetryRunID != resolution.RetryRunID || !replay.CreatedAt.Equal(resolution.CreatedAt) {
		t.Fatalf("idempotent resolution created=%v resolution=%+v err=%v", replayed, replay, err)
	}
	changed := draft
	changed.Note = "Changed recovery meaning."
	if _, _, err := repository.ResolveRun(ctx, changed); !errors.Is(err, agentapp.ErrConflict) {
		t.Fatalf("changed replay err=%v", err)
	}
	duplicate := draft
	duplicate.ResolutionID = "92000000-0000-4000-8000-000000000002"
	duplicate.RetryRunID = "63000000-0000-4000-8000-000000000003"
	if _, _, err := repository.ResolveRun(ctx, duplicate); !errors.Is(err, agentapp.ErrConflict) {
		t.Fatalf("second source resolution err=%v", err)
	}

	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_invocations SET status='failed',runner_result_digest=decode(repeat('cd',32),'hex'),failure_code='provider_unavailable',completed_at=$3
		WHERE account_id=$1 AND run_id=$2`, accountID, retryRunID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.agent_runs SET state='failed',started_at=$3,completed_at=$3 WHERE account_id=$1 AND id=$2`, accountID, retryRunID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	accepted, created, err := repository.ResolveRun(ctx, agentapp.ResolveRunDraft{Actor: actor, AccountID: accountID, RunID: retryRunID,
		ResolutionID: "93000000-0000-4000-8000-000000000003", Action: agentapp.RunResolutionAcceptFailed,
		Note: "Accepted after an external decision.", EntitlementVersion: 10, CreatedAt: now.Add(4 * time.Minute)})
	if err != nil || !created || accepted.RetryRunID != "" || accepted.Action != agentapp.RunResolutionAcceptFailed {
		t.Fatalf("accept failure created=%v resolution=%+v err=%v", created, accepted, err)
	}
}
