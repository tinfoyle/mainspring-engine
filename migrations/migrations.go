// Package migrations embeds and applies Spyglass database migrations.
package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed global/*.sql cell/*.sql development/*.sql
var files embed.FS

type Target string

const (
	Global      Target = "global"
	Cell        Target = "cell"
	Development Target = "development"
)

type Result struct {
	Applied []string
}

// Apply serializes runners per database and target, verifies that previously
// applied migrations are immutable, and applies every new migration atomically.
func Apply(ctx context.Context, pool *pgxpool.Pool, target Target) (Result, error) {
	if pool == nil {
		return Result{}, errors.New("migration database pool is required")
	}
	if !target.valid() {
		return Result{}, fmt.Errorf("unsupported migration target %q", target)
	}
	migrations, err := load(target)
	if err != nil {
		return Result{}, err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()
	lockName := "spyglass:migrations:" + string(target)
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, lockName); err != nil {
		return Result{}, fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockName)
	}()

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS spyglass_schema_migrations (
			target text NOT NULL,
			version bigint NOT NULL,
			name text NOT NULL,
			checksum bytea NOT NULL,
			applied_at timestamptz NOT NULL,
			execution_milliseconds bigint NOT NULL CHECK (execution_milliseconds >= 0),
			PRIMARY KEY (target, version)
		)`); err != nil {
		return Result{}, fmt.Errorf("create migration ledger: %w", err)
	}

	result := Result{Applied: make([]string, 0)}
	for _, migration := range migrations {
		var storedName string
		var storedChecksum []byte
		err := connection.QueryRow(ctx, `SELECT name,checksum FROM spyglass_schema_migrations WHERE target=$1 AND version=$2`, target, migration.version).Scan(&storedName, &storedChecksum)
		switch {
		case err == nil:
			if storedName != migration.name || !bytes.Equal(storedChecksum, migration.checksum[:]) {
				return Result{}, fmt.Errorf("migration %s/%s differs from the applied migration; migrations are immutable", target, migration.name)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return Result{}, fmt.Errorf("read migration ledger: %w", err)
		}

		started := time.Now()
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("begin migration %s: %w", migration.name, err)
		}
		if _, err := tx.Exec(ctx, migration.body); err != nil {
			_ = tx.Rollback(ctx)
			return Result{}, fmt.Errorf("apply migration %s/%s: %w", target, migration.name, err)
		}
		elapsed := time.Since(started).Milliseconds()
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass_schema_migrations (target,version,name,checksum,applied_at,execution_milliseconds) VALUES ($1,$2,$3,$4,statement_timestamp(),$5)`, target, migration.version, migration.name, migration.checksum[:], elapsed); err != nil {
			_ = tx.Rollback(ctx)
			return Result{}, fmt.Errorf("record migration %s: %w", migration.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit migration %s: %w", migration.name, err)
		}
		result.Applied = append(result.Applied, migration.name)
	}
	return result, nil
}

func (target Target) valid() bool {
	return target == Global || target == Cell || target == Development
}

type migration struct {
	version  int64
	name     string
	body     string
	checksum [sha256.Size]byte
}

func load(target Target) ([]migration, error) {
	entries, err := fs.ReadDir(files, string(target))
	if err != nil {
		return nil, fmt.Errorf("read %s migrations: %w", target, err)
	}
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no numeric version prefix", entry.Name())
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q has an invalid version prefix", entry.Name())
		}
		raw, err := files.ReadFile(path.Join(string(target), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		body, err := atomicBody(string(raw))
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", entry.Name(), err)
		}
		result = append(result, migration{version: version, name: entry.Name(), body: body, checksum: sha256.Sum256(raw)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].version < result[j].version })
	for index := 1; index < len(result); index++ {
		if result[index-1].version == result[index].version {
			return nil, fmt.Errorf("duplicate migration version %d for target %s", result[index].version, target)
		}
	}
	return result, nil
}

func atomicBody(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToUpper(trimmed), "BEGIN;") || !strings.HasSuffix(strings.ToUpper(trimmed), "COMMIT;") {
		return "", errors.New("must have a BEGIN; ... COMMIT; envelope")
	}
	body := strings.TrimSpace(trimmed[len("BEGIN;") : len(trimmed)-len("COMMIT;")])
	if body == "" {
		return "", errors.New("is empty")
	}
	return body, nil
}
