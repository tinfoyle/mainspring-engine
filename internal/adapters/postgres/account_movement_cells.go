package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmovement"
)

type AccountCellMover struct {
	source, destination *pgxpool.Pool
}

func NewAccountCellMover(source, destination *pgxpool.Pool) (*AccountCellMover, error) {
	if source == nil || destination == nil || source == destination {
		return nil, errors.New("distinct source and destination cell pools are required")
	}
	return &AccountCellMover{source: source, destination: destination}, nil
}

func (m *AccountCellMover) FreezeAndStage(ctx context.Context, move accountmovement.Move) error {
	now := time.Now().UTC()
	if _, err := m.source.Exec(ctx, `SELECT public.spyglass_stage_account_move($1,$2,'source',$3,$4,$5)`, move.ID, move.AccountID, move.SourceGeneration, move.DestinationGeneration, now); err != nil {
		return fmt.Errorf("freeze source Account namespace: %w", err)
	}
	if _, err := m.destination.Exec(ctx, `SELECT public.spyglass_stage_account_move($1,$2,'destination',$3,$4,$5)`, move.ID, move.AccountID, move.SourceGeneration, move.DestinationGeneration, now); err != nil {
		return fmt.Errorf("stage destination Account namespace: %w", err)
	}
	return nil
}

func (m *AccountCellMover) Copy(ctx context.Context, move accountmovement.Move) (accountmovement.Evidence, error) {
	tables, err := movementTables(ctx, m.source)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	destinationTables, err := movementTables(ctx, m.destination)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	if !sameMovementTables(tables, destinationTables) {
		return accountmovement.Evidence{}, errors.New("source and destination cell schemas differ")
	}
	sourceTx, err := m.source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	defer sourceTx.Rollback(ctx)
	destinationTx, err := m.destination.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	defer destinationTx.Rollback(ctx)
	if err := setMovementAccount(ctx, sourceTx, move, "source"); err != nil {
		return accountmovement.Evidence{}, err
	}
	if err := setMovementAccount(ctx, destinationTx, move, "destination"); err != nil {
		return accountmovement.Evidence{}, err
	}
	var highWatermark string
	if err := sourceTx.QueryRow(ctx, `SELECT txid_current_snapshot()::text`).Scan(&highWatermark); err != nil {
		return accountmovement.Evidence{}, err
	}
	for index := len(tables) - 1; index >= 0; index-- {
		table := tables[index]
		if _, err := destinationTx.Exec(ctx, `DELETE FROM `+table.qualified()+` WHERE account_id=$1`, move.AccountID); err != nil {
			return accountmovement.Evidence{}, fmt.Errorf("clear destination %s: %w", table.Name, err)
		}
	}
	for _, table := range tables {
		if err := copyMovementTable(ctx, sourceTx, destinationTx, move.AccountID, table); err != nil {
			return accountmovement.Evidence{}, err
		}
	}
	sourceEvidence, err := movementEvidence(ctx, sourceTx, move.AccountID, tables, highWatermark)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	if err := destinationTx.Commit(ctx); err != nil {
		return accountmovement.Evidence{}, err
	}
	if err := sourceTx.Commit(ctx); err != nil {
		return accountmovement.Evidence{}, err
	}
	destinationEvidence, err := m.evidence(ctx, m.destination, move, "destination", tables, highWatermark)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	if !equalEvidence(sourceEvidence, destinationEvidence) {
		return accountmovement.Evidence{}, accountmovement.ErrReconcile
	}
	now := time.Now().UTC()
	if err := recordMovementEvidence(ctx, m.source, move, "source", move.SourceGeneration, sourceEvidence, now); err != nil {
		return accountmovement.Evidence{}, err
	}
	if err := recordMovementEvidence(ctx, m.destination, move, "destination", move.DestinationGeneration, destinationEvidence, now); err != nil {
		return accountmovement.Evidence{}, err
	}
	return sourceEvidence, nil
}

