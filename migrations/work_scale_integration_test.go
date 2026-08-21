package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

const workPlanSelect = `SELECT account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
	assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
	baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
	capacity_released_at,version,created_at,updated_at FROM spyglass.work_items`

func TestWorkScalePaginationAndContentionContracts(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatalf("apply cell migrations: %v", err)
	}

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	accountA := ids.AccountID("a1000000-0000-4000-8000-000000000001")
	accountB := ids.AccountID("b2000000-0000-4000-8000-000000000002")
	if _, err := owner.Exec(ctx, `
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$3),($2,1,'active',$3);
		INSERT INTO spyglass.work_item_number_counters(account_id,next_number)
		VALUES ($1,60000),($2,20000)`, pgx.QueryExecModeSimpleProtocol, accountA, accountB, now); err != nil {
		t.Fatalf("seed scale Account namespaces: %v", err)
	}

	seedWorkScaleRows(t, ctx, owner, accountA, "a1100000-0000-4000-8000-", 4096, now.Add(-30*24*time.Hour))
	seedWorkScaleRows(t, ctx, owner, accountB, "b2200000-0000-4000-8000-", 16384, now.Add(-60*24*time.Hour))
	parentID := "a1300000-0000-4000-8000-000000000001"
	if _, err := owner.Exec(ctx, `
		INSERT INTO spyglass.work_items
		(account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
		 assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
		 baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
		 capacity_released_at,version,created_at,updated_at)
		VALUES ($1,$2,50000,NULL,0,'ticket','Scale fixture parent','Representative direct-child plan','open','normal','shared',
		 NULL,NULL,NULL,'manual','user','scale-fixture',NULL,NULL,NULL,NULL,NULL,NULL,$2,NULL,1,$3,$3);
		INSERT INTO spyglass.work_items
		(account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
		 assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
		 baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
		 capacity_released_at,version,created_at,updated_at)
		SELECT $1,('a1400000-0000-4000-8000-' || lpad(n::text,12,'0'))::uuid,50000+n,$2,1,'todo',
		       'Scale child ' || n,'Representative direct child','open','normal','shared',NULL,NULL,NULL,
		       'manual','user','scale-fixture',NULL,NULL,NULL,NULL,NULL,NULL,
		       ('a1400000-0000-4000-8000-' || lpad(n::text,12,'0'))::uuid,NULL,1,$3,$3
		FROM generate_series(1,128) AS n`, pgx.QueryExecModeSimpleProtocol, accountA, parentID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed Work child fixture: %v", err)
	}
	if _, err := owner.Exec(ctx, `VACUUM (ANALYZE) spyglass.work_items`); err != nil {
		t.Fatalf("analyze Work scale fixture: %v", err)
	}

	role := "spyglass_work_scale_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA spyglass TO `+role); err != nil {
		t.Fatalf("create Work scale role: %v", err)
	}
	serving := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		serving.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()
	cellPool, err := database.NewCellPool(serving)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewWorkRepository(cellPool, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}

	testWorkRepresentativePlans(t, ctx, cellPool, accountA, parentID)
	testWorkPaginationProperties(t, ctx, cellPool, repository, accountA)
	testWorkCompletionAssignmentContention(t, ctx, cellPool, repository, accountA, now)
}

func seedWorkScaleRows(t *testing.T, ctx context.Context, owner *pgxpool.Pool, accountID ids.AccountID, prefix string, count int, base time.Time) {
	t.Helper()
	_, err := owner.Exec(ctx, `
		WITH generated AS (
			SELECT n,
				CASE n%5 WHEN 0 THEN 'waiting' WHEN 1 THEN 'open' WHEN 2 THEN 'in_progress' WHEN 3 THEN 'done' ELSE 'canceled' END AS state,
				$4::timestamptz + ((n/8) * interval '1 second') AS changed_at
			FROM generate_series(1,$3) AS n
		)
		INSERT INTO spyglass.work_items
		(account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
		 assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
		 baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
		 capacity_released_at,version,created_at,updated_at)
		SELECT $1,($2 || lpad(n::text,12,'0'))::uuid,n,NULL,0,
		       CASE n%2 WHEN 0 THEN 'todo' ELSE 'ticket' END,
		       CASE WHEN n%11=0 THEN 'Quarterly needle ' || n ELSE 'Representative work ' || n END,
		       'Bounded representative Work history row ' || n,state,
		       CASE n%4 WHEN 0 THEN 'low' WHEN 1 THEN 'normal' WHEN 2 THEN 'high' ELSE 'urgent' END,
		       'shared',NULL,NULL,NULL,'manual','user','scale-fixture',NULL,NULL,NULL,NULL,NULL,
		       CASE WHEN state='done' THEN changed_at ELSE NULL END,
		       ($2 || lpad(n::text,12,'0'))::uuid,
		       CASE WHEN state IN ('done','canceled') THEN changed_at ELSE NULL END,
		       1,$4,changed_at
		FROM generated`, accountID, prefix, count, base)
	if err != nil {
		t.Fatalf("seed %s Work scale rows: %v", accountID, err)
	}
}

