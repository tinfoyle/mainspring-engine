// Package restoregate prevents a database restored from an older backup from
// serving or processing work until its durable Account-erasure ledger has been
// replayed to an externally pinned checkpoint.
package restoregate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Target string

const (
	Global Target = "global"
	Cell   Target = "cell"
)

var (
	ErrInvalidCheckpoint = errors.New("restore checkpoint is invalid")
	ErrReplayRequired    = errors.New("Account erasure restore replay is required")
)

type Checkpoint struct {
	Sequence uint64
	Root     [32]byte
}

func NewCheckpoint(sequence uint64, root []byte) (Checkpoint, error) {
	if len(root) != 32 {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	var result Checkpoint
	result.Sequence = sequence
	copy(result.Root[:], root)
	zeroRoot := bytes.Equal(result.Root[:], make([]byte, 32))
	if (sequence == 0) != zeroRoot {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	return result, nil
}

func InitialCheckpoint() Checkpoint { return Checkpoint{} }

type Gate struct {
	pool         *pgxpool.Pool
	target       Target
	required     Checkpoint
	ownedPool    bool
	mu           sync.Mutex
	lastVerified time.Time
}

const SuccessCacheTTL = 5 * time.Second

func New(pool *pgxpool.Pool, target Target, required Checkpoint) (*Gate, error) {
	zeroRoot := bytes.Equal(required.Root[:], make([]byte, 32))
	if pool == nil || (target != Global && target != Cell) || ((required.Sequence == 0) != zeroRoot) {
		return nil, ErrInvalidCheckpoint
	}
	return &Gate{pool: pool, target: target, required: required}, nil
}

func Open(ctx context.Context, databaseURL string, target Target, required Checkpoint) (*Gate, error) {
	if databaseURL == "" {
		return nil, ErrInvalidCheckpoint
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	gate, err := New(pool, target, required)
	if err != nil {
		pool.Close()
		return nil, err
	}
	gate.ownedPool = true
	if err := gate.Ready(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return gate, nil
}

func (g *Gate) Ready(ctx context.Context) error {
	if g == nil || g.pool == nil {
		return ErrInvalidCheckpoint
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.lastVerified.IsZero() && time.Since(g.lastVerified) < SuccessCacheTTL {
		return nil
	}
	table := "public.account_erasure_restore_ledger"
	if g.target == Cell {
		table = "spyglass.account_erasure_restore_ledger"
	}
	var present bool
	if err := g.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE sequence=$1 AND root=$2)`, g.required.Sequence, g.required.Root[:]).Scan(&present); err != nil {
		return fmt.Errorf("verify %s erasure restore checkpoint: %w", g.target, err)
	}
	if !present {
		return ErrReplayRequired
	}
	g.lastVerified = time.Now()
	return nil
}

func (g *Gate) Close() {
	if g != nil && g.ownedPool && g.pool != nil {
		g.pool.Close()
	}
}