func (m *AccountCellMover) Reconcile(ctx context.Context, move accountmovement.Move) (accountmovement.Evidence, error) {
	tables, err := movementTables(ctx, m.source)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	sourceEvidence, err := m.evidence(ctx, m.source, move, "source", tables, move.SourceHighWatermark)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	expected := accountmovement.Evidence{HighWatermark: move.SourceHighWatermark, Manifest: move.SourceManifest, Digest: move.SourceDigest}
	if !equalEvidence(sourceEvidence, expected) {
		return accountmovement.Evidence{}, accountmovement.ErrReconcile
	}
	destinationTables, err := movementTables(ctx, m.destination)
	if err != nil || !sameMovementTables(tables, destinationTables) {
		return accountmovement.Evidence{}, accountmovement.ErrReconcile
	}
	destinationEvidence, err := m.evidence(ctx, m.destination, move, "destination", tables, move.SourceHighWatermark)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	if !equalEvidence(sourceEvidence, destinationEvidence) {
		return accountmovement.Evidence{}, accountmovement.ErrReconcile
	}
	return destinationEvidence, nil
}

func (m *AccountCellMover) ActivateDestination(ctx context.Context, move accountmovement.Move) error {
	_, err := m.destination.Exec(ctx, `SELECT public.spyglass_activate_account_move_namespace($1,$2,'destination',$3,$3,$4)`, move.ID, move.AccountID, move.DestinationGeneration, time.Now().UTC())
	return err
}

func (m *AccountCellMover) ActivateRollback(ctx context.Context, move accountmovement.Move) error {
	if move.RollbackGeneration == 0 {
		return accountmovement.ErrStateConflict
	}
	now := time.Now().UTC()
	if _, err := m.source.Exec(ctx, `SELECT public.spyglass_activate_account_move_namespace($1,$2,'source',$3,$4,$5)`, move.ID, move.AccountID, move.SourceGeneration, move.RollbackGeneration, now); err != nil {
		return err
	}
	_, err := m.destination.Exec(ctx, `SELECT public.spyglass_freeze_account_move_destination($1,$2,$3,$4)`, move.ID, move.AccountID, move.DestinationGeneration, now)
	return err
}

