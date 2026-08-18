package memory

import (
	"context"

	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SecurityPostureRepository struct {
	passkeys *PasskeyRepository
	recovery *RecoveryCodeRepository
}

func NewSecurityPostureRepository(passkeys *PasskeyRepository, recovery *RecoveryCodeRepository) *SecurityPostureRepository {
	return &SecurityPostureRepository{passkeys: passkeys, recovery: recovery}
}

func (r *SecurityPostureRepository) Status(_ context.Context, userID ids.UserID) (securityposture.State, error) {
	var result securityposture.State
	if r.passkeys != nil {
		r.passkeys.mu.Lock()
		result.PasskeyCount = len(r.passkeys.credentials[userID])
		r.passkeys.mu.Unlock()
	}
	if r.recovery != nil {
		r.recovery.mu.Lock()
		value, exists := r.recovery.sets[userID]
		result.RecoveryCodesConfigured = exists
		if exists {
			result.RecoveryCodesRemaining = len(value.value.Hashes) - len(value.used)
		}
		r.recovery.mu.Unlock()
	}
	return result, nil
}

var _ securityposture.Store = (*SecurityPostureRepository)(nil)