func testWorkRepresentativePlans(t *testing.T, ctx context.Context, cellPool *database.CellPool, accountID ids.AccountID, parentID string) {
	t.Helper()
	err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		assertPlan := func(name, expectedIndex, query string, arguments ...any) error {
			rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+query, arguments...)
			if err != nil {
				return fmt.Errorf("explain %s: %w", name, err)
			}
			defer rows.Close()
			var lines []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					return err
				}
				lines = append(lines, line)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			plan := strings.Join(lines, "\n")
			if strings.Contains(plan, "Seq Scan on work_items") || !strings.Contains(plan, expectedIndex) {
				return fmt.Errorf("%s plan must use %s without a Work sequential scan:\n%s", name, expectedIndex, plan)
			}
			return nil
		}
		if err := assertPlan("queue", "work_items_queue", workPlanSelect+`
			WHERE account_id=$1
			  AND (cardinality($2::text[])=0 OR state=ANY($2::text[]))
			  AND (cardinality($3::text[])=0 OR kind=ANY($3::text[]))
			  AND ($4='' OR title ILIKE '%' || $4 || '%' OR description ILIKE '%' || $4 || '%')
			  AND ($5::timestamptz IS NULL OR (updated_at,id)<($5,$6::uuid))
			ORDER BY updated_at DESC,id DESC LIMIT $7`, accountID, []string{}, []string{}, "", nil, nil, 101); err != nil {
			return err
		}
		if err := assertPlan("filtered queue", "work_items_state_priority", workPlanSelect+`
			WHERE account_id=$1 AND state=ANY($2::text[]) AND kind=ANY($3::text[])
			  AND (title ILIKE '%' || $4 || '%' OR description ILIKE '%' || $4 || '%')
			ORDER BY updated_at DESC,id DESC LIMIT $5`, accountID, []string{"waiting"}, []string{"todo"}, "needle", 101); err != nil {
			return err
		}
		if err := assertPlan("children", "work_items_children", workPlanSelect+`
			WHERE account_id=$1 AND parent_id=$2 ORDER BY created_at,id LIMIT $3`, accountID, parentID, 100); err != nil {
			return err
		}
		return assertPlan("summary", "work_items_state_priority", `SELECT
			count(*) FILTER (WHERE state IN ('open','in_progress','waiting')),
			count(*) FILTER (WHERE state='in_progress'),count(*) FILTER (WHERE state='waiting'),
			count(*) FILTER (WHERE priority='urgent' AND state IN ('open','in_progress','waiting')),
			count(*) FILTER (WHERE state='done') FROM spyglass.work_items WHERE account_id=$1`, accountID)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testWorkPaginationProperties(t *testing.T, ctx context.Context, cellPool *database.CellPool, repository *postgresadapter.WorkRepository, accountID ids.AccountID) {
	t.Helper()
	var expected int
	if err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.work_items WHERE account_id=$1 AND state='waiting' AND kind='todo'`, accountID).Scan(&expected)
	}); err != nil {
		t.Fatal(err)
	}
	for _, pageSize := range []int{3, 17, 64, 100} {
		seen := make(map[ids.WorkItemID]bool, expected)
		var afterTime *time.Time
		var afterID ids.WorkItemID
		var previous *workdomain.Item
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber > expected {
				t.Fatalf("page size %d did not terminate", pageSize)
			}
			page, err := repository.List(ctx, accountID, workapp.ListQuery{States: []workdomain.State{workdomain.StateWaiting}, Kinds: []workdomain.Kind{workdomain.KindTodo}, AfterUpdatedAt: afterTime, AfterID: afterID, Limit: pageSize})
			if err != nil {
				t.Fatalf("page size %d page %d: %v", pageSize, pageNumber, err)
			}
			for index := range page.Items {
				item := page.Items[index]
				if seen[item.ID] {
					t.Fatalf("page size %d repeated %s", pageSize, item.ID)
				}
				if previous != nil && (previous.UpdatedAt.Before(item.UpdatedAt) || (previous.UpdatedAt.Equal(item.UpdatedAt) && string(previous.ID) <= string(item.ID))) {
					t.Fatalf("page size %d broke descending keyset order: previous=%s/%s current=%s/%s", pageSize, previous.UpdatedAt, previous.ID, item.UpdatedAt, item.ID)
				}
				seen[item.ID] = true
				copy := item
				previous = &copy
			}
			if page.NextCursor == nil {
				break
			}
			cursorTime := page.NextCursor.UpdatedAt
			afterTime, afterID = &cursorTime, page.NextCursor.ID
		}
		if len(seen) != expected {
			t.Fatalf("page size %d returned %d unique rows, want %d", pageSize, len(seen), expected)
		}
	}
}

func testWorkCompletionAssignmentContention(t *testing.T, ctx context.Context, cellPool *database.CellPool, repository *postgresadapter.WorkRepository, accountID ids.AccountID, now time.Time) {
	t.Helper()
	const pairs = 24
	actor := workdomain.Actor{Kind: workdomain.ActorUser, ID: "a1500000-0000-4000-8000-000000000001"}
	type candidate struct {
		item     workdomain.Item
		assigned workdomain.Item
		done     workdomain.Item
	}
	candidates := make([]candidate, 0, pairs)
	for index := 0; index < pairs; index++ {
		itemID := ids.WorkItemID(fmt.Sprintf("a1600000-0000-4000-8000-%012d", index+1))
		draft, err := workdomain.NewDraft(workdomain.Draft{ID: itemID, AccountID: accountID, Kind: workdomain.KindTicket, Title: fmt.Sprintf("Contended Work %02d", index+1), Priority: workdomain.PriorityNormal, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: actor}, CapacityReservationID: string(itemID)})
		if err != nil {
			t.Fatal(err)
		}
		created, err := repository.Create(ctx, draft, workapp.Mutation{Kind: workapp.MutationCreated, Actor: actor, Reason: "contention fixture", CorrelationID: fmt.Sprintf("work-contention-create-%d", index), At: now.Add(time.Duration(index) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		started, err := created.Transition(workdomain.TransitionCommand{To: workdomain.StateInProgress, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: created.Version, At: now.Add(30 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		started, err = repository.Update(ctx, started, created.Version, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "contention fixture start", CorrelationID: fmt.Sprintf("work-contention-start-%d", index), At: now.Add(30 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		assigned, err := started.Assign(workdomain.AssignmentCommand{Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityUser, UserID: ids.UserID(actor.ID)}, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: started.Version, At: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		done, err := started.Transition(workdomain.TransitionCommand{To: workdomain.StateDone, Role: accounts.RoleOwner, Actor: actor, ExpectedVersion: started.Version, At: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		candidates = append(candidates, candidate{item: started, assigned: assigned, done: done})
	}

	type outcome struct {
		index int
		err   error
	}
	outcomes := make(chan outcome, pairs*2)
	var group sync.WaitGroup
	for index, candidate := range candidates {
		index, candidate := index, candidate
		for _, update := range []struct {
			item     workdomain.Item
			mutation workapp.Mutation
		}{
			{candidate.assigned, workapp.Mutation{Kind: workapp.MutationAssigned, Actor: actor, Reason: "contended assignment", CorrelationID: fmt.Sprintf("work-contention-assign-%d", index), At: now.Add(time.Hour)}},
			{candidate.done, workapp.Mutation{Kind: workapp.MutationTransitioned, Actor: actor, Reason: "contended completion", CorrelationID: fmt.Sprintf("work-contention-complete-%d", index), At: now.Add(time.Hour)}},
		} {
			update := update
			group.Add(1)
			go func() {
				defer group.Done()
				_, err := repository.Update(ctx, update.item, candidate.item.Version, update.mutation)
				outcomes <- outcome{index: index, err: err}
			}()
		}
	}
	group.Wait()
	close(outcomes)
	results := make([][]error, pairs)
	for result := range outcomes {
		results[result.index] = append(results[result.index], result.err)
	}
	for index, pairResults := range results {
		if len(pairResults) != 2 {
			t.Fatalf("contention pair %d produced %d results", index, len(pairResults))
		}
		var succeeded, conflicted int
		for _, err := range pairResults {
			if err == nil {
				succeeded++
			} else if errors.Is(err, workapp.ErrConflict) {
				conflicted++
			} else {
				t.Fatalf("contention pair %d returned %v", index, err)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("contention pair %d succeeded=%d conflicted=%d", index, succeeded, conflicted)
		}
		loaded, err := repository.Get(ctx, accountID, candidates[index].item.ID)
		if err != nil || loaded.Version != candidates[index].item.Version+1 {
			t.Fatalf("contention pair %d durable winner=%+v err=%v", index, loaded, err)
		}
		var events, releases int
		if err := cellPool.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM spyglass.work_item_events WHERE account_id=$1 AND work_item_id=$2),
				(SELECT count(*) FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2)`, accountID, loaded.ID).Scan(&events, &releases)
		}); err != nil {
			t.Fatal(err)
		}
		wantReleases := 0
		if loaded.State == workdomain.StateDone {
			wantReleases = 1
		}
		if events != 3 || releases != wantReleases {
			t.Fatalf("contention pair %d events=%d releases=%d want releases=%d", index, events, releases, wantReleases)
		}
	}
}