func (m *AccountCellMover) RetireSource(ctx context.Context, move accountmovement.Move) error {
	tables, err := movementTables(ctx, m.source)
	if err != nil {
		return err
	}
	tx, err := m.source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setMovementAccount(ctx, tx, move, "source"); err != nil {
		return err
	}
	var generation uint64
	var state string
	if err := tx.QueryRow(ctx, `SELECT placement_generation,state FROM spyglass.account_namespaces WHERE account_id=$1 FOR UPDATE`, move.AccountID).Scan(&generation, &state); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var checkpointState string
		if checkpointErr := tx.QueryRow(ctx, `SELECT state FROM spyglass.account_move_checkpoints WHERE account_id=$1 AND move_id=$2 AND role='source'`, move.AccountID, move.ID).Scan(&checkpointState); checkpointErr != nil || checkpointState != "retired" {
			return accountmovement.ErrStateConflict
		}
		return tx.Commit(ctx)
	}
	if generation != move.SourceGeneration || state != "moving" {
		return accountmovement.ErrStateConflict
	}
	for index := len(tables) - 1; index >= 0; index-- {
		if _, err := tx.Exec(ctx, `DELETE FROM `+tables[index].qualified()+` WHERE account_id=$1`, move.AccountID); err != nil {
			return fmt.Errorf("retire source %s: %w", tables[index].Name, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM spyglass.account_namespaces WHERE account_id=$1`, move.AccountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE spyglass.account_move_checkpoints SET state='retired',updated_at=$2 WHERE account_id=$1 AND move_id=$3 AND role='source'`, move.AccountID, time.Now().UTC(), move.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *AccountCellMover) evidence(ctx context.Context, pool *pgxpool.Pool, move accountmovement.Move, role string, tables []movementTable, highWatermark string) (accountmovement.Evidence, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	defer tx.Rollback(ctx)
	if err := setMovementAccount(ctx, tx, move, role); err != nil {
		return accountmovement.Evidence{}, err
	}
	result, err := movementEvidence(ctx, tx, move.AccountID, tables, highWatermark)
	if err != nil {
		return accountmovement.Evidence{}, err
	}
	return result, tx.Commit(ctx)
}

type movementTable struct {
	Name       string
	Columns    []string
	PrimaryKey []string
}

func (t movementTable) qualified() string { return pgx.Identifier{"spyglass", t.Name}.Sanitize() }

func movementTables(ctx context.Context, pool *pgxpool.Pool) ([]movementTable, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname,a.attname,a.attnum
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
		WHERE n.nspname='spyglass' AND c.relkind='r'
		  AND c.relname NOT IN ('account_namespaces','account_move_checkpoints','account_erasure_tombstones','account_erasure_restore_ledger','integration_health_probe_queue','integration_source_sync_queue')
		  AND EXISTS (SELECT 1 FROM pg_catalog.pg_attribute account_column WHERE account_column.attrelid=c.oid AND account_column.attname='account_id' AND account_column.attnum>0 AND NOT account_column.attisdropped)
		ORDER BY c.relname,a.attnum`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*movementTable{}
	for rows.Next() {
		var name, column string
		var position int
		if err := rows.Scan(&name, &column, &position); err != nil {
			return nil, err
		}
		table := byName[name]
		if table == nil {
			table = &movementTable{Name: name}
			byName[name] = table
		}
		table.Columns = append(table.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pkRows, err := pool.Query(ctx, `
		SELECT c.relname,a.attname
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_catalog.pg_index i ON i.indrelid=c.oid AND i.indisprimary
		JOIN LATERAL unnest(i.indkey) WITH ORDINALITY key(attnum,ordinality) ON true
		JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum=key.attnum
		WHERE n.nspname='spyglass' ORDER BY c.relname,key.ordinality`)
	if err != nil {
		return nil, err
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var name, column string
		if err := pkRows.Scan(&name, &column); err != nil {
			return nil, err
		}
		if table := byName[name]; table != nil {
			table.PrimaryKey = append(table.PrimaryKey, column)
		}
	}
	dependencies := map[string]map[string]struct{}{}
	edgeRows, err := pool.Query(ctx, `
		SELECT child.relname,parent.relname
		FROM pg_catalog.pg_constraint constraint_row
		JOIN pg_catalog.pg_class child ON child.oid=constraint_row.conrelid
		JOIN pg_catalog.pg_class parent ON parent.oid=constraint_row.confrelid
		JOIN pg_catalog.pg_namespace child_ns ON child_ns.oid=child.relnamespace
		JOIN pg_catalog.pg_namespace parent_ns ON parent_ns.oid=parent.relnamespace
		WHERE constraint_row.contype='f' AND child_ns.nspname='spyglass' AND parent_ns.nspname='spyglass'`)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var child, parent string
		if err := edgeRows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child != parent && byName[child] != nil && byName[parent] != nil {
			if dependencies[child] == nil {
				dependencies[child] = map[string]struct{}{}
			}
			dependencies[child][parent] = struct{}{}
		}
	}
	return orderMovementTables(byName, dependencies)
}

func orderMovementTables(byName map[string]*movementTable, dependencies map[string]map[string]struct{}) ([]movementTable, error) {
	remaining := make(map[string]map[string]struct{}, len(byName))
	for name, table := range byName {
		if len(table.PrimaryKey) == 0 {
			return nil, fmt.Errorf("Account movement table %s has no primary key", name)
		}
		remaining[name] = dependencies[name]
	}
	result := make([]movementTable, 0, len(byName))
	for len(remaining) > 0 {
		var ready []string
		for name, needs := range remaining {
			if len(needs) == 0 {
				ready = append(ready, name)
			}
		}
		if len(ready) == 0 {
			return nil, errors.New("Account movement table dependency cycle")
		}
		sort.Strings(ready)
		for _, name := range ready {
			result = append(result, *byName[name])
			delete(remaining, name)
			for _, needs := range remaining {
				delete(needs, name)
			}
		}
	}
	return result, nil
}

func copyMovementTable(ctx context.Context, source, destination pgx.Tx, accountID any, table movementTable) error {
	columns := quotedIdentifiers(table.Columns)
	rows, err := source.Query(ctx, `SELECT `+columns+` FROM `+table.qualified()+` WHERE account_id=$1 ORDER BY `+quotedIdentifiers(table.PrimaryKey), accountID)
	if err != nil {
		return fmt.Errorf("read source %s: %w", table.Name, err)
	}
	defer rows.Close()
	placeholders := make([]string, len(table.Columns))
	for index := range placeholders {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
	}
	insert := `INSERT INTO ` + table.qualified() + ` (` + columns + `) VALUES (` + strings.Join(placeholders, ",") + `)`
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return err
		}
		if _, err := destination.Exec(ctx, insert, values...); err != nil {
			return fmt.Errorf("write destination %s: %w", table.Name, err)
		}
	}
	return rows.Err()
}

func movementEvidence(ctx context.Context, tx pgx.Tx, accountID any, tables []movementTable, highWatermark string) (accountmovement.Evidence, error) {
	digest := sha256.New()
	manifest := make(map[string]int64, len(tables))
	for _, table := range tables {
		query := `SELECT row_to_json(movement_row)::text FROM (SELECT ` + quotedIdentifiers(table.Columns) + ` FROM ` + table.qualified() + ` WHERE account_id=$1 ORDER BY ` + quotedIdentifiers(table.PrimaryKey) + `) movement_row`
		rows, err := tx.Query(ctx, query, accountID)
		if err != nil {
			return accountmovement.Evidence{}, err
		}
		var count int64
		_, _ = digest.Write([]byte(table.Name + "\x00"))
		for rows.Next() {
			var canonical string
			if err := rows.Scan(&canonical); err != nil {
				rows.Close()
				return accountmovement.Evidence{}, err
			}
			_, _ = fmt.Fprintf(digest, "%d:%s", len(canonical), canonical)
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return accountmovement.Evidence{}, err
		}
		rows.Close()
		manifest[table.Name] = count
	}
	return accountmovement.Evidence{HighWatermark: highWatermark, Manifest: manifest, Digest: digest.Sum(nil)}, nil
}

func recordMovementEvidence(ctx context.Context, pool *pgxpool.Pool, move accountmovement.Move, role string, generation uint64, evidence accountmovement.Evidence, at time.Time) error {
	raw, err := json.Marshal(evidence.Manifest)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `SELECT public.spyglass_record_account_move_copy($1,$2,$3,$4,$5,$6,$7,$8)`, move.ID, move.AccountID, role, generation, evidence.HighWatermark, raw, evidence.Digest, at)
	return err
}

func setMovementAccount(ctx context.Context, tx pgx.Tx, move accountmovement.Move, role string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.account_id',$1::text,true),
		set_config('spyglass.account_movement','on',true),set_config('spyglass.account_movement_id',$2,true),
		set_config('spyglass.account_movement_role',$3,true)`, move.AccountID, move.ID, role)
	return err
}

func quotedIdentifiers(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = pgx.Identifier{value}.Sanitize()
	}
	return strings.Join(quoted, ",")
}

func sameMovementTables(left, right []movementTable) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func equalEvidence(left, right accountmovement.Evidence) bool {
	if !strings.EqualFold(fmt.Sprintf("%x", left.Digest), fmt.Sprintf("%x", right.Digest)) || len(left.Manifest) != len(right.Manifest) {
		return false
	}
	for table, count := range left.Manifest {
		if right.Manifest[table] != count {
			return false
		}
	}
	return true
}

var _ accountmovement.Cells = (*AccountCellMover)(nil)
