package accounterasure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fakeRestoreGlobal struct {
	called    bool
	target    accounterasure.RestoreTarget
	tombstone accounterasure.GlobalTombstone
}

func (s *fakeRestoreGlobal) ResolveRestore(context.Context, string, ids.AccountID, []byte) (accounterasure.RestoreTarget, error) {
	s.called = true
	return s.target, nil
}
func (s *fakeRestoreGlobal) ReplayGlobalRestore(context.Context, accounterasure.GlobalRestoreReplay) (accounterasure.GlobalTombstone, error) {
	return s.tombstone, nil
}
func (s *fakeRestoreGlobal) AttestGlobalErasure(context.Context, string, []byte) (accounterasure.GlobalTombstone, error) {
	return s.tombstone, nil
}

type fakeRestoreCell struct{ tombstone accounterasure.CellTombstone }

func (s *fakeRestoreCell) ReplayCellRestore(context.Context, accounterasure.CellRestoreReplay) (accounterasure.CellTombstone, error) {
	return s.tombstone, nil
}

func TestRestoreServiceRejectsForgedDirectiveBeforeDatabaseAccess(t *testing.T) {
	directive := validRestoreDirectiveFixture()
	key := make([]byte, 32)
	signed, err := accounterasure.SignRestoreDirective(directive, key)
	if err != nil {
		t.Fatal(err)
	}
	signed.Signature[0] ^= 0xff
	global := &fakeRestoreGlobal{}
	service, _ := accounterasure.NewRestoreService(global, &fakeRestoreCell{}, key)
	if _, err := service.Replay(context.Background(), signed, directive.CellID, directive.Environment); !errors.Is(err, accounterasure.ErrInvalidRestoreDirective) {
		t.Fatalf("forged directive=%v", err)
	}
	if global.called {
		t.Fatal("forged directive reached the global database")
	}
}

func validRestoreDirectiveFixture() accounterasure.RestoreDirective {
	prepared := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)
	approved := prepared.Add(time.Hour)
	cellErased := approved.Add(time.Hour)
	completed := cellErased.Add(time.Hour)
	root := make([]byte, 32)
	root[0] = 1
	return accounterasure.RestoreDirective{
		Version: accounterasure.RestoreDirectiveVersion, RequestID: "11111111-1111-4111-8111-111111111111",
		AccountID: "22222222-2222-4222-8222-222222222222", CellID: "cell-a", TombstonePlacementGeneration: 3,
		AccountFingerprint: make([]byte, 32), PolicyVersion: 1, CellRequestVersion: 3, FinalRequestVersion: 5,
		Environment: "test", PreparedAt: prepared, ApprovedAt: approved, CellErasedAt: cellErased, CompletedAt: completed,
		CellTombstoneSHA256: make([]byte, 32), OperatorEvidenceSHA256: make([]byte, 32), BackupExpiresAt: completed.Add(30 * 24 * time.Hour),
		CellCheckpoint:   accounterasure.RestoreCheckpoint{PreviousRoot: make([]byte, 32), Sequence: 1, Root: root},
		GlobalCheckpoint: accounterasure.RestoreCheckpoint{PreviousRoot: make([]byte, 32), Sequence: 1, Root: root},
	}
}
